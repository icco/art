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
	r.Use(httprate.Limit(rpm, time.Minute, httprate.WithKeyFuncs(clientIPKey)))
	r.Use(middleware.Timeout(60 * time.Second))

	// Public: health, the Google OAuth redirect, and Prometheus scrape.
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
		r.Post("/emails/{id}/reverse", d.H.EmailReverse)
		r.Get("/agent-runs", d.H.AgentRunsList)
		r.Post("/sync", d.H.SyncRun)
		r.Post("/replan", d.H.ReplanRun)
		r.Post("/triage/run", d.H.TriageRun)
	})

	return r
}

// secureHeaders sets API security headers. No CORS (no browser clients); HSTS
// only over HTTPS. CSP allows the inline style on the OAuth callback page.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy",
			"default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'")
		if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
			h.Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// clientIPKey keys the rate limiter on the rightmost X-Forwarded-For hop (the
// one the trusted proxy appends), not the spoofable leftmost entry.
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

func canonicalIP(s string) string {
	if ip := net.ParseIP(s); ip != nil {
		return ip.String()
	}
	return s
}
