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

func TestHabitTargetCountPerWeek(t *testing.T) {
	weekEnd := time.Now().Add(7 * 24 * time.Hour)
	got := habitTargetCount(models.Cadence{Type: "per_week", Count: 3}, time.Now(), weekEnd)
	if got != 3 {
		t.Fatalf("per_week count: got %d want 3", got)
	}
}
