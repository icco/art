package handlers

import "net/http"

// SyncRun runs all configured upstream syncs and reports their results.
func (h *Handlers) SyncRun(w http.ResponseWriter, r *http.Request) {
	results, err := h.Sync.RunAll(r.Context())
	if err != nil {
		writeServerError(w, r, "sync run", err)
		return
	}
	body := map[string]any{"ok": true}
	if len(results) > 0 {
		body["errors"] = results
		body["ok"] = false
	}
	writeJSON(w, r, http.StatusOK, body)
}
