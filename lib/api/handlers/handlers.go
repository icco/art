package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/icco/art/lib/config"
	"github.com/icco/art/lib/models"
	gutillog "github.com/icco/gutil/logging"
	gutilrender "github.com/icco/gutil/render"
	"gorm.io/gorm"
)

// The working-hours replacement is the largest payload and fits in <8 KiB.
const maxBodyBytes = 64 * 1024

// Handlers wires HTTP handlers to their service dependencies.
type Handlers struct {
	Cfg    *config.Config
	DB     *gorm.DB
	OAuth  OAuthService
	Jobs   JobsService
	Triage TriageService
}

// OAuthService decouples handlers from the concrete oauth package and lets
// tests pass a fake implementation. JobsService and TriageService do the
// same for the job queue and email actions respectively.
type (
	OAuthService interface {
		StartURL(account string) (string, error)
		Complete(ctx context.Context, state, code string) (account, email string, err error)
	}
	// JobsService enqueues background jobs; satisfied by *queue.Worker.
	JobsService interface {
		Enqueue(ctx context.Context, kind models.JobKind) (job models.Job, running bool, err error)
	}
	// TriageService applies email actions; the triage pass itself runs as a job.
	TriageService interface {
		Reverse(ctx context.Context, id string) (models.EmailMessage, error)
		SetArchived(ctx context.Context, id string, archived bool) (models.EmailMessage, error)
	}
)

func writeJSON(w http.ResponseWriter, r *http.Request, status int, body any) {
	gutilrender.JSON(gutillog.FromContext(r.Context()), w, status, body)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	writeJSON(w, r, status, map[string]string{"error": msg})
}

// writeServerError keeps DB column names and constraint identifiers out of
// the response while still recording them server-side.
func writeServerError(w http.ResponseWriter, r *http.Request, op string, err error) {
	gutillog.FromContext(r.Context()).Errorw(op, "err", err)
	writeError(w, r, http.StatusInternalServerError, "internal error")
}

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
