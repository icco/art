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

type projectReq struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Kind        string     `json:"kind"`
	TargetHours float64    `json:"target_hours"`
	Deadline    *time.Time `json:"deadline,omitempty"`
	Status      string     `json:"status,omitempty"`
}

func (p projectReq) validate(create bool) error {
	if create && p.Name == "" {
		return errors.New("name required")
	}
	if p.Kind != "" && !models.SlotKind(p.Kind).Valid() {
		return errors.New("kind must be 'work' or 'personal'")
	}
	if create && p.TargetHours <= 0 {
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

func (h *Handlers) ProjectsList(w http.ResponseWriter, r *http.Request) {
	var out []models.Project
	if err := h.DB.WithContext(r.Context()).Order("created_at DESC").Find(&out).Error; err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, r, http.StatusOK, out)
}

func (h *Handlers) ProjectsCreate(w http.ResponseWriter, r *http.Request) {
	var req projectReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
	p := models.Project{
		Name:        req.Name,
		Description: req.Description,
		Kind:        models.SlotKind(req.Kind),
		TargetHours: req.TargetHours,
		Deadline:    req.Deadline,
		Status:      models.ProjectStatus(req.Status),
	}
	if err := h.DB.WithContext(r.Context()).Create(&p).Error; err != nil {
		writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, r, http.StatusCreated, p)
}

func (h *Handlers) ProjectsUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req projectReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
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
		writeError(w, r, http.StatusInternalServerError, err.Error())
		return
	}
	updates := map[string]any{}
	if req.Name != "" {
		updates["name"] = req.Name
	}
	if req.Description != "" {
		updates["description"] = req.Description
	}
	if req.Kind != "" {
		updates["kind"] = req.Kind
	}
	if req.TargetHours > 0 {
		updates["target_hours"] = req.TargetHours
	}
	if req.Deadline != nil {
		updates["deadline"] = req.Deadline
	}
	if req.Status != "" {
		updates["status"] = req.Status
	}
	if len(updates) > 0 {
		if err := h.DB.WithContext(r.Context()).Model(&p).Updates(updates).Error; err != nil {
			writeError(w, r, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, r, http.StatusOK, p)
}

func (h *Handlers) ProjectsDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	res := h.DB.WithContext(r.Context()).Delete(&models.Project{}, "id = ?", id)
	if res.Error != nil {
		writeError(w, r, http.StatusInternalServerError, res.Error.Error())
		return
	}
	if res.RowsAffected == 0 {
		writeError(w, r, http.StatusNotFound, "project not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
