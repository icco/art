// Package agent implements the art planning and scheduling agents.
package agent

import (
	"context"
	"sort"
	"time"

	"github.com/icco/art/lib/calendar"
	"github.com/icco/art/lib/models"
	"gorm.io/gorm"
)

// Slot is a candidate free interval on a particular account. Soft means it is
// only free because it sits on a placeholder event, so prefer non-soft slots.
type Slot struct {
	AccountKind models.AccountKind
	Start       time.Time
	End         time.Time
	Soft        bool
}

// FindFreeSlots returns up to maxSlots non-overlapping durationMin-long slots
// inside [windowStart, windowEnd) that fall within a working_hours window
// for slotKind (tz-interpreted) and don't clash with any event on the account.
//
// Events named in soft don't clash; the slots they cover come back marked Soft
// after every hard-free slot, so taking the first result prefers real free time.
func FindFreeSlots(
	ctx context.Context,
	db *gorm.DB,
	tz *time.Location,
	accountKind models.AccountKind,
	slotKind models.SlotKind,
	durationMin int,
	windowStart, windowEnd time.Time,
	maxSlots int,
	soft models.SoftTitles,
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

	busy, err := loadBusy(ctx, db, windowStart, windowEnd.Add(duration), soft)
	if err != nil {
		return nil, err
	}

	// scan collects non-overlapping slots that clear working hours and avoid.
	scan := func(avoid []busyRange, isSoft bool) []Slot {
		const step = 15 * time.Minute
		var out []Slot
		cursor := windowStart.Truncate(step)
		if cursor.Before(windowStart) {
			cursor = cursor.Add(step)
		}
		for !cursor.Add(duration).After(windowEnd) {
			end := cursor.Add(duration)
			if withinWorkingHours(cursor, end, hours, tz) && !overlapsAny(cursor, end, avoid) {
				out = append(out, Slot{AccountKind: accountKind, Start: cursor, End: end, Soft: isSoft})
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

	out := scan(busy, false)
	if len(soft) == 0 || (maxSlots > 0 && len(out) >= maxSlots) {
		return out, nil
	}

	// Placeholders stop counting as busy, but the hard slots just chosen start:
	// a soft slot must never shadow real free time.
	relaxed := make([]busyRange, 0, len(busy)+len(out))
	for _, b := range busy {
		if !b.soft {
			relaxed = append(relaxed, b)
		}
	}
	for _, s := range out {
		relaxed = append(relaxed, busyRange{start: s.Start, end: s.End})
	}
	for _, s := range scan(relaxed, true) {
		// A slot no placeholder covers isn't soft — the hard pass already saw it.
		if !overlapsAny(s.Start, s.End, busy) {
			continue
		}
		out = append(out, s)
		if maxSlots > 0 && len(out) >= maxSlots {
			break
		}
	}
	return out, nil
}

type busyRange struct {
	start, end time.Time
	// soft marks a placeholder event the planner may schedule over.
	soft bool
}

func loadBusy(ctx context.Context, db *gorm.DB, from, to time.Time, soft models.SoftTitles) ([]busyRange, error) {
	owned, err := calendar.OwnedCalendarIDs(ctx, db)
	if err != nil {
		return nil, err
	}
	// Busy spans both calendars: the owner can only be in one place, so a work
	// meeting blocks a personal block and vice versa. Scoping to account_kind
	// would let Art book focus time on top of a meeting on the other calendar.
	q := db.WithContext(ctx).
		Where(`status <> 'cancelled' AND transparency <> 'transparent'
		       AND (all_day = false OR event_type = 'outOfOffice') AND end_time > ? AND start_time < ?`,
			from, to)
	// No linked account yet: treat every calendar as the owner's rather than none.
	if len(owned) > 0 {
		q = q.Where("calendar_id IN ?", owned)
	}
	var events []models.Event
	if err := q.Order("start_time").Find(&events).Error; err != nil {
		return nil, err
	}
	out := make([]busyRange, 0, len(events))
	for _, e := range events {
		// Art's own blocks are never soft, or two sessions would stack up.
		out = append(out, busyRange{start: e.StartTime, end: e.EndTime, soft: !e.IsArtManaged && soft.Match(e.Summary)})
	}
	// Planned sessions are busy too; they have no Event row until the next sync.
	var sessions []models.Session
	if err := db.WithContext(ctx).
		Where("status = ? AND scheduled_end > ? AND scheduled_start < ?",
			models.SessionPlanned, from, to).
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

// overlapsHard is overlapsAny ignoring soft (schedulable-over) ranges.
func overlapsHard(start, end time.Time, busy []busyRange) bool {
	for _, b := range busy {
		if !b.soft && b.end.After(start) && b.start.Before(end) {
			return true
		}
	}
	return false
}
