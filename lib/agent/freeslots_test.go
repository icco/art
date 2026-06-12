package agent

import (
	"testing"
	"time"

	"github.com/icco/art/lib/models"
)

func mustTZ(t *testing.T) *time.Location {
	t.Helper()
	tz, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	return tz
}

func TestWithinWorkingHours(t *testing.T) {
	tz := mustTZ(t)
	// 2026-05-25 is a Monday in PT.
	monday := time.Date(2026, 5, 25, 10, 0, 0, 0, tz)
	hours := []models.WorkingHour{{DayOfWeek: 1, StartMinute: 9 * 60, EndMinute: 18 * 60}}

	if !withinWorkingHours(monday, monday.Add(time.Hour), hours, tz) {
		t.Fatal("expected 10:00-11:00 Mon inside 9-18 window to be allowed")
	}
	early := time.Date(2026, 5, 25, 8, 0, 0, 0, tz)
	if withinWorkingHours(early, early.Add(time.Hour), hours, tz) {
		t.Fatal("8:00-9:00 Mon should be outside the 9-18 window")
	}
	saturday := time.Date(2026, 5, 23, 10, 0, 0, 0, tz)
	if withinWorkingHours(saturday, saturday.Add(time.Hour), hours, tz) {
		t.Fatal("Saturday should be outside a Mon-only window")
	}
}

func TestOverlapsAny(t *testing.T) {
	now := time.Date(2026, 5, 25, 10, 0, 0, 0, time.UTC)
	busy := []busyRange{
		{start: now.Add(-2 * time.Hour), end: now.Add(-time.Hour)},
		{start: now.Add(30 * time.Minute), end: now.Add(90 * time.Minute)},
	}
	if !overlapsAny(now, now.Add(time.Hour), busy) {
		t.Fatal("expected overlap with the [10:30, 11:30) busy range")
	}
	if overlapsAny(now.Add(2*time.Hour), now.Add(3*time.Hour), busy) {
		t.Fatal("expected no overlap with a slot that starts after the last busy range ends")
	}
}

func TestFindSlotsPure(t *testing.T) {
	tz := mustTZ(t)
	// Monday 2026-05-25, working 9-18 PT.
	hours := []models.WorkingHour{{SlotKind: models.SlotWork, DayOfWeek: 1, StartMinute: 9 * 60, EndMinute: 18 * 60}}
	day := func(h, m int) time.Time { return time.Date(2026, 5, 25, h, m, 0, 0, tz) }
	busy := []busyRange{{start: day(10, 0), end: day(12, 0)}}

	slots := findSlots(hours, busy, tz, 60, day(9, 0), day(18, 0), 0)
	if len(slots) == 0 {
		t.Fatal("expected slots")
	}
	if !slots[0].Start.Equal(day(9, 0)) {
		t.Fatalf("first slot should be 9:00, got %v", slots[0].Start)
	}
	for _, s := range slots {
		if s.Start.Before(day(12, 0)) && s.End.After(day(10, 0)) && s.End.After(day(10, 0)) && s.Start.Before(day(10, 0)) {
			t.Fatalf("slot %v-%v overlaps busy", s.Start, s.End)
		}
	}
	// maxSlots respected.
	if got := findSlots(hours, busy, tz, 60, day(9, 0), day(18, 0), 2); len(got) != 2 {
		t.Fatalf("maxSlots: got %d slots", len(got))
	}
	// No working hours → no slots.
	if got := findSlots(nil, busy, tz, 60, day(9, 0), day(18, 0), 0); got != nil {
		t.Fatalf("no hours should give no slots, got %v", got)
	}
}

func TestIsAbsenceEvent(t *testing.T) {
	cases := []struct {
		name string
		ev   models.Event
		want bool
	}{
		{"ooo event type", models.Event{AllDay: true, EventType: "outOfOffice"}, true},
		{"vacation title", models.Event{AllDay: true, Summary: "Vacation - Lake Tahoe"}, true},
		{"ooo title", models.Event{AllDay: true, Summary: "Nat OOO"}, true},
		{"pto title", models.Event{AllDay: true, Summary: "PTO"}, true},
		{"birthday", models.Event{AllDay: true, Summary: "Mom's birthday"}, false},
		{"holiday calendar entry", models.Event{AllDay: true, Summary: "Juneteenth"}, false},
		{"timed ooo type still absence", models.Event{AllDay: false, EventType: "outOfOffice"}, true},
	}
	for _, tc := range cases {
		if got := isAbsenceEvent(tc.ev); got != tc.want {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

func TestHabitTargetCountPerWeek(t *testing.T) {
	weekEnd := time.Now().Add(7 * 24 * time.Hour)
	got := habitTargetCount(models.Cadence{Type: "per_week", Count: 3}, time.Now(), weekEnd)
	if got != 3 {
		t.Fatalf("per_week count: got %d want 3", got)
	}
}

func TestHabitTargetCountPerDay(t *testing.T) {
	tz := mustTZ(t)
	weekStart := time.Date(2026, 6, 15, 0, 0, 0, 0, tz) // Monday
	weekEnd := weekStart.AddDate(0, 0, 7)

	// A full week of a 1/day habit is 7 blocks, not 8.
	if got := habitTargetCount(models.Cadence{Type: "per_day", Count: 1}, weekStart, weekEnd); got != 7 {
		t.Fatalf("full week per_day: got %d want 7", got)
	}
	// From Wednesday noon, the partial Wednesday still counts: 5 days remain.
	wedNoon := weekStart.AddDate(0, 0, 2).Add(12 * time.Hour)
	if got := habitTargetCount(models.Cadence{Type: "per_day", Count: 1}, wedNoon, weekEnd); got != 5 {
		t.Fatalf("partial week per_day: got %d want 5", got)
	}
	// DST spring-forward week (Mar 8 2026, US): still 7 calendar days.
	dstWeek := time.Date(2026, 3, 9, 0, 0, 0, 0, tz)
	if got := habitTargetCount(models.Cadence{Type: "per_day", Count: 1}, dstWeek, dstWeek.AddDate(0, 0, 7)); got != 7 {
		t.Fatalf("DST week per_day: got %d want 7", got)
	}
}
