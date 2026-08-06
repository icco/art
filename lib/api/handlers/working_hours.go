package handlers

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/go-chi/chi/v5"
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
		return errSlotKindInvalid
	}
	if req.DayOfWeek < 0 || req.DayOfWeek > 6 {
		return errDayOfWeek
	}
	if req.StartMinute < 0 || req.StartMinute >= 1440 {
		return errStartMinute
	}
	if req.EndMinute <= req.StartMinute || req.EndMinute > 1440 {
		return errEndMinute
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

// dayWindowReq is one window in a per-day patch; kind and day come from the path.
type dayWindowReq struct {
	StartMinute int `json:"start_minute"`
	EndMinute   int `json:"end_minute"`
}

// WorkingHoursPatchDay replaces the windows for one slot kind and weekday,
// leaving the rest of the table alone. An empty list clears the day.
func (h *Handlers) WorkingHoursPatchDay(w http.ResponseWriter, r *http.Request) {
	kind := models.SlotKind(chi.URLParam(r, "kind"))
	if !kind.Valid() {
		writeError(w, r, http.StatusBadRequest, "kind must be 'work' or 'personal'")
		return
	}
	day, err := strconv.Atoi(chi.URLParam(r, "day"))
	if err != nil || day < 0 || day > 6 {
		writeError(w, r, http.StatusBadRequest, "day must be 0-6")
		return
	}
	var windows []dayWindowReq
	if err := decodeJSON(r, &windows); err != nil {
		writeError(w, r, http.StatusBadRequest, err.Error())
		return
	}
	reqs := make([]workingHourReq, len(windows))
	for i, win := range windows {
		reqs[i] = workingHourReq{
			SlotKind: string(kind), DayOfWeek: day,
			StartMinute: win.StartMinute, EndMinute: win.EndMinute,
		}
		if err := reqs[i].validate(); err != nil {
			writeError(w, r, http.StatusBadRequest, fmt.Sprintf("window %d: %v", i, err))
			return
		}
	}

	// Overlap is judged against the table the patch would produce, so read the
	// untouched rows in the same transaction that writes.
	var badReq error
	err = h.DB.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var others []models.WorkingHour
		if err := tx.Where("NOT (slot_kind = ? AND day_of_week = ?)", kind, day).Find(&others).Error; err != nil {
			return err
		}
		all := make([]workingHourReq, 0, len(others)+len(reqs))
		for _, o := range others {
			all = append(all, workingHourReq{
				SlotKind: string(o.SlotKind), DayOfWeek: o.DayOfWeek,
				StartMinute: o.StartMinute, EndMinute: o.EndMinute,
			})
		}
		all = append(all, reqs...)
		if err := validateNoOverlap(all); err != nil {
			badReq = err
			return err
		}

		if err := tx.Where("slot_kind = ? AND day_of_week = ?", kind, day).
			Delete(&models.WorkingHour{}).Error; err != nil {
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
	if badReq != nil {
		writeError(w, r, http.StatusBadRequest, badReq.Error())
		return
	}
	if err != nil {
		writeServerError(w, r, "working_hours patch day", err)
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
				return fmt.Errorf("%w for %s day %d: [%d-%d] and [%d-%d]",
					errOverlapping,
					b.slot, b.day,
					rs[i-1].StartMinute, rs[i-1].EndMinute,
					rs[i].StartMinute, rs[i].EndMinute)
			}
		}
	}
	return nil
}
