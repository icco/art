package handlers

import (
	"net/http"

	"github.com/icco/art/lib/models"
)

func (h *Handlers) ReplanRun(w http.ResponseWriter, r *http.Request) {
	if err := h.Planner.Run(r.Context()); err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	var run models.AgentRun
	if err := h.DB.WithContext(r.Context()).
		Order("started_at DESC").
		First(&run).Error; err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, r, http.StatusOK, run)
}
