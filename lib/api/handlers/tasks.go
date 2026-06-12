package handlers

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/quickadd"
	"gorm.io/gorm"
)

type taskReq struct {
	Title           string     `json:"title"`
	Kind            string     `json:"kind"`
	DurationMinutes int        `json:"duration_minutes"`
	Deadline        *time.Time `json:"deadline,omitempty"`
	Status          string     `json:"status,omitempty"`
	Notes           string     `json:"notes"`
}

func (t taskReq) validate(create bool) error {
	if create && t.Title == "" {
		return errors.New("title required")
	}
	if t.Kind != "" && !models.SlotKind(t.Kind).Valid() {
		return errors.New("kind must be 'work' or 'personal'")
	}
	if create && t.DurationMinutes <= 0 {
		return errors.New("duration_minutes must be > 0")
	}
	if t.Status != "" && !models.TaskStatus(t.Status).Valid() {
		return errors.New("status must be one of pending|scheduled|done|unschedulable")
	}
	return nil
}

// TasksList responds with a paginated list of tasks, optionally filtered by
// ?status= (comma-separated).
func (h *Handlers) TasksList(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := parsePagination(w, r)
	if !ok {
		return
	}
	q := h.DB.WithContext(r.Context()).Order("created_at DESC").Limit(limit).Offset(offset)
	if raw := r.URL.Query().Get("status"); raw != "" {
		var statuses []string
		for s := range strings.SplitSeq(raw, ",") {
			s = strings.TrimSpace(s)
			if !models.TaskStatus(s).Valid() {
				writeError(w, r, http.StatusBadRequest, "invalid status filter: "+s)
				return
			}
			statuses = append(statuses, s)
		}
		q = q.Where("status IN ?", statuses)
	}
	var out []models.Task
	if err := q.Find(&out).Error; err != nil {
		writeServerError(w, r, "tasks list", err)
		return
	}
	writeJSON(w, r, http.StatusOK, out)
}

// TasksCreate creates a new task from the request body.
func (h *Handlers) TasksCreate(w http.ResponseWriter, r *http.Request) {
	var req taskReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if req.Kind == "" {
		req.Kind = string(models.SlotPersonal)
	}
	if req.Status == "" {
		req.Status = string(models.TaskPending)
	}
	if err := req.validate(true); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	t := models.Task{
		Title:           req.Title,
		Kind:            models.SlotKind(req.Kind),
		DurationMinutes: req.DurationMinutes,
		Deadline:        req.Deadline,
		Status:          models.TaskStatus(req.Status),
		Notes:           req.Notes,
	}
	if err := h.DB.WithContext(r.Context()).Create(&t).Error; err != nil {
		writeServerError(w, r, "tasks create", err)
		return
	}
	writeJSON(w, r, http.StatusCreated, t)
}

// TasksQuickAdd parses a one-line capture like "pack office 2h by friday"
// into a task. It echoes the created task back so the client can show what
// was understood.
func (h *Handlers) TasksQuickAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Input string `json:"input"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	parsed, err := quickadd.Parse(req.Input, time.Now(), h.Cfg.Timezone)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	t := models.Task{
		Title:           parsed.Title,
		Kind:            parsed.Kind,
		DurationMinutes: parsed.DurationMinutes,
		Deadline:        parsed.Deadline,
		Status:          models.TaskPending,
	}
	if err := h.DB.WithContext(r.Context()).Create(&t).Error; err != nil {
		writeServerError(w, r, "tasks quickadd", err)
		return
	}
	writeJSON(w, r, http.StatusCreated, t)
}

// TasksUpdate applies partial updates to the task identified by the URL.
// Changing the duration or deadline of an unschedulable task resets it to
// pending so the next planner pass retries it.
func (h *Handlers) TasksUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req taskReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if err := req.validate(false); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	var t models.Task
	if err := h.DB.WithContext(r.Context()).First(&t, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, r, http.StatusNotFound, "task not found")
			return
		}
		writeServerError(w, r, "tasks update lookup", err)
		return
	}
	updates := map[string]any{}
	if req.Title != "" {
		updates["title"] = req.Title
	}
	if req.Kind != "" {
		updates["kind"] = req.Kind
	}
	if req.DurationMinutes > 0 {
		updates["duration_minutes"] = req.DurationMinutes
	}
	if req.Deadline != nil {
		updates["deadline"] = req.Deadline
	}
	if req.Notes != "" {
		updates["notes"] = req.Notes
	}
	if req.Status != "" {
		updates["status"] = req.Status
	} else if t.Status == models.TaskUnschedulable &&
		(updates["duration_minutes"] != nil || updates["deadline"] != nil) {
		updates["status"] = string(models.TaskPending)
	}
	if len(updates) > 0 {
		if err := h.DB.WithContext(r.Context()).Model(&t).Updates(updates).Error; err != nil {
			writeServerError(w, r, "tasks update", err)
			return
		}
	}
	writeJSON(w, r, http.StatusOK, t)
}

// TasksDelete deletes the task identified by the URL.
func (h *Handlers) TasksDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	res := h.DB.WithContext(r.Context()).Delete(&models.Task{}, "id = ?", id)
	if res.Error != nil {
		writeServerError(w, r, "tasks delete", res.Error)
		return
	}
	if res.RowsAffected == 0 {
		writeError(w, r, http.StatusNotFound, "task not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
