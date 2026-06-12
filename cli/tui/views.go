package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var (
	workStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#5fafff"))
	personalStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#87ff87"))
	focusStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaf00")).Bold(true)
)

func (a *App) renderWeek() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Week of %s\n\n", a.weekAnchor.Format("Mon Jan 2 2006"))
	for d := range 7 {
		day := a.weekAnchor.AddDate(0, 0, d)
		fmt.Fprintf(&b, "%s\n", day.Format("Mon Jan 2"))
		hasEvents := false
		for _, e := range a.events {
			if !sameDay(e.StartTime.In(day.Location()), day) {
				continue
			}
			hasEvents = true
			fmt.Fprintf(&b, "  %s\n", renderEventLine(e))
		}
		if !hasEvents {
			b.WriteString("  (empty)\n")
		}
	}
	return b.String()
}

func renderEventLine(e Event) string {
	t := fmt.Sprintf("%s–%s", e.StartTime.Local().Format("15:04"), e.EndTime.Local().Format("15:04"))
	label := fmt.Sprintf("%s  %s  [%s]", t, e.Summary, e.AccountKind)
	switch {
	case e.IsArtManaged || e.EventType == "focusTime":
		return focusStyle.Render(label)
	case e.AccountKind == "work":
		return workStyle.Render(label)
	default:
		return personalStyle.Render(label)
	}
}

func (a *App) renderProjects() string {
	if len(a.projects) == 0 {
		return "No projects.  Press 'a' to add."
	}
	var b strings.Builder
	for i, p := range a.projects {
		cursor := "  "
		if i == a.projCursor {
			cursor = "> "
		}
		dl := "no deadline"
		if p.Deadline != nil {
			dl = "due " + p.Deadline.Format("2006-01-02")
		}
		fmt.Fprintf(&b, "%s%s [%s] target=%.1fh %s status=%s\n",
			cursor, p.Name, p.Kind, p.TargetHours, dl, p.Status)
	}
	return b.String()
}

func (a *App) renderHabits() string {
	if len(a.habits) == 0 {
		return "No habits.  Press 'a' to add."
	}
	var b strings.Builder
	for i, h := range a.habits {
		cursor := "  "
		if i == a.habitCursor {
			cursor = "> "
		}
		active := "active"
		if !h.Active {
			active = "paused"
		}
		fmt.Fprintf(&b, "%s%s [%s] %dmin × %d/%s %s\n",
			cursor, h.Name, h.Kind, h.BlockDurationMinutes, h.Cadence.Count, h.Cadence.Type, active)
	}
	return b.String()
}

func (a *App) renderTasks() string {
	if len(a.tasks) == 0 {
		return "No tasks.  Press 'n' to quick-add one."
	}
	var b strings.Builder
	for i, t := range a.tasks {
		cursor := "  "
		if i == a.taskCursor {
			cursor = "> "
		}
		dl := ""
		if t.Deadline != nil {
			dl = " by " + t.Deadline.Local().Format("Mon Jan 2")
		}
		fmt.Fprintf(&b, "%s[%s] %s %s%s [%s]\n",
			cursor, taskStatusGlyph(t.Status), t.Title, formatMinutes(t.DurationMinutes), dl, t.Kind)
	}
	b.WriteString("\nenter=toggle done  d=delete  n=quick-add")
	return b.String()
}

func taskStatusGlyph(status string) string {
	switch status {
	case "done":
		return "x"
	case "scheduled":
		return "s"
	case "unschedulable":
		return "!"
	default:
		return " "
	}
}

func (a *App) renderForm() string {
	var b strings.Builder
	title := "New project"
	switch a.form.kind {
	case formKindHabit:
		title = "New habit"
	case formKindQuickAdd:
		title = "Quick-add task"
	case formKindHours:
		title = "Working hours (HH:MM-HH:MM, comma-separate multiple, blank = none)"
	}
	fmt.Fprintf(&b, "%s — Tab/Enter to submit, Esc to cancel\n\n", title)
	for i, f := range a.form.fields {
		cursor := "  "
		if i == a.form.active {
			cursor = "> "
		}
		fmt.Fprintf(&b, "%s%s: %s\n", cursor, f.label, f.value)
	}
	return b.String()
}

func sameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
}

func startOfWeekLocal(t time.Time) time.Time {
	d := int(t.Weekday())
	if d == 0 {
		d = 7
	}
	monday := t.AddDate(0, 0, -(d - 1))
	return time.Date(monday.Year(), monday.Month(), monday.Day(), 0, 0, 0, 0, monday.Location())
}
