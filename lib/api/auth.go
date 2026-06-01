// Package api wires HTTP middleware and routes for the art API server.
package api

import (
	"context"
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
			email, _ := payload.Claims["email"].(string)
			if email == "" || !cfg.OwnerAllowed(email) {
				http.Error(w, "not authorized", http.StatusForbidden)
				return
			}
			ctx := context.WithValue(r.Context(), idCtxKey{}, Identity{Email: email})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// FromContext returns the Identity stored on ctx by OIDCMiddleware, if any.
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(idCtxKey{}).(Identity)
	return id, ok
}
