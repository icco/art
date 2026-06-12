package agent

import (
	"testing"
	"time"
)

func TestWeekWindow(t *testing.T) {
	tz, _ := time.LoadLocation("America/Los_Angeles")
	wed := time.Date(2026, 5, 27, 14, 30, 0, 0, tz)
	start, end := WeekWindow(wed, tz)
	if start.Weekday() != time.Monday || start.Hour() != 0 {
		t.Fatalf("week start not Mon 00:00: %v", start)
	}
	if !end.Equal(start.Add(7 * 24 * time.Hour)) {
		t.Fatalf("week end != start+7d: %v vs %v", end, start)
	}
}

func TestNextHour(t *testing.T) {
	in := time.Date(2026, 5, 27, 14, 17, 30, 0, time.UTC)
	got := NextHour(in)
	want := time.Date(2026, 5, 27, 15, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("NextHour: got %v want %v", got, want)
	}

	// Already on the boundary: still advance.
	on := time.Date(2026, 5, 27, 14, 0, 0, 0, time.UTC)
	if got := NextHour(on); !got.Equal(want) {
		t.Fatalf("NextHour on boundary: got %v want %v", got, want)
	}
}

func TestPlanningStartTakesLater(t *testing.T) {
	tz, _ := time.LoadLocation("America/Los_Angeles")
	// Wednesday afternoon → PlanningStart should be the next hour, not Monday 00:00.
	wed := time.Date(2026, 5, 27, 14, 17, 0, 0, tz)
	got := PlanningStart(wed, tz)
	wantMin := time.Date(2026, 5, 27, 15, 0, 0, 0, time.UTC)
	if got.Before(wantMin) {
		t.Fatalf("PlanningStart should be >= next-hour boundary: got %v", got)
	}
}

func TestPlanningStartClampsToWeekStart(t *testing.T) {
	tz, _ := time.LoadLocation("America/Los_Angeles")
	// Late Sunday: nextHour rolls into Monday-of-next-week, but WeekWindow
	// of Sunday is still the *current* week starting last Monday. The max
	// should be the next-hour boundary (later than weekStart).
	sun := time.Date(2026, 5, 31, 23, 30, 0, 0, tz)
	got := PlanningStart(sun, tz)
	weekStart, _ := WeekWindow(sun, tz)
	if got.Before(weekStart) {
		t.Fatalf("PlanningStart never earlier than weekStart, got %v < %v", got, weekStart)
	}
}
