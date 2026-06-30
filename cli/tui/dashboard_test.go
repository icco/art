package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestDashboardViewRendersTiles(t *testing.T) {
	// Freeze the clock so "today" filtering and relative times are stable.
	now := time.Date(2026, 6, 30, 9, 0, 0, 0, time.Local)
	orig := timeNow
	timeNow = func() time.Time { return now }
	defer func() { timeNow = orig }()

	dl := time.Date(2026, 8, 1, 0, 0, 0, 0, time.Local)
	var page Page = newDashboardPage(nil)
	msgs := []tea.Msg{
		tea.WindowSizeMsg{Width: 100, Height: 30},
		eventsMsg{[]Event{{Summary: "Standup", AccountKind: "work", StartTime: now.Add(time.Hour), EndTime: now.Add(90 * time.Minute)}}},
		projectsMsg{[]Project{{ID: "p1", Name: "Book", Kind: "work", TargetHours: 40, ScheduledHours: 12, Deadline: &dl, Status: "active"}}},
		habitsMsg{[]Habit{{ID: "h1", Name: "Run", Active: true, Cadence: Cadence{Type: "per_week", Count: 3}}}},
		sessionsMsg{[]Session{{Source: "habit", SourceID: "h1", Status: "happened"}, {Source: "habit", SourceID: "h1", Status: "planned"}}},
		runsMsg{[]AgentRun{{Kind: "planner", Status: "succeeded", StartedAt: now.Add(-2 * time.Minute)}}},
	}
	for _, m := range msgs {
		page, _ = page.Update(m)
	}

	view := page.View()
	for _, want := range []string{"TODAY", "Standup", "Book", "12/40h", "due Aug 1", "Run", "planner", "2m ago"} {
		if !strings.Contains(view, want) {
			t.Errorf("dashboard view missing %q\n---\n%s", want, view)
		}
	}
	if !strings.Contains(view, "●") || !strings.Contains(view, "○") {
		t.Errorf("expected cadence dots in view:\n%s", view)
	}
}
