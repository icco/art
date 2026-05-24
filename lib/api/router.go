package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/icco/art/lib/api/handlers"
	"github.com/icco/art/lib/config"
	"github.com/icco/art/lib/logging"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type Deps struct {
	Cfg *config.Config
	DB  *gorm.DB
	H   *handlers.Handlers
	Log *zap.SugaredLogger
}

func NewRouter(d Deps) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))
	r.Use(injectLogger(d.Log))

	r.Get("/", handlers.Health)
	r.Get("/healthz", handlers.Health)
	r.Handle("/metrics", promhttp.Handler())

	// /oauth/callback must stay public — Google redirects here.
	r.Get("/oauth/callback", d.H.OAuthCallback)

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
		r.Post("/sync", d.H.SyncRun)
		r.Post("/replan", d.H.ReplanRun)
	})

	return r
}

// injectLogger puts the process logger on each request's context so handlers
// (and writeServerError in particular) can log via logging.From.
func injectLogger(log *zap.SugaredLogger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(logging.Inject(r.Context(), log)))
		})
	}
}
