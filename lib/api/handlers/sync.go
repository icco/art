package handlers

import "net/http"

func (h *Handlers) SyncRun(w http.ResponseWriter, r *http.Request) {
	results, err := h.Sync.RunAll(r.Context())
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	body := map[string]any{"ok": true}
	if len(results) > 0 {
		body["errors"] = results
		body["ok"] = false
	}
	writeJSON(w, r, http.StatusOK, body)
}
