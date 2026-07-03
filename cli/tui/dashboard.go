package tui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// dashboardPage is the glanceable home screen: today's schedule, project
// progress, habit cadence, and last agent runs.
type dashboardPage struct {
	client *Client

	width, height int

	events   []Event
	projects []Project
	habits   []Habit
	sessions []Session
	runs     []AgentRun
	loaded   bool
}

func newDashboardPage(c *Client) dashboardPage { return dashboardPage{client: c} }

func (p dashboardPage) Title() string           { return "dashboard" }
func (p dashboardPage) FullInput() bool         { return false }
func (p dashboardPage) bindings() []key.Binding { return nil }

func (p dashboardPage) Init() tea.Cmd {
	from := startOfWeek(timeNow())
	to := from.AddDate(0, 0, 7)
	return tea.Batch(
		loadEvents(p.client, from, to),
		loadProjects(p.client),
		loadHabits(p.client),
		loadSessions(p.client, from, to),
		loadRuns(p.client),
	)
}

func (p dashboardPage) Update(msg tea.Msg) (Page, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width, p.height = msg.Width, msg.Height
	case eventsMsg:
		p.events, p.loaded = msg.events, true
	case errMsg:
		p.loaded = true // a failed fetch must not leave "loading…" up forever
	case projectsMsg:
		p.projects = msg.projects
	case habitsMsg:
		p.habits = msg.habits
	case sessionsMsg:
		p.sessions = msg.sessions
	case runsMsg:
		p.runs = msg.runs
	}
	return p, nil
}

func (p dashboardPage) View() string {
	width := p.width
	if width == 0 {
		width = 80
	}
	colW := max(width/2-2, 20)

	left := lipgloss.JoinVertical(lipgloss.Left,
		tileHeadStyle.Render("TODAY"),
		p.renderToday(),
		"",
		tileHeadStyle.Render("LAST RUNS"),
		p.renderRuns(),
	)
	right := lipgloss.JoinVertical(lipgloss.Left,
		tileHeadStyle.Render("PROJECTS"),
		p.renderProjects(),
		"",
		tileHeadStyle.Render("HABITS"),
		p.renderHabits(),
	)
	leftCol := lipgloss.NewStyle().Width(colW).Render(left)
	rightCol := lipgloss.NewStyle().Width(colW).Render(right)
	return lipgloss.JoinHorizontal(lipgloss.Top, leftCol, "  ", rightCol)
}

func (p dashboardPage) renderToday() string {
	if !p.loaded {
		return faintStyle.Render("loading…")
	}
	now := timeNow()
	var today []Event
	for _, e := range p.events {
		if sameLocalDay(e.StartTime, now) {
			today = append(today, e)
		}
	}
	if len(today) == 0 {
		return faintStyle.Render("nothing scheduled")
	}
	sort.Slice(today, func(i, j int) bool { return today[i].StartTime.Before(today[j].StartTime) })
	var b strings.Builder
	for _, e := range today {
		st := kindStyle(e.AccountKind, e.IsArtManaged)
		mark := "  "
		if e.IsArtManaged {
			mark = "◆ "
		}
		fmt.Fprintf(&b, "%s%s %s\n", mark, e.StartTime.Local().Format("15:04"), st.Render(e.Summary))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (p dashboardPage) renderProjects() string {
	active := make([]Project, 0, len(p.projects))
	for _, pr := range p.projects {
		if pr.Status != "done" {
			active = append(active, pr)
		}
	}
	if len(active) == 0 {
		return faintStyle.Render("no active projects")
	}
	barW := 6
	var b strings.Builder
	for _, pr := range active {
		frac := 0.0
		if pr.TargetHours > 0 {
			frac = pr.ScheduledHours / pr.TargetHours
		}
		due := ""
		if pr.Deadline != nil {
			due = " due " + pr.Deadline.Format("Jan 2")
		}
		fmt.Fprintf(&b, "%s %s %.0f/%.0fh%s\n",
			kindStyle(pr.Kind, false).Render(truncate(pr.Name, 14)),
			progressBar(frac, barW), pr.ScheduledHours, pr.TargetHours, faintStyle.Render(due))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (p dashboardPage) renderHabits() string {
	active := make([]Habit, 0, len(p.habits))
	for _, h := range p.habits {
		if h.Active {
			active = append(active, h)
		}
	}
	if len(active) == 0 {
		return faintStyle.Render("no active habits")
	}
	var b strings.Builder
	for _, h := range active {
		happened, planned := cadenceCounts(p.sessions, h.ID)
		target := h.Cadence.Count
		fmt.Fprintf(&b, "%s %s %d×/wk\n",
			truncate(h.Name, 12), cadenceDots(happened, planned, target), target)
	}
	return strings.TrimRight(b.String(), "\n")
}

func (p dashboardPage) renderRuns() string {
	if len(p.runs) == 0 {
		return faintStyle.Render("no runs yet")
	}
	now := timeNow()
	var b strings.Builder
	for _, kind := range []string{"planner", "triage"} {
		if r, ok := latestRun(p.runs, kind); ok {
			fmt.Fprintf(&b, "%-8s %s %s\n", kind, runStatusMark(r.Status), faintStyle.Render(relTime(r.StartedAt, now)))
		}
	}
	if b.Len() == 0 {
		return faintStyle.Render("no runs yet")
	}
	return strings.TrimRight(b.String(), "\n")
}

// cadenceDots renders filled dots for happened, hollow for planned, padded to
// the weekly target.
func cadenceDots(happened, planned, target int) string {
	dots := strings.Repeat("●", happened) + strings.Repeat("○", planned)
	scheduled := happened + planned
	if scheduled < target {
		dots += strings.Repeat("·", target-scheduled)
	}
	if happened >= target && target > 0 {
		return okStyle.Render(dots) + " ✓"
	}
	return dots
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
