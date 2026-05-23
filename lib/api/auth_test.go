package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/icco/art/lib/config"
)

func TestOIDCMiddleware_NoBearer(t *testing.T) {
	cfg := &config.Config{OIDCAudience: "x", OwnerEmails: []string{"a@b.com"}}
	h := OIDCMiddleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not run")
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no bearer: got %d", w.Code)
	}
}

func TestOIDCMiddleware_BadToken(t *testing.T) {
	cfg := &config.Config{OIDCAudience: "x", OwnerEmails: []string{"a@b.com"}}
	h := OIDCMiddleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not run")
	}))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer not-a-real-token")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("bad token: got %d", w.Code)
	}
}

func TestFromContext_Empty(t *testing.T) {
	if _, ok := FromContext(context.Background()); ok {
		t.Fatal("FromContext should return ok=false on empty ctx")
	}
}
