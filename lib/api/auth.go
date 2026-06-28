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

// authorize requires a verified email on the owner allow-list. Pure, so the
// policy is unit-testable without real tokens.
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

// emailVerified handles the claim as either a bool or the string "true".
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
