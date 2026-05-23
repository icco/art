package handlers

import "net/http"

// ReplanRun is filled in alongside the planner in a later commit.
func (h *Handlers) ReplanRun(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "replan not implemented yet")
}
