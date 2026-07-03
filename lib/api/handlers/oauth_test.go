package handlers_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/icco/art/lib/api/handlers"
)

type fakeOAuth struct {
	startErr error
	url      string
	account  string
	email    string
	compErr  error
}

func (f *fakeOAuth) StartURL(_ string) (string, error) { return f.url, f.startErr }
func (f *fakeOAuth) Complete(_ context.Context, _, _ string) (string, string, error) {
	return f.account, f.email, f.compErr
}

func TestOAuthStart(t *testing.T) {
	h := &handlers.Handlers{OAuth: &fakeOAuth{url: "https://google/consent"}}
	w := httptest.NewRecorder()
	h.OAuthStart(w, httptest.NewRequestWithContext(t.Context(), "POST", "/oauth/start?account=personal", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("code: %d", w.Code)
	}
}

func TestOAuthStartMissingAccount(t *testing.T) {
	h := &handlers.Handlers{OAuth: &fakeOAuth{}}
	w := httptest.NewRecorder()
	h.OAuthStart(w, httptest.NewRequestWithContext(t.Context(), "POST", "/oauth/start", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code: %d", w.Code)
	}
}

func TestOAuthStartErr(t *testing.T) {
	h := &handlers.Handlers{OAuth: &fakeOAuth{startErr: errors.New("nope")}}
	w := httptest.NewRecorder()
	h.OAuthStart(w, httptest.NewRequestWithContext(t.Context(), "POST", "/oauth/start?account=personal", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code: %d", w.Code)
	}
}

func TestOAuthCallback(t *testing.T) {
	h := &handlers.Handlers{OAuth: &fakeOAuth{account: "personal", email: "a@b.com"}}
	w := httptest.NewRecorder()
	h.OAuthCallback(w, httptest.NewRequestWithContext(t.Context(), "GET", "/oauth/callback?state=s&code=c", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("code: %d", w.Code)
	}
}

func TestOAuthCallbackError(t *testing.T) {
	h := &handlers.Handlers{OAuth: &fakeOAuth{}}
	w := httptest.NewRecorder()
	h.OAuthCallback(w, httptest.NewRequestWithContext(t.Context(), "GET", "/oauth/callback?error=denied", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code: %d", w.Code)
	}
}

func TestOAuthCallbackCompleteErr(t *testing.T) {
	h := &handlers.Handlers{OAuth: &fakeOAuth{compErr: errors.New("bad")}}
	w := httptest.NewRecorder()
	h.OAuthCallback(w, httptest.NewRequestWithContext(t.Context(), "GET", "/oauth/callback?state=s&code=c", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code: %d", w.Code)
	}
}

// The callback is a public route: internal error detail (GORM/pq messages,
// table names) must never render to the browser.
func TestOAuthCallbackDoesNotLeakInternalErrors(t *testing.T) {
	h := &handlers.Handlers{OAuth: &fakeOAuth{
		compErr: errors.New(`oauth: save: pq: duplicate key value violates "accounts_pkey"`),
	}}
	w := httptest.NewRecorder()
	h.OAuthCallback(w, httptest.NewRequestWithContext(t.Context(), "GET", "/oauth/callback?state=s&code=c", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code: %d", w.Code)
	}
	if body := w.Body.String(); strings.Contains(body, "accounts_pkey") || strings.Contains(body, "pq:") {
		t.Fatalf("internal error detail leaked to public page:\n%s", body)
	}
}
