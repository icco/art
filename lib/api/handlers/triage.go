package handlers

import (
	"net/http"

	"github.com/icco/art/lib/models"
)

// TriageRun runs an email-triage pass across all linked accounts.
func (h *Handlers) TriageRun(w http.ResponseWriter, r *http.Request) {
	if err := h.Triage.RunAll(r.Context()); err != nil {
		writeServerError(w, r, "triage run", err)
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true})
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
			writeError(w, r, http.StatusBadRequest, "category must be one of archive, reply, read, thinking, keep")
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
