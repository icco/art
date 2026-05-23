package handlers

import "net/http"

// Stubs filled in by later commits. 501 makes them visible as
// "not implemented yet" rather than 404.

func (h *Handlers) ProjectsList(w http.ResponseWriter, r *http.Request)        { notImpl(w) }
func (h *Handlers) ProjectsCreate(w http.ResponseWriter, r *http.Request)      { notImpl(w) }
func (h *Handlers) ProjectsUpdate(w http.ResponseWriter, r *http.Request)      { notImpl(w) }
func (h *Handlers) ProjectsDelete(w http.ResponseWriter, r *http.Request)      { notImpl(w) }
func (h *Handlers) HabitsList(w http.ResponseWriter, r *http.Request)          { notImpl(w) }
func (h *Handlers) HabitsCreate(w http.ResponseWriter, r *http.Request)        { notImpl(w) }
func (h *Handlers) HabitsUpdate(w http.ResponseWriter, r *http.Request)        { notImpl(w) }
func (h *Handlers) HabitsDelete(w http.ResponseWriter, r *http.Request)        { notImpl(w) }
func (h *Handlers) WorkingHoursList(w http.ResponseWriter, r *http.Request)    { notImpl(w) }
func (h *Handlers) WorkingHoursReplace(w http.ResponseWriter, r *http.Request) { notImpl(w) }
func (h *Handlers) EventsList(w http.ResponseWriter, r *http.Request)          { notImpl(w) }
func (h *Handlers) SessionsList(w http.ResponseWriter, r *http.Request)        { notImpl(w) }
func (h *Handlers) ReplanRun(w http.ResponseWriter, r *http.Request)           { notImpl(w) }

func notImpl(w http.ResponseWriter) {
	writeError(w, http.StatusNotImplemented, "not implemented yet")
}
