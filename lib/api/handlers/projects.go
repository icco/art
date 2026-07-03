package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/icco/art/lib/models"
	"gorm.io/gorm"
)

// Pointer fields distinguish "absent" from "clear" on updates; raw Deadline
// lets an explicit null clear it.
type projectReq struct {
	Name        *string         `json:"name"`
	Description *string         `json:"description"`
	Kind        string          `json:"kind"`
	TargetHours *float64        `json:"target_hours"`
	Deadline    json.RawMessage `json:"deadline,omitempty"`
	Status      string          `json:"status,omitempty"`
}

func (p projectReq) validate(create bool) error {
	if create && (p.Name == nil || *p.Name == "") {
		return errors.New("name required")
	}
	if p.Name != nil && *p.Name == "" {
		return errors.New("name cannot be empty")
	}
	if p.Kind != "" && !models.SlotKind(p.Kind).Valid() {
		return errors.New("kind must be 'work' or 'personal'")
	}
	if create && p.TargetHours == nil {
		return errors.New("target_hours must be > 0")
	}
	if p.TargetHours != nil && *p.TargetHours <= 0 {
		return errors.New("target_hours must be > 0")
	}
	if p.Status != "" {
		switch models.ProjectStatus(p.Status) {
		case models.ProjectActive, models.ProjectPaused, models.ProjectDone:
		default:
			return errors.New("status must be one of active|paused|done")
		}
	}
	return nil
}

// deadlineValue decodes the raw deadline: absent → set=false; null → clear.
func (p projectReq) deadlineValue() (*time.Time, bool, error) {
	if len(p.Deadline) == 0 {
		return nil, false, nil
	}
	if string(p.Deadline) == "null" {
		return nil, true, nil
	}
	var t time.Time
	if err := json.Unmarshal(p.Deadline, &t); err != nil {
		return nil, false, errors.New("deadline must be an RFC3339 timestamp or null")
	}
	return &t, true, nil
}

// ProjectsList responds with a paginated list of projects.
func (h *Handlers) ProjectsList(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := parsePagination(w, r)
	if !ok {
		return
	}
	var out []models.Project
	if err := h.DB.WithContext(r.Context()).
		Order("created_at DESC").Limit(limit).Offset(offset).
		Find(&out).Error; err != nil {
		writeServerError(w, r, "projects list", err)
		return
	}
	writeJSON(w, r, http.StatusOK, out)
}

// ProjectsCreate creates a new project from the request body.
func (h *Handlers) ProjectsCreate(w http.ResponseWriter, r *http.Request) {
	var req projectReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if req.Kind == "" {
		req.Kind = string(models.SlotWork)
	}
	if req.Status == "" {
		req.Status = string(models.ProjectActive)
	}
	if err := req.validate(true); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	deadline, _, err := req.deadlineValue()
	if err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	p := models.Project{
		Name:        *req.Name,
		Kind:        models.SlotKind(req.Kind),
		TargetHours: *req.TargetHours,
		Deadline:    deadline,
		Status:      models.ProjectStatus(req.Status),
	}
	if req.Description != nil {
		p.Description = *req.Description
	}
	if err := h.DB.WithContext(r.Context()).Create(&p).Error; err != nil {
		writeServerError(w, r, "projects create", err)
		return
	}
	writeJSON(w, r, http.StatusCreated, p)
}

// ProjectsUpdate applies partial updates to the project identified by the URL.
func (h *Handlers) ProjectsUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req projectReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if err := req.validate(false); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	var p models.Project
	if err := h.DB.WithContext(r.Context()).First(&p, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, r, http.StatusNotFound, "project not found")
			return
		}
		writeServerError(w, r, "projects update lookup", err)
		return
	}
	deadline, deadlineSet, err := req.deadlineValue()
	if err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	updates := map[string]any{}
	if req.Name != nil {
		updates["name"] = *req.Name
	}
	if req.Description != nil {
		updates["description"] = *req.Description
	}
	if req.Kind != "" {
		updates["kind"] = req.Kind
	}
	if req.TargetHours != nil {
		updates["target_hours"] = *req.TargetHours
	}
	if deadlineSet {
		updates["deadline"] = deadline
	}
	if req.Status != "" {
		updates["status"] = req.Status
	}
	if len(updates) > 0 {
		if err := h.DB.WithContext(r.Context()).Model(&p).Updates(updates).Error; err != nil {
			writeServerError(w, r, "projects update", err)
			return
		}
	}
	writeJSON(w, r, http.StatusOK, p)
}

// ProjectsDelete deletes the project identified by the URL.
func (h *Handlers) ProjectsDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	res := h.DB.WithContext(r.Context()).Delete(&models.Project{}, "id = ?", id)
	if res.Error != nil {
		writeServerError(w, r, "projects delete", res.Error)
		return
	}
	if res.RowsAffected == 0 {
		writeError(w, r, http.StatusNotFound, "project not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
