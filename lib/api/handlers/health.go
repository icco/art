package handlers

import "net/http"

// Health responds with a small JSON OK body for liveness/readiness probes.
func Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusOK, map[string]string{"status": "ok"})
}
