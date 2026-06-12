// Package agent implements the art planning and scheduling agents.
package agent

import (
	"context"
	"regexp"
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
// inside [windowStart, windowEnd) that fall within a working_hours window for
// slotKind (tz-interpreted). Busy times from events on *all* accounts block a
// slot — a personal block must not land on a work meeting — plus any
// extraBusy ranges (e.g. blocks committed earlier in the same planner run
// that haven't synced back yet). accountKind only labels the returned slots
// with the account the block would be created on.
func FindFreeSlots(
	ctx context.Context,
	db *gorm.DB,
	tz *time.Location,
	accountKind models.AccountKind,
	slotKind models.SlotKind,
	durationMin int,
	windowStart, windowEnd time.Time,
	maxSlots int,
	extraBusy []Slot,
) ([]Slot, error) {
	if durationMin <= 0 {
		return nil, nil
	}
	duration := time.Duration(durationMin) * time.Minute

	var hours []models.WorkingHour
	if err := db.WithContext(ctx).Where("slot_kind = ?", slotKind).Find(&hours).Error; err != nil {
		return nil, err
	}

	busy, err := loadBusy(ctx, db, windowStart, windowEnd.Add(duration))
	if err != nil {
		return nil, err
	}
	for _, s := range extraBusy {
		busy = append(busy, busyRange{start: s.Start, end: s.End})
	}

	ranges := findSlots(hours, busy, tz, durationMin, windowStart, windowEnd, maxSlots)
	out := make([]Slot, len(ranges))
	for i, r := range ranges {
		out[i] = Slot{AccountKind: accountKind, Start: r.Start, End: r.End}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// findSlots is the pure core of free-slot search: no DB, no account. It
// walks [windowStart, windowEnd) in 15-minute steps and returns up to
// maxSlots non-overlapping duration-long ranges inside working hours that
// avoid busy ranges.
func findSlots(
	hours []models.WorkingHour,
	busy []busyRange,
	tz *time.Location,
	durationMin int,
	windowStart, windowEnd time.Time,
	maxSlots int,
) []Slot {
	if durationMin <= 0 || len(hours) == 0 {
		return nil
	}
	duration := time.Duration(durationMin) * time.Minute

	const step = 15 * time.Minute
	var out []Slot
	cursor := windowStart.Truncate(step)
	if cursor.Before(windowStart) {
		cursor = cursor.Add(step)
	}
	for !cursor.Add(duration).After(windowEnd) {
		end := cursor.Add(duration)
		if withinWorkingHours(cursor, end, hours, tz) && !overlapsAny(cursor, end, busy) {
			out = append(out, Slot{Start: cursor, End: end})
			if maxSlots > 0 && len(out) >= maxSlots {
				return out
			}
			cursor = end
			continue
		}
		cursor = cursor.Add(step)
	}
	return out
}

type busyRange struct {
	start, end time.Time
}

// absenceTitleRe matches all-day event titles that indicate the owner is
// away (vacation/PTO/OOO) as opposed to birthdays, holidays, or reminders.
var absenceTitleRe = regexp.MustCompile(`(?i)\b(ooo|out of office|vacation|pto)\b`)

// isAbsenceEvent reports whether ev blocks scheduling even though regular
// all-day events don't: Google outOfOffice events and absence-titled
// all-day events.
func isAbsenceEvent(ev models.Event) bool {
	if ev.EventType == "outOfOffice" {
		return true
	}
	return ev.AllDay && absenceTitleRe.MatchString(ev.Summary)
}

// loadBusy returns the busy ranges in [from, to) across every linked
// account: all timed events, plus all-day events that look like absence
// (vacation days block scheduling; birthdays don't).
func loadBusy(ctx context.Context, db *gorm.DB, from, to time.Time) ([]busyRange, error) {
	var events []models.Event
	if err := db.WithContext(ctx).
		Where("status <> 'cancelled' AND end_time > ? AND start_time < ?", from, to).
		Order("start_time").
		Find(&events).Error; err != nil {
		return nil, err
	}
	var out []busyRange
	for _, e := range events {
		if e.AllDay && !isAbsenceEvent(e) {
			continue
		}
		out = append(out, busyRange{start: e.StartTime, end: e.EndTime})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start.Before(out[j].start) })
	return out, nil
}

func withinWorkingHours(start, end time.Time, hours []models.WorkingHour, tz *time.Location) bool {
	s := start.In(tz)
	e := end.In(tz)
	if s.YearDay() != e.YearDay() || s.Year() != e.Year() {
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
