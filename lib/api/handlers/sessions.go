package handlers

import (
	"net/http"

	"github.com/icco/art/lib/models"
)

func (h *Handlers) SessionsList(w http.ResponseWriter, r *http.Request) {
	from, to, ok := parseWindow(w, r)
	if !ok {
		return
	}
	var out []models.Session
	if err := h.DB.WithContext(r.Context()).
		Where("scheduled_start >= ? AND scheduled_start < ?", from, to).
		Order("scheduled_start ASC").
		Find(&out).Error; err != nil {
		writeServerError(w, r, "sessions list", err)
		return
	}
	writeJSON(w, r, http.StatusOK, out)
}
