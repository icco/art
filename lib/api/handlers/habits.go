package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/icco/art/lib/models"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

type habitReq struct {
	Name                 string          `json:"name"`
	Description          string          `json:"description"`
	Kind                 string          `json:"kind"`
	BlockDurationMinutes int             `json:"block_duration_minutes"`
	Cadence              *models.Cadence `json:"cadence,omitempty"`
	Active               *bool           `json:"active,omitempty"`
}

func (req habitReq) validate(create bool) error {
	if create && req.Name == "" {
		return errors.New("name required")
	}
	if req.Kind != "" && !models.SlotKind(req.Kind).Valid() {
		return errors.New("kind must be 'work' or 'personal'")
	}
	if create && req.BlockDurationMinutes <= 0 {
		return errors.New("block_duration_minutes must be > 0")
	}
	if create && (req.Cadence == nil || req.Cadence.Count <= 0 || req.Cadence.Type == "") {
		return errors.New("cadence with type and positive count required")
	}
	return nil
}

func (h *Handlers) HabitsList(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := parsePagination(w, r)
	if !ok {
		return
	}
	var out []models.Habit
	if err := h.DB.WithContext(r.Context()).
		Order("created_at DESC").Limit(limit).Offset(offset).
		Find(&out).Error; err != nil {
		writeServerError(w, r, "habits list", err)
		return
	}
	writeJSON(w, r, http.StatusOK, out)
}

func (h *Handlers) HabitsCreate(w http.ResponseWriter, r *http.Request) {
	var req habitReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if req.Kind == "" {
		req.Kind = string(models.SlotPersonal)
	}
	if req.Active == nil {
		t := true
		req.Active = &t
	}
	if err := req.validate(true); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	cad, err := json.Marshal(req.Cadence)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	hb := models.Habit{
		Name:                 req.Name,
		Description:          req.Description,
		Kind:                 models.SlotKind(req.Kind),
		BlockDurationMinutes: req.BlockDurationMinutes,
		Cadence:              datatypes.JSON(cad),
		Active:               *req.Active,
	}
	if err := h.DB.WithContext(r.Context()).Create(&hb).Error; err != nil {
		writeServerError(w, r, "habits create", err)
		return
	}
	writeJSON(w, r, http.StatusCreated, hb)
}

func (h *Handlers) HabitsUpdate(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req habitReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	if err := req.validate(false); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	var hb models.Habit
	if err := h.DB.WithContext(r.Context()).First(&hb, "id = ?", id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, r, http.StatusNotFound, "habit not found")
			return
		}
		writeServerError(w, r, "habits update lookup", err)
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
	if req.BlockDurationMinutes > 0 {
		updates["block_duration_minutes"] = req.BlockDurationMinutes
	}
	if req.Cadence != nil {
		cad, err := json.Marshal(req.Cadence)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, err.Error())
			return
		}
		updates["cadence"] = datatypes.JSON(cad)
	}
	if req.Active != nil {
		updates["active"] = *req.Active
	}
	if len(updates) > 0 {
		if err := h.DB.WithContext(r.Context()).Model(&hb).Updates(updates).Error; err != nil {
			writeServerError(w, r, "habits update", err)
			return
		}
	}
	writeJSON(w, r, http.StatusOK, hb)
}

func (h *Handlers) HabitsDelete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	res := h.DB.WithContext(r.Context()).Delete(&models.Habit{}, "id = ?", id)
	if res.Error != nil {
		writeServerError(w, r, "habits delete", res.Error)
		return
	}
	if res.RowsAffected == 0 {
		writeError(w, r, http.StatusNotFound, "habit not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
