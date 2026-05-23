package handlers

import (
	"context"
	"net/http"

	"github.com/icco/art/lib/config"
	"github.com/icco/art/lib/logging"
	gutilrender "github.com/icco/gutil/render"
	"gorm.io/gorm"
)

type Handlers struct {
	Cfg     *config.Config
	DB      *gorm.DB
	OAuth   OAuthService
	Sync    SyncService
	Planner PlannerService
}

// Service interfaces decouple handlers from concrete oauth/calendar/agent packages.
type (
	OAuthService interface {
		StartURL(account string) (string, error)
		Complete(ctx context.Context, state, code string) (account, email string, err error)
	}
	SyncService interface {
		RunAll(ctx context.Context) (perAccountErrors map[string]string, err error)
	}
	PlannerService interface {
		Run(ctx context.Context) error
	}
)

func writeJSON(w http.ResponseWriter, r *http.Request, status int, body any) {
	gutilrender.JSON(logging.From(r.Context()), w, status, body)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	writeJSON(w, r, status, map[string]string{"error": msg})
}
