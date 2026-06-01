package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/icco/art/lib/models"
	"golang.org/x/oauth2"
	googleoauth "golang.org/x/oauth2/google"
	"google.golang.org/api/calendar/v3"
	oauthv2 "google.golang.org/api/oauth2/v2"
	"google.golang.org/api/option"
)

// Scopes is the set of Google OAuth scopes art requests at account linking.
var Scopes = []string{
	calendar.CalendarScope,
	"openid",
	"https://www.googleapis.com/auth/userinfo.email",
	"https://www.googleapis.com/auth/userinfo.profile",
}

// Flow runs the Google OAuth authorization code exchange and persists the
// resulting refresh token via Store.
type Flow struct {
	OAuth   *oauth2.Config
	Store   *Store
	pending sync.Map // state -> pendingState
}

type pendingState struct {
	kind      models.AccountKind
	expiresAt time.Time
}

// NewFlow returns a Flow configured to exchange Google OAuth codes.
func NewFlow(clientID, clientSecret, redirectURL string, store *Store) *Flow {
	return &Flow{
		OAuth: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Scopes:       Scopes,
			Endpoint:     googleoauth.Endpoint,
		},
		Store: store,
	}
}

// StartURL returns the Google consent URL for linking the given account kind.
func (f *Flow) StartURL(account string) (string, error) {
	kind := models.AccountKind(account)
	if !kind.Valid() {
		return "", fmt.Errorf("oauth: unknown account kind %q", account)
	}
	f.gcExpired(time.Now())
	state, err := randState()
	if err != nil {
		return "", err
	}
	f.pending.Store(state, pendingState{kind: kind, expiresAt: time.Now().Add(10 * time.Minute)})
	return f.OAuth.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.ApprovalForce, // ensures Google returns a refresh_token every time
	), nil
}

// gcExpired keeps the in-memory state map from growing unbounded when
// callers start flows they never complete.
func (f *Flow) gcExpired(now time.Time) {
	f.pending.Range(func(k, v any) bool {
		if p, ok := v.(pendingState); ok && now.After(p.expiresAt) {
			f.pending.Delete(k)
		}
		return true
	})
}

// Complete exchanges code for a token, persists it, and returns (kind, email).
func (f *Flow) Complete(ctx context.Context, state, code string) (string, string, error) {
	raw, ok := f.pending.LoadAndDelete(state)
	if !ok {
		return "", "", errors.New("oauth: unknown or expired state")
	}
	p := raw.(pendingState)
	if time.Now().After(p.expiresAt) {
		return "", "", errors.New("oauth: state expired")
	}

	tok, err := f.OAuth.Exchange(ctx, code)
	if err != nil {
		return "", "", fmt.Errorf("oauth: exchange: %w", err)
	}
	if tok.RefreshToken == "" {
		return "", "", errors.New("oauth: Google did not return a refresh token; revoke the app's access and retry")
	}

	ts := f.OAuth.TokenSource(ctx, tok)
	email, err := fetchEmail(ctx, ts)
	if err != nil {
		return "", "", fmt.Errorf("oauth: userinfo: %w", err)
	}
	primary, err := fetchPrimaryCalendar(ctx, ts)
	if err != nil {
		return "", "", fmt.Errorf("oauth: primary calendar: %w", err)
	}
	if err := f.Store.Save(ctx, p.kind, email, primary, tok); err != nil {
		return "", "", fmt.Errorf("oauth: save: %w", err)
	}
	return string(p.kind), email, nil
}

// TokenSource returns a refreshing oauth2.TokenSource for the linked account.
func (f *Flow) TokenSource(ctx context.Context, kind models.AccountKind) (oauth2.TokenSource, models.Account, error) {
	tok, acct, err := f.Store.Load(ctx, kind)
	if err != nil {
		return nil, acct, err
	}
	return f.OAuth.TokenSource(ctx, tok), acct, nil
}

func fetchEmail(ctx context.Context, ts oauth2.TokenSource) (string, error) {
	svc, err := oauthv2.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return "", err
	}
	info, err := svc.Userinfo.Get().Context(ctx).Do()
	if err != nil {
		return "", err
	}
	return info.Email, nil
}

func fetchPrimaryCalendar(ctx context.Context, ts oauth2.TokenSource) (string, error) {
	svc, err := calendar.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return "", err
	}
	cal, err := svc.Calendars.Get("primary").Context(ctx).Do()
	if err != nil {
		return "", err
	}
	return cal.Id, nil
}

func randState() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
