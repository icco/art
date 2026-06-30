package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/icco/art/lib/models"
	gutillog "github.com/icco/gutil/logging"
	"gorm.io/gorm"
)

// triageRunTimeout bounds a detached triage pass and doubles as the staleness
// window for the in-flight guard: a run still "running" past this is treated as
// abandoned (e.g. the process restarted mid-pass) so it can't block new runs.
const triageRunTimeout = 10 * time.Minute

// TriageRun starts an email-triage pass across all linked accounts and returns
// immediately. The pass can take minutes, so it runs detached from the request:
// a client disconnect or the router timeout no longer aborts it. Progress is
// tracked by the AgentRun row (kind=triage); clients poll /agent-runs to see
// when it lands.
func (h *Handlers) TriageRun(w http.ResponseWriter, r *http.Request) {
	var running int64
	if err := h.DB.WithContext(r.Context()).Model(&models.AgentRun{}).
		Where("kind = ? AND status = ? AND started_at > ?",
			string(models.AgentRunTriage), string(models.AgentRunRunning),
			time.Now().Add(-triageRunTimeout)).
		Count(&running).Error; err != nil {
		writeServerError(w, r, "triage run", err)
		return
	}
	if running > 0 {
		writeJSON(w, r, http.StatusAccepted, map[string]any{"status": "running"})
		return
	}

	// Keep the request's logger/request-id but drop its cancellation so the
	// pass survives the response returning.
	ctx := context.WithoutCancel(r.Context())
	go func() {
		ctx, cancel := context.WithTimeout(ctx, triageRunTimeout)
		defer cancel()
		if err := h.Triage.RunAll(ctx); err != nil {
			gutillog.FromContext(ctx).Errorw("triage run", "err", err)
		}
	}()
	writeJSON(w, r, http.StatusAccepted, map[string]any{"status": "started"})
}

// EmailReverse marks a triaged decision wrong: it undoes the Gmail action and
// records the reversal so the classifier learns. Returns the updated row.
func (h *Handlers) EmailReverse(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	msg, err := h.Triage.Reverse(r.Context(), id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, r, http.StatusNotFound, "email not found")
			return
		}
		writeServerError(w, r, "email reverse", err)
		return
	}
	writeJSON(w, r, http.StatusOK, msg)
}

// EmailSetArchived moves a triaged message between the inbox and the archive
// (Gmail labels only, never writing mail) and records the change for learning.
// Returns the updated row.
func (h *Handlers) EmailSetArchived(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var body struct {
		Archived bool `json:"archived"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeError(w, r, http.StatusBadRequest, `body must be {"archived": bool}`)
		return
	}
	msg, err := h.Triage.SetArchived(r.Context(), id, body.Archived)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, r, http.StatusNotFound, "email not found")
			return
		}
		writeServerError(w, r, "email set archived", err)
		return
	}
	writeJSON(w, r, http.StatusOK, msg)
}

// EmailsList responds with triaged messages, newest first. Supports optional
// account and category filters plus standard pagination.
func (h *Handlers) EmailsList(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := parsePagination(w, r)
	if !ok {
		return
	}
	q := h.DB.WithContext(r.Context())
	if kind := r.URL.Query().Get("account"); kind != "" {
		if !models.AccountKind(kind).Valid() {
			writeError(w, r, http.StatusBadRequest, "account must be 'personal' or 'work'")
			return
		}
		q = q.Where("account_kind = ?", kind)
	}
	if cat := r.URL.Query().Get("category"); cat != "" {
		if !models.EmailCategory(cat).Valid() {
			writeError(w, r, http.StatusBadRequest, "category must be one of archive, reply, keep")
			return
		}
		q = q.Where("category = ?", cat)
	}
	var out []models.EmailMessage
	if err := q.Order("received_at DESC").Limit(limit).Offset(offset).Find(&out).Error; err != nil {
		writeServerError(w, r, "emails list", err)
		return
	}
	writeJSON(w, r, http.StatusOK, out)
}
