package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/icco/art/lib/models"
	"gorm.io/gorm"
)

// SessionsList responds with the planner sessions in the requested window.
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

// SessionsDelete retracts a planned session: it deletes the backing
// Art-managed calendar event, then the row. Deleting a session frees its
// habit or project to be re-planned onto a different slot.
func (h *Handlers) SessionsDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var sess models.Session
	err := h.DB.WithContext(r.Context()).First(&sess, "id = ?", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		writeError(w, r, http.StatusNotFound, "session not found")
		return
	}
	if err != nil {
		writeServerError(w, r, "sessions delete lookup", err)
		return
	}
	// Remove the calendar event first: if that fails the row stays, so the
	// session and calendar don't drift apart.
	if sess.GoogleEventID != nil && *sess.GoogleEventID != "" {
		if err := h.Calendar.DeleteManaged(r.Context(), sess.AccountKind, sess.CalendarID, *sess.GoogleEventID); err != nil {
			writeServerError(w, r, "sessions delete calendar", err)
			return
		}
	}
	if err := h.DB.WithContext(r.Context()).Delete(&models.Session{}, "id = ?", sess.ID).Error; err != nil {
		writeServerError(w, r, "sessions delete", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
