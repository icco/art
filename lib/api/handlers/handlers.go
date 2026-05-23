package handlers

import (
	"context"
	"net/http"

	"github.com/icco/art/lib/config"
	"github.com/icco/art/lib/logging"
	gutilrender "github.com/icco/gutil/render"
	"gorm.io/gorm"
)

// Handlers bundles every HTTP handler with shared dependencies.
type Handlers struct {
	Cfg     *config.Config
	DB      *gorm.DB
	OAuth   OAuthService
	Sync    SyncService
	Planner PlannerService
}

// OAuthService is the surface used by the OAuth handlers, decoupled from the
// concrete oauth package so handlers don't import it directly.
type OAuthService interface {
	StartURL(account string) (string, error)
	Complete(ctx context.Context, state, code string) (account, email string, err error)
}

// SyncService kicks off a sync across every linked account.
type SyncService interface {
	RunAll(ctx context.Context) (perAccountErrors map[string]string, err error)
}

// PlannerService runs one planning cycle.
type PlannerService interface {
	Run(ctx context.Context) error
}

// writeJSON delegates to icco/gutil/render so encode errors are logged
// through the same logger the rest of the server uses.
func writeJSON(w http.ResponseWriter, r *http.Request, status int, body any) {
	gutilrender.JSON(logging.From(r.Context()), w, status, body)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	writeJSON(w, r, status, map[string]string{"error": msg})
}
