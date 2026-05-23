package handlers

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/icco/art/lib/config"
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

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
