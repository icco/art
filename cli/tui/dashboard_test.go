package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestDashboardViewRendersRuns(t *testing.T) {
	// Freeze the clock so relative times are stable.
	now := time.Date(2026, 6, 30, 9, 0, 0, 0, time.Local)
	orig := timeNow
	timeNow = func() time.Time { return now }
	defer func() { timeNow = orig }()

	var page Page = newDashboardPage(nil)
	if !strings.Contains(page.View(), "no runs yet") {
		t.Errorf("empty dashboard should say no runs yet:\n%s", page.View())
	}
	msgs := []tea.Msg{
		tea.WindowSizeMsg{Width: 100, Height: 30},
		runsMsg{[]AgentRun{{Kind: "triage", Status: "succeeded", StartedAt: now.Add(-2 * time.Minute)}}},
	}
	for _, m := range msgs {
		page, _ = page.Update(m)
	}

	view := page.View()
	for _, want := range []string{"LAST RUNS", "triage", "2m ago"} {
		if !strings.Contains(view, want) {
			t.Errorf("dashboard view missing %q\n---\n%s", want, view)
		}
	}
}
