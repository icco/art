package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/icco/art/lib/config"
	"github.com/icco/art/lib/logging"
	gutilrender "github.com/icco/gutil/render"
	"gorm.io/gorm"
)

// maxBodyBytes caps every JSON request body. Our largest payload (the
// working-hours replacement) is well under 64 KiB.
const maxBodyBytes = 64 * 1024

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

// writeServerError logs the underlying error and returns a generic message
// so DB internals (column names, constraint identifiers) don't leak.
func writeServerError(w http.ResponseWriter, r *http.Request, op string, err error) {
	logging.From(r.Context()).Errorw(op, "err", err)
	writeError(w, r, http.StatusInternalServerError, "internal error")
}

// decodeJSON parses the request body with a size cap and rejects unknown
// fields so typos surface as 400s instead of silent absorption.
func decodeJSON(r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(dst)
}

const (
	defaultPageLimit = 100
	maxPageLimit     = 500
)

// parsePagination reads ?limit and ?offset, applying defaults and a cap.
// Returns ok=false (and writes a 400) if either parses but is invalid.
func parsePagination(w http.ResponseWriter, r *http.Request) (limit, offset int, ok bool) {
	limit = defaultPageLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeError(w, r, http.StatusBadRequest, "limit must be a positive integer")
			return 0, 0, false
		}
		if n > maxPageLimit {
			n = maxPageLimit
		}
		limit = n
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeError(w, r, http.StatusBadRequest, "offset must be a non-negative integer")
			return 0, 0, false
		}
		offset = n
	}
	return limit, offset, true
}
