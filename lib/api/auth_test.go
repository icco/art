package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/icco/art/lib/config"
	"google.golang.org/api/idtoken"
)

func TestAuthorize(t *testing.T) {
	cfg := &config.Config{OwnerEmails: []string{"owner@example.com"}}
	cases := []struct {
		name    string
		claims  map[string]any
		wantErr bool
		want    string
	}{
		{
			name:   "verified owner (bool)",
			claims: map[string]any{"email": "owner@example.com", "email_verified": true},
			want:   "owner@example.com",
		},
		{
			name:   "verified owner (string true)",
			claims: map[string]any{"email": "Owner@Example.com", "email_verified": "true"},
			want:   "Owner@Example.com",
		},
		{
			name:    "unverified email",
			claims:  map[string]any{"email": "owner@example.com", "email_verified": false},
			wantErr: true,
		},
		{
			name:    "missing email_verified",
			claims:  map[string]any{"email": "owner@example.com"},
			wantErr: true,
		},
		{
			name:    "verified but not on allow-list",
			claims:  map[string]any{"email": "stranger@example.com", "email_verified": true},
			wantErr: true,
		},
		{
			name:    "empty email",
			claims:  map[string]any{"email": "", "email_verified": true},
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, err := authorize(&idtoken.Payload{Claims: tc.claims}, cfg)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got identity %+v", id)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if id.Email != tc.want {
				t.Fatalf("email: got %q want %q", id.Email, tc.want)
			}
		})
	}
}

func TestAuthorize_NilPayload(t *testing.T) {
	cfg := &config.Config{OwnerEmails: []string{"owner@example.com"}}
	if _, err := authorize(nil, cfg); err == nil {
		t.Fatal("expected error for nil payload")
	}
}

func TestOIDCMiddleware_NoBearer(t *testing.T) {
	cfg := &config.Config{OIDCAudience: "x", OwnerEmails: []string{"a@b.com"}}
	h := OIDCMiddleware(cfg)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("handler should not run")
	}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), "GET", "/", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("no bearer: got %d", w.Code)
	}
}

func TestOIDCMiddleware_BadToken(t *testing.T) {
	cfg := &config.Config{OIDCAudience: "x", OwnerEmails: []string{"a@b.com"}}
	h := OIDCMiddleware(cfg)(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		t.Fatal("handler should not run")
	}))
	req := httptest.NewRequestWithContext(context.Background(), "GET", "/", nil)
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
