package handlers

import (
	"net/http"
	"time"

	"github.com/icco/art/lib/models"
)

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
