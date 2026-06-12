package tui

import (
	"strings"
	"testing"
	"time"
)

func TestFormatTask(t *testing.T) {
	deadline := time.Date(2026, 6, 19, 23, 59, 59, 0, time.Local)
	got := formatTask(Task{Title: "pack office", DurationMinutes: 120, Deadline: &deadline, Kind: "personal"})
	want := `"pack office" 2h by Fri Jun 19 [personal]`
	if got != want {
		t.Fatalf("formatTask: got %q want %q", got, want)
	}

	got = formatTask(Task{Title: "call dentist", DurationMinutes: 30, Kind: "personal"})
	if got != `"call dentist" 30m [personal]` {
		t.Fatalf("formatTask no deadline: got %q", got)
	}
}

func TestFormatStatus(t *testing.T) {
	tz, _ := time.LoadLocation("America/Los_Angeles")
	start := time.Date(2026, 6, 15, 9, 0, 0, 0, tz)
	r := StatusReport{
		Upcoming: []UpcomingBlock{{
			Source: "task", Title: "pack office", AccountKind: "personal",
			Start: start, End: start.Add(2 * time.Hour), Status: "planned",
		}},
		TasksPending:       []Task{{Title: "call dentist", DurationMinutes: 30, Kind: "personal"}},
		TasksUnschedulable: []Task{{Title: "impossible", DurationMinutes: 60, Kind: "work"}},
		LastRun: &AgentRun{
			StartedAt: start, Status: "succeeded", Model: "deterministic",
			Summary: []byte(`{"tasks_scheduled":1}`),
		},
	}
	out := formatStatus(r, tz)
	for _, want := range []string{
		"Mon Jun 15  09:00-11:00  task: pack office [personal]",
		"Pending tasks",
		`"call dentist" 30m [personal]`,
		"Unschedulable",
		`"impossible" 1h [work]`,
		"Last planner run",
		"deterministic",
		`{"tasks_scheduled":1}`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q:\n%s", want, out)
		}
	}

	empty := formatStatus(StatusReport{}, tz)
	if !strings.Contains(empty, "(none)") {
		t.Errorf("empty status should say (none):\n%s", empty)
	}
}

func TestFormatMinutes(t *testing.T) {
	cases := map[int]string{30: "30m", 60: "1h", 90: "1h30m", 120: "2h"}
	for mins, want := range cases {
		if got := formatMinutes(mins); got != want {
			t.Errorf("formatMinutes(%d): got %q want %q", mins, got, want)
		}
	}
}
