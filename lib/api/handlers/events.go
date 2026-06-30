// Package handlers implements the HTTP handlers exposed by the art API.
package handlers

import (
	"net/http"
	"time"

	"github.com/icco/art/lib/models"
)

// EventsList responds with calendar events in the requested time window.
func (h *Handlers) EventsList(w http.ResponseWriter, r *http.Request) {
	from, to, ok := parseWindow(w, r)
	if !ok {
		return
	}
	q := h.DB.WithContext(r.Context()).
		Where("start_time >= ? AND start_time < ?", from, to)
	if kind := r.URL.Query().Get("kind"); kind != "" {
		if !models.AccountKind(kind).Valid() {
			writeError(w, r, http.StatusBadRequest, "kind must be 'personal' or 'work'")
			return
		}
		q = q.Where("account_kind = ?", kind)
	}
	if r.URL.Query().Get("calendar") == "primary" {
		// Keep only events on each account's primary calendar. The row-value
		// subquery returns no rows when no account has a primary set, so the
		// filter yields an empty list rather than silently matching everything.
		primaries := h.DB.Model(&models.Account{}).
			Select("kind", "primary_calendar_id").
			Where("primary_calendar_id <> ''")
		q = q.Where("(account_kind, calendar_id) IN (?)", primaries)
	}
	var out []models.Event
	if err := q.Order("start_time ASC").Limit(2000).Find(&out).Error; err != nil {
		writeServerError(w, r, "events list", err)
		return
	}
	writeJSON(w, r, http.StatusOK, out)
}

func parseWindow(w http.ResponseWriter, r *http.Request) (time.Time, time.Time, bool) {
	q := r.URL.Query()
	now := time.Now().UTC()
	from := now.Add(-7 * 24 * time.Hour)
	to := now.Add(30 * 24 * time.Hour)
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "from must be RFC3339")
			return time.Time{}, time.Time{}, false
		}
		from = t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "to must be RFC3339")
			return time.Time{}, time.Time{}, false
		}
		to = t
	}
	return from, to, true
}
