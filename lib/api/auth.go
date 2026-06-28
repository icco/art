// Package api wires HTTP middleware and routes for the art API server.
package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/icco/art/lib/config"
	"google.golang.org/api/idtoken"
)

type idCtxKey struct{}

// Identity is the authenticated caller information stored on a request context.
type Identity struct {
	Email string
}

// errNotAuthorized is returned by authorize when a validated token's claims do
// not grant access. It maps to a 403 with a generic message (no claim echo).
var errNotAuthorized = errors.New("not authorized")

// OIDCMiddleware verifies the Google ID token and gates on OWNER_EMAILS.
func OIDCMiddleware(cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") {
				http.Error(w, "missing bearer token", http.StatusUnauthorized)
				return
			}
			tok := strings.TrimPrefix(auth, "Bearer ")
			payload, err := idtoken.Validate(r.Context(), tok, cfg.OIDCAudience)
			if err != nil {
				http.Error(w, "invalid id token", http.StatusUnauthorized)
				return
			}
			id, err := authorize(payload, cfg)
			if err != nil {
				http.Error(w, "not authorized", http.StatusForbidden)
				return
			}
			ctx := context.WithValue(r.Context(), idCtxKey{}, id)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// authorize maps a validated ID token's claims to an Identity. It requires a
// verified email that is on the owner allow-list. It is pure (no I/O) so the
// authorization policy can be unit-tested without minting real Google tokens.
func authorize(payload *idtoken.Payload, cfg *config.Config) (Identity, error) {
	if payload == nil || !emailVerified(payload.Claims) {
		return Identity{}, errNotAuthorized
	}
	email, _ := payload.Claims["email"].(string)
	if email == "" || !cfg.OwnerAllowed(email) {
		return Identity{}, errNotAuthorized
	}
	return Identity{Email: email}, nil
}

// emailVerified reports whether the token asserts a verified email. Google ID
// tokens encode the claim as a bool; some issuers use the string "true". Any
// other value (including absent) is treated as unverified.
func emailVerified(claims map[string]any) bool {
	switch v := claims["email_verified"].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	default:
		return false
	}
}

// FromContext returns the Identity stored on ctx by OIDCMiddleware, if any.
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(idCtxKey{}).(Identity)
	return id, ok
}
