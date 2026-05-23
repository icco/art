package handlers_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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

func (f *fakeOAuth) StartURL(account string) (string, error) { return f.url, f.startErr }
func (f *fakeOAuth) Complete(ctx context.Context, state, code string) (string, string, error) {
	return f.account, f.email, f.compErr
}

func TestOAuthStart(t *testing.T) {
	h := &handlers.Handlers{OAuth: &fakeOAuth{url: "https://google/consent"}}
	w := httptest.NewRecorder()
	h.OAuthStart(w, httptest.NewRequest("POST", "/oauth/start?account=personal", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("code: %d", w.Code)
	}
}

func TestOAuthStartMissingAccount(t *testing.T) {
	h := &handlers.Handlers{OAuth: &fakeOAuth{}}
	w := httptest.NewRecorder()
	h.OAuthStart(w, httptest.NewRequest("POST", "/oauth/start", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code: %d", w.Code)
	}
}

func TestOAuthStartErr(t *testing.T) {
	h := &handlers.Handlers{OAuth: &fakeOAuth{startErr: errors.New("nope")}}
	w := httptest.NewRecorder()
	h.OAuthStart(w, httptest.NewRequest("POST", "/oauth/start?account=personal", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code: %d", w.Code)
	}
}

func TestOAuthCallback(t *testing.T) {
	h := &handlers.Handlers{OAuth: &fakeOAuth{account: "personal", email: "a@b.com"}}
	w := httptest.NewRecorder()
	h.OAuthCallback(w, httptest.NewRequest("GET", "/oauth/callback?state=s&code=c", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("code: %d", w.Code)
	}
}

func TestOAuthCallbackError(t *testing.T) {
	h := &handlers.Handlers{OAuth: &fakeOAuth{}}
	w := httptest.NewRecorder()
	h.OAuthCallback(w, httptest.NewRequest("GET", "/oauth/callback?error=denied", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code: %d", w.Code)
	}
}

func TestOAuthCallbackCompleteErr(t *testing.T) {
	h := &handlers.Handlers{OAuth: &fakeOAuth{compErr: errors.New("bad")}}
	w := httptest.NewRecorder()
	h.OAuthCallback(w, httptest.NewRequest("GET", "/oauth/callback?state=s&code=c", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("code: %d", w.Code)
	}
}
