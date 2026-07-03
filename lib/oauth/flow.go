package oauth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/icco/art/lib/models"
	"golang.org/x/oauth2"
	googleoauth "golang.org/x/oauth2/google"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/gmail/v1"
	oauthv2 "google.golang.org/api/oauth2/v2"
	"google.golang.org/api/option"
)

// Scopes is the set of Google OAuth scopes art requests at account linking.
//
// gmail.GmailModifyScope is a Google "restricted" scope: it allows reading and
// changing labels, which is the minimum Google offers that still permits
// archiving (removing the INBOX label). Art uses it only to label and archive —
// it never drafts, sends, or deletes mail. Adding it forces re-consent on every
// linked account.
var Scopes = []string{
	calendar.CalendarScope,
	gmail.GmailModifyScope,
	"openid",
	"https://www.googleapis.com/auth/userinfo.email",
	"https://www.googleapis.com/auth/userinfo.profile",
}

// Flow runs the Google OAuth authorization code exchange and persists the
// resulting refresh token via Store.
type Flow struct {
	OAuth *oauth2.Config
	Store *Store
	// RevokeURL is Google's token revocation endpoint; overridable in tests.
	RevokeURL string
	pending   sync.Map // state -> pendingState

	mu      sync.Mutex
	sources map[models.AccountKind]*accountSource
}

type accountSource struct {
	ts   oauth2.TokenSource
	acct models.Account
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
		Store:     store,
		RevokeURL: "https://oauth2.googleapis.com/revoke",
		sources:   map[models.AccountKind]*accountSource{},
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
		f.revoke(ctx, tok.RefreshToken)
		return "", "", fmt.Errorf("oauth: userinfo: %w", err)
	}
	primary, err := fetchPrimaryCalendar(ctx, ts)
	if err != nil {
		f.revoke(ctx, tok.RefreshToken)
		return "", "", fmt.Errorf("oauth: primary calendar: %w", err)
	}
	if err := f.Store.Save(ctx, p.kind, email, primary, tok); err != nil {
		f.revoke(ctx, tok.RefreshToken)
		return "", "", fmt.Errorf("oauth: save: %w", err)
	}
	f.dropSource(p.kind)
	return string(p.kind), email, nil
}

// revoke best-effort invalidates a refresh token that won't be persisted.
func (f *Flow) revoke(ctx context.Context, token string) {
	if token == "" || f.RevokeURL == "" {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.RevokeURL,
		strings.NewReader(url.Values{"token": {token}}.Encode()))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if resp, err := http.DefaultClient.Do(req); err == nil {
		_ = resp.Body.Close()
	}
}

// TokenSource returns a cached, self-refreshing oauth2.TokenSource for the
// linked account, persisting rotated refresh tokens.
func (f *Flow) TokenSource(ctx context.Context, kind models.AccountKind) (oauth2.TokenSource, models.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if s, ok := f.sources[kind]; ok {
		return s.ts, s.acct, nil
	}
	tok, acct, err := f.Store.Load(ctx, kind)
	if err != nil {
		return nil, acct, err
	}
	// The source outlives this call; don't tie refreshes to the request ctx.
	ts := &persistingSource{
		inner: f.OAuth.TokenSource(context.WithoutCancel(ctx), tok),
		store: f.Store,
		kind:  kind,
		last:  tok.RefreshToken,
	}
	f.sources[kind] = &accountSource{ts: ts, acct: acct}
	return ts, acct, nil
}

func (f *Flow) dropSource(kind models.AccountKind) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sources, kind)
}

// persistingSource saves the refresh token whenever Google rotates it.
type persistingSource struct {
	inner oauth2.TokenSource
	store *Store
	kind  models.AccountKind

	mu   sync.Mutex
	last string
}

func (p *persistingSource) Token() (*oauth2.Token, error) {
	tok, err := p.inner.Token()
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if tok.RefreshToken != "" && tok.RefreshToken != p.last {
		if err := p.store.UpdateRefreshToken(context.Background(), p.kind, tok.RefreshToken); err != nil {
			return nil, fmt.Errorf("oauth: persist rotated refresh token: %w", err)
		}
		p.last = tok.RefreshToken
	}
	return tok, nil
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
