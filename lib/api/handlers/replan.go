package handlers

import (
	"net/http"

	"github.com/icco/art/lib/models"
)

// ReplanRun reconciles calendar drift, triggers a planner run, and returns
// the resulting AgentRun row.
func (h *Handlers) ReplanRun(w http.ResponseWriter, r *http.Request) {
	if _, err := h.Planner.Reconcile(r.Context()); err != nil {
		writeServerError(w, r, "reconcile", err)
		return
	}
	if err := h.Planner.Run(r.Context()); err != nil {
		writeServerError(w, r, "planner run", err)
		return
	}
	var run models.AgentRun
	if err := h.DB.WithContext(r.Context()).
		Order("started_at DESC").
		First(&run).Error; err != nil {
		writeServerError(w, r, "agent_run lookup", err)
		return
	}
	writeJSON(w, r, http.StatusOK, run)
}
