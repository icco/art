package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/icco/art/lib/config"
	"google.golang.org/api/idtoken"
)

type idCtxKey struct{}

// Identity is the subject extracted from a verified ID token.
type Identity struct {
	Email string
}

// OIDCMiddleware verifies an inbound Google ID token and rejects requests whose
// email is not on the OWNER_EMAILS allowlist.
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

func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(idCtxKey{}).(Identity)
	return id, ok
}
