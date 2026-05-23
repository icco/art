package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/icco/art/lib/config"
	"gorm.io/gorm"
)

// Handlers bundles every HTTP handler with shared dependencies. Phase-1
// commits leave Planner/OAuth/Sync nil so the scaffold can build before they
// land in subsequent commits; the corresponding routes return 501 until then.
type Handlers struct {
	Cfg     *config.Config
	DB      *gorm.DB
	OAuth   OAuthService   // set by main once the OAuth flow is wired
	Sync    SyncService    // set by main once calendar sync is wired
	Planner PlannerService // set by main once the planner is wired
}

// Service interfaces decouple the handler package from concrete implementations
// added in later commits.

type OAuthService interface {
	StartURL(account string) (string, error)
	Complete(ctx interface{ Done() <-chan struct{} }, state, code string) (account string, email string, err error)
}

type SyncService interface {
	RunAll(ctx interface{ Done() <-chan struct{} }) (perAccount map[string]string, err error)
}

type PlannerService interface {
	Run(ctx interface{ Done() <-chan struct{} }) error
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
