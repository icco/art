package api

import (
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/httprate"
	"github.com/icco/art/lib/api/handlers"
	"github.com/icco/art/lib/config"
	gutillog "github.com/icco/gutil/logging"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// defaultRateLimitRPM is used when the configured limit is unset or invalid.
const defaultRateLimitRPM = 120

// Deps bundles the dependencies needed to construct the API router.
type Deps struct {
	Cfg *config.Config
	DB  *gorm.DB
	H   *handlers.Handlers
	Log *zap.SugaredLogger
}

// NewRouter returns the HTTP handler that serves the art API.
func NewRouter(d Deps) http.Handler {
	rpm := d.Cfg.RateLimitRPM
	if rpm <= 0 {
		rpm = defaultRateLimitRPM
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(gutillog.Middleware(d.Log.Desugar()))
	r.Use(middleware.Recoverer)
	r.Use(secureHeaders)
	// Rate limit by the real client IP. We deliberately key on the rightmost
	// X-Forwarded-For hop (the one Caddy appends) rather than chi's RealIP,
	// which trusts the attacker-controlled leftmost entry. See clientIPKey.
	r.Use(httprate.Limit(rpm, time.Minute, httprate.WithKeyFuncs(clientIPKey)))
	r.Use(middleware.Timeout(60 * time.Second))

	// Public, unauthenticated endpoints. Health is harmless and needed by the
	// reverse proxy / uptime checks; the OAuth callback is where Google
	// redirects; /metrics is scraped by Prometheus and is expected to be
	// restricted at the reverse-proxy edge rather than by app auth.
	r.Get("/", handlers.Health)
	r.Get("/healthz", handlers.Health)
	r.Get("/oauth/callback", d.H.OAuthCallback)
	r.Handle("/metrics", promhttp.Handler())

	r.Group(func(r chi.Router) {
		r.Use(OIDCMiddleware(d.Cfg))

		r.Post("/oauth/start", d.H.OAuthStart)
		r.Route("/projects", func(r chi.Router) {
			r.Get("/", d.H.ProjectsList)
			r.Post("/", d.H.ProjectsCreate)
			r.Patch("/{id}", d.H.ProjectsUpdate)
			r.Delete("/{id}", d.H.ProjectsDelete)
		})
		r.Route("/habits", func(r chi.Router) {
			r.Get("/", d.H.HabitsList)
			r.Post("/", d.H.HabitsCreate)
			r.Patch("/{id}", d.H.HabitsUpdate)
			r.Delete("/{id}", d.H.HabitsDelete)
		})
		r.Get("/working-hours", d.H.WorkingHoursList)
		r.Put("/working-hours", d.H.WorkingHoursReplace)
		r.Get("/events", d.H.EventsList)
		r.Get("/sessions", d.H.SessionsList)
		r.Get("/emails", d.H.EmailsList)
		r.Post("/sync", d.H.SyncRun)
		r.Post("/replan", d.H.ReplanRun)
		r.Post("/triage/run", d.H.TriageRun)
	})

	return r
}

// secureHeaders sets conservative response headers for an API with no browser
// clients. CORS is intentionally absent. HSTS is only sent over HTTPS so the
// plaintext localhost dev server doesn't pin a browser to https://localhost.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		// The only HTML response is the OAuth callback page, which uses an
		// inline style attribute; allow inline styles but nothing else.
		h.Set("Content-Security-Policy",
			"default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'")
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// clientIPKey derives a rate-limit key from the true client IP. Behind a single
// trusted proxy (Caddy) that appends the connecting peer, the trustworthy value
// is the rightmost X-Forwarded-For entry; attacker-supplied entries land to its
// left and are ignored. Falls back to RemoteAddr when no header is present.
func clientIPKey(r *http.Request) (string, error) {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
			return canonicalIP(ip), nil
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return canonicalIP(host), nil
}

// canonicalIP normalizes an address to its canonical IP form, collapsing
// equivalent representations so they share one rate-limit bucket.
func canonicalIP(s string) string {
	if ip := net.ParseIP(s); ip != nil {
		return ip.String()
	}
	return s
}
