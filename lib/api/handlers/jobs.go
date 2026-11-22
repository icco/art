package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.icco.me/art/lib/models"
	"gorm.io/gorm"
)

// TriageRun enqueues an email-triage pass; clients poll /agent-runs or /jobs/{id}.
func (h *Handlers) TriageRun(w http.ResponseWriter, r *http.Request) {
	h.enqueueJob(w, r, models.JobTriage)
}

func (h *Handlers) enqueueJob(w http.ResponseWriter, r *http.Request, kind models.JobKind) {
	job, running, err := h.Jobs.Enqueue(r.Context(), kind)
	if err != nil {
		writeServerError(w, r, "enqueue "+string(kind), err)
		return
	}
	status := "queued"
	if running {
		status = "running"
	}
	writeJSON(w, r, http.StatusAccepted, map[string]any{"status": status, "job": job})
}

// JobsList responds with jobs by recent activity, with optional kind and
// status filters plus standard pagination.
func (h *Handlers) JobsList(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := parsePagination(w, r)
	if !ok {
		return
	}
	q := h.DB.WithContext(r.Context())
	if kind := r.URL.Query().Get("kind"); kind != "" {
		if !models.JobKind(kind).Valid() {
			writeError(w, r, http.StatusBadRequest, "kind must be triage")
			return
		}
		q = q.Where("kind = ?", kind)
	}
	if status := r.URL.Query().Get("status"); status != "" {
		if !models.JobStatus(status).Valid() {
			writeError(w, r, http.StatusBadRequest, "status must be one of pending, running, succeeded, failed")
			return
		}
		q = q.Where("status = ?", status)
	}
	var out []models.Job
	if err := q.Order("updated_at DESC").Limit(limit).Offset(offset).Find(&out).Error; err != nil {
		writeServerError(w, r, "jobs list", err)
		return
	}
	writeJSON(w, r, http.StatusOK, out)
}

// JobsGet responds with a single job by id.
func (h *Handlers) JobsGet(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := uuid.Parse(id); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid id")
		return
	}
	var job models.Job
	if err := h.DB.WithContext(r.Context()).First(&job, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, r, http.StatusNotFound, "job not found")
			return
		}
		writeServerError(w, r, "job get", err)
		return
	}
	writeJSON(w, r, http.StatusOK, job)
}
