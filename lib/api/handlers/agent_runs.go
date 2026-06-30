package handlers

import (
	"net/http"

	"github.com/icco/art/lib/models"
)

// AgentRunsList responds with recent agent runs, newest first. Supports an
// optional kind filter (planner|triage) plus standard pagination.
func (h *Handlers) AgentRunsList(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := parsePagination(w, r)
	if !ok {
		return
	}
	q := h.DB.WithContext(r.Context())
	if kind := r.URL.Query().Get("kind"); kind != "" {
		if !models.AgentRunKind(kind).Valid() {
			writeError(w, r, http.StatusBadRequest, "kind must be 'planner' or 'triage'")
			return
		}
		q = q.Where("kind = ?", kind)
	}
	var out []models.AgentRun
	if err := q.Order("started_at DESC").Limit(limit).Offset(offset).Find(&out).Error; err != nil {
		writeServerError(w, r, "agent runs list", err)
		return
	}
	writeJSON(w, r, http.StatusOK, out)
}
