// Package agent implements the art planning and scheduling agents.
package agent

import (
	"context"
	"sort"
	"time"

	"github.com/icco/art/lib/models"
	"gorm.io/gorm"
)

// Slot is a candidate free interval on a particular account.
type Slot struct {
	AccountKind models.AccountKind
	Start       time.Time
	End         time.Time
}

// FindFreeSlots returns up to maxSlots non-overlapping durationMin-long slots
// inside [windowStart, windowEnd) that fall within a working_hours window
// for slotKind (tz-interpreted) and don't clash with any event on the account.
func FindFreeSlots(
	ctx context.Context,
	db *gorm.DB,
	tz *time.Location,
	accountKind models.AccountKind,
	slotKind models.SlotKind,
	durationMin int,
	windowStart, windowEnd time.Time,
	maxSlots int,
) ([]Slot, error) {
	if durationMin <= 0 {
		return nil, nil
	}
	duration := time.Duration(durationMin) * time.Minute

	var hours []models.WorkingHour
	if err := db.WithContext(ctx).Where("slot_kind = ?", slotKind).Find(&hours).Error; err != nil {
		return nil, err
	}
	if len(hours) == 0 {
		return nil, nil
	}

	busy, err := loadBusy(ctx, db, accountKind, windowStart, windowEnd.Add(duration))
	if err != nil {
		return nil, err
	}

	const step = 15 * time.Minute
	var out []Slot
	cursor := windowStart.Truncate(step)
	if cursor.Before(windowStart) {
		cursor = cursor.Add(step)
	}
	for !cursor.Add(duration).After(windowEnd) {
		end := cursor.Add(duration)
		if withinWorkingHours(cursor, end, hours, tz) && !overlapsAny(cursor, end, busy) {
			out = append(out, Slot{AccountKind: accountKind, Start: cursor, End: end})
			if maxSlots > 0 && len(out) >= maxSlots {
				return out, nil
			}
			cursor = end
			continue
		}
		cursor = cursor.Add(step)
	}
	return out, nil
}

type busyRange struct {
	start, end time.Time
}

func loadBusy(ctx context.Context, db *gorm.DB, kind models.AccountKind, from, to time.Time) ([]busyRange, error) {
	// All-day events don't block (birthdays, holidays) unless they mark
	// out-of-office time.
	var events []models.Event
	if err := db.WithContext(ctx).
		Where("account_kind = ? AND status <> 'cancelled' AND (all_day = false OR event_type = 'outOfOffice') AND end_time > ? AND start_time < ?",
			kind, from, to).
		Order("start_time").
		Find(&events).Error; err != nil {
		return nil, err
	}
	out := make([]busyRange, 0, len(events))
	for _, e := range events {
		out = append(out, busyRange{start: e.StartTime, end: e.EndTime})
	}
	// Planned sessions are busy too: a block committed earlier in the same
	// planner run has no Event row until the next calendar sync.
	var sessions []models.Session
	if err := db.WithContext(ctx).
		Where("account_kind = ? AND status = ? AND scheduled_end > ? AND scheduled_start < ?",
			kind, models.SessionPlanned, from, to).
		Find(&sessions).Error; err != nil {
		return nil, err
	}
	for _, s := range sessions {
		out = append(out, busyRange{start: s.ScheduledStart, end: s.ScheduledEnd})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start.Before(out[j].start) })
	return out, nil
}

func withinWorkingHours(start, end time.Time, hours []models.WorkingHour, tz *time.Location) bool {
	s := start.In(tz)
	e := end.In(tz)
	// An end at exactly midnight belongs to the previous day (endMin 1440).
	last := e.Add(-time.Nanosecond)
	if s.YearDay() != last.YearDay() || s.Year() != last.Year() {
		return false // don't straddle midnight
	}
	day := int(s.Weekday())
	startMin := s.Hour()*60 + s.Minute()
	endMin := e.Hour()*60 + e.Minute()
	if endMin == 0 {
		endMin = 1440
	}
	for _, h := range hours {
		if h.DayOfWeek == day && startMin >= h.StartMinute && endMin <= h.EndMinute {
			return true
		}
	}
	return false
}

func overlapsAny(start, end time.Time, busy []busyRange) bool {
	for _, b := range busy {
		if b.end.After(start) && b.start.Before(end) {
			return true
		}
	}
	return false
}
