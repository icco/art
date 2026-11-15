package tui

import (
	"fmt"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// dashboardPage is the glanceable home screen: the last agent runs.
type dashboardPage struct {
	client *Client

	width, height int

	runs []AgentRun
}

func newDashboardPage(c *Client) dashboardPage { return dashboardPage{client: c} }

func (p dashboardPage) Title() string           { return "dashboard" }
func (p dashboardPage) FullInput() bool         { return false }
func (p dashboardPage) bindings() []key.Binding { return nil }

func (p dashboardPage) Init() tea.Cmd { return loadRuns(p.client) }

func (p dashboardPage) Update(msg tea.Msg) (Page, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width, p.height = msg.Width, msg.Height
	case runsMsg:
		p.runs = msg.runs
	}
	return p, nil
}

func (p dashboardPage) View() string {
	return lipgloss.JoinVertical(lipgloss.Left, tileHeadStyle.Render("LAST RUNS"), p.renderRuns())
}

func (p dashboardPage) renderRuns() string {
	if len(p.runs) == 0 {
		return faintStyle.Render("no runs yet")
	}
	r, ok := latestRun(p.runs, "triage")
	if !ok {
		return faintStyle.Render("no runs yet")
	}
	return fmt.Sprintf("%-8s %s %s", "triage", runStatusMark(r.Status), faintStyle.Render(relTime(r.StartedAt, timeNow())))
}

func runStatusMark(status string) string {
	switch status {
	case "succeeded":
		return okStyle.Render("✓")
	case "failed":
		return errorStyle.Render("✗")
	default:
		return faintStyle.Render("…")
	}
}

func latestRun(runs []AgentRun, kind string) (AgentRun, bool) {
	for _, r := range runs {
		if r.Kind == kind {
			return r, true // runs arrive newest-first
		}
	}
	return AgentRun{}, false
}
