package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"sort"

	"github.com/icco/art/lib/models"
	"gorm.io/gorm"
)

type workingHourReq struct {
	SlotKind    string `json:"slot_kind"`
	DayOfWeek   int    `json:"day_of_week"`
	StartMinute int    `json:"start_minute"`
	EndMinute   int    `json:"end_minute"`
}

func (req workingHourReq) validate() error {
	if !models.SlotKind(req.SlotKind).Valid() {
		return errors.New("slot_kind must be 'work' or 'personal'")
	}
	if req.DayOfWeek < 0 || req.DayOfWeek > 6 {
		return errors.New("day_of_week must be 0-6")
	}
	if req.StartMinute < 0 || req.StartMinute >= 1440 {
		return errors.New("start_minute must be 0-1439")
	}
	if req.EndMinute <= req.StartMinute || req.EndMinute > 1440 {
		return errors.New("end_minute must be > start_minute and <= 1440")
	}
	return nil
}

// WorkingHoursList responds with all configured working-hours windows.
func (h *Handlers) WorkingHoursList(w http.ResponseWriter, r *http.Request) {
	var out []models.WorkingHour
	if err := h.DB.WithContext(r.Context()).
		Order("slot_kind, day_of_week, start_minute").
		Find(&out).Error; err != nil {
		writeServerError(w, r, "working_hours list", err)
		return
	}
	writeJSON(w, r, http.StatusOK, out)
}

// WorkingHoursReplace atomically replaces the entire table.
func (h *Handlers) WorkingHoursReplace(w http.ResponseWriter, r *http.Request) {
	var reqs []workingHourReq
	if err := decodeJSON(r, &reqs); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	for i, req := range reqs {
		if err := req.validate(); err != nil {
			writeError(w, r, http.StatusBadRequest, fmt.Sprintf("row %d: %v", i, err))
			return
		}
	}
	if err := validateNoOverlap(reqs); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}

	err := h.DB.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("1 = 1").Delete(&models.WorkingHour{}).Error; err != nil {
			return err
		}
		for _, req := range reqs {
			wh := models.WorkingHour{
				SlotKind:    models.SlotKind(req.SlotKind),
				DayOfWeek:   req.DayOfWeek,
				StartMinute: req.StartMinute,
				EndMinute:   req.EndMinute,
			}
			if err := tx.Create(&wh).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		writeServerError(w, r, "working_hours replace", err)
		return
	}
	h.WorkingHoursList(w, r)
}

// The unique index only catches identical starts, not overlapping ranges.
func validateNoOverlap(reqs []workingHourReq) error {
	type bucket struct {
		slot string
		day  int
	}
	groups := map[bucket][]workingHourReq{}
	for _, r := range reqs {
		b := bucket{slot: r.SlotKind, day: r.DayOfWeek}
		groups[b] = append(groups[b], r)
	}
	for b, rs := range groups {
		sort.Slice(rs, func(i, j int) bool { return rs[i].StartMinute < rs[j].StartMinute })
		for i := 1; i < len(rs); i++ {
			if rs[i].StartMinute < rs[i-1].EndMinute {
				return fmt.Errorf("overlapping windows for %s day %d: [%d-%d] and [%d-%d]",
					b.slot, b.day,
					rs[i-1].StartMinute, rs[i-1].EndMinute,
					rs[i].StartMinute, rs[i].EndMinute)
			}
		}
	}
	return nil
}
