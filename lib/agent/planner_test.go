package agent

import (
	"testing"
	"time"

	"github.com/icco/art/lib/models"
)

func TestAccountForKind(t *testing.T) {
	if accountForKind(models.SlotWork) != models.AccountWork {
		t.Fatal("work slot should map to work account")
	}
	if accountForKind(models.SlotPersonal) != models.AccountPersonal {
		t.Fatal("personal slot should map to personal account")
	}
}

func TestFocusTitle(t *testing.T) {
	if got := focusTitle(models.SourceProject, "Service"); got != "Focus: Service" {
		t.Fatalf("project title: %q", got)
	}
	if got := focusTitle(models.SourceHabit, "Walk"); got != "Habit: Walk" {
		t.Fatalf("habit title: %q", got)
	}
}

func TestFocusDescription(t *testing.T) {
	got := focusDescription(models.SourceProject, "abc")
	if got == "" || !contains(got, "abc") || !contains(got, "project") {
		t.Fatalf("description missing source/id: %q", got)
	}
}

func TestStartOfWeek(t *testing.T) {
	tz, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	// Wednesday 2026-05-27 PT
	wed := time.Date(2026, 5, 27, 14, 30, 0, 0, tz)
	got := startOfWeek(wed, tz)
	want := time.Date(2026, 5, 25, 0, 0, 0, 0, tz) // Monday
	if !got.Equal(want) {
		t.Fatalf("startOfWeek(Wed) = %v want %v", got, want)
	}
	// Sunday should still wrap to *that* Monday-of-the-week, i.e. previous Monday.
	sun := time.Date(2026, 5, 31, 9, 0, 0, 0, tz)
	got = startOfWeek(sun, tz)
	if got.Weekday() != time.Monday {
		t.Fatalf("startOfWeek(Sun) weekday = %v", got.Weekday())
	}
}

func TestHabitTargetCount(t *testing.T) {
	now := time.Now()
	weekEnd := now.Add(7 * 24 * time.Hour)
	if got := habitTargetCount(models.Cadence{Type: "per_week", Count: 4}, now, weekEnd); got != 4 {
		t.Fatalf("per_week: %d", got)
	}
	if got := habitTargetCount(models.Cadence{Type: "per_day", Count: 1}, now, weekEnd); got < 6 || got > 8 {
		t.Fatalf("per_day approximate: %d", got)
	}
	if got := habitTargetCount(models.Cadence{Type: "unknown", Count: 2}, now, weekEnd); got != 2 {
		t.Fatalf("default branch: %d", got)
	}
}

func TestMaxTime(t *testing.T) {
	a := time.Now()
	b := a.Add(time.Hour)
	if !maxTime(a, b).Equal(b) || !maxTime(b, a).Equal(b) {
		t.Fatal("maxTime should pick the later time")
	}
}

func TestAppendErr(t *testing.T) {
	s := map[string]any{"errors": []string{}}
	appendErr(s, "oops")
	appendErr(s, "again")
	errs, _ := s["errors"].([]string)
	if len(errs) != 2 || errs[0] != "oops" || errs[1] != "again" {
		t.Fatalf("appendErr produced: %v", errs)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
