package tui

import (
	"fmt"
	"sort"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// sessionsPage lists the current week's planned focus blocks and lets the
// owner retract one. The planner only schedules the current week, so the page
// is scoped to it — retracting a block frees its habit or project to be
// re-planned onto a different slot on the next planner pass.
type sessionsPage struct {
	client        *Client
	width, height int
	anchor        time.Time
	list          list.Model
	sessions      []Session
	names         map[string]string // source_id -> display name
	keys          keyMap
}

func newSessionsPage(c *Client, isDark bool) sessionsPage {
	d := list.NewDefaultDelegate()
	d.Styles = list.NewDefaultItemStyles(isDark)
	l := list.New(nil, d, 0, 0)
	l.Title = "Sessions"
	l.SetShowHelp(false)
	return sessionsPage{
		client: c,
		anchor: startOfWeek(timeNow()),
		list:   l,
		names:  map[string]string{},
		keys:   defaultKeyMap(),
	}
}

func (p sessionsPage) Title() string   { return "sessions" }
func (p sessionsPage) FullInput() bool { return p.list.SettingFilter() }
func (p sessionsPage) bindings() []key.Binding {
	return []key.Binding{p.keys.Delete}
}

// Init loads this week's sessions plus projects/habits, whose names label the
// otherwise opaque source IDs.
func (p sessionsPage) Init() tea.Cmd {
	return tea.Batch(p.load(), loadProjects(p.client), loadHabits(p.client))
}

func (p sessionsPage) load() tea.Cmd {
	return loadSessions(p.client, p.anchor, p.anchor.AddDate(0, 0, 7))
}

func (p sessionsPage) Update(msg tea.Msg) (Page, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		p.width, p.height = m.Width, m.Height
		p.list.SetSize(m.Width, m.Height)
		return p, nil
	case sessionsMsg:
		p.sessions = m.sessions
		return p, p.list.SetItems(p.items())
	case projectsMsg:
		for _, pr := range m.projects {
			p.names[pr.ID] = "Project: " + pr.Name
		}
		return p, p.list.SetItems(p.items())
	case habitsMsg:
		for _, h := range m.habits {
			p.names[h.ID] = "Habit: " + h.Name
		}
		return p, p.list.SetItems(p.items())
	case tea.KeyPressMsg:
		return p.handleKey(m)
	}
	return p, nil
}

func (p sessionsPage) handleKey(m tea.KeyPressMsg) (Page, tea.Cmd) {
	if p.list.SettingFilter() {
		var cmd tea.Cmd
		p.list, cmd = p.list.Update(m)
		return p, cmd
	}
	if key.Matches(m, p.keys.Delete) {
		if it, ok := p.list.SelectedItem().(sessionItem); ok {
			return p, tea.Sequence(deleteSession(p.client, it.s.ID), p.load())
		}
	}
	var cmd tea.Cmd
	p.list, cmd = p.list.Update(m)
	return p, cmd
}

func (p sessionsPage) items() []list.Item {
	ss := append([]Session{}, p.sessions...)
	sort.Slice(ss, func(i, j int) bool { return ss[i].ScheduledStart.Before(ss[j].ScheduledStart) })
	items := make([]list.Item, len(ss))
	for i, s := range ss {
		items[i] = sessionItem{s: s, name: p.names[s.SourceID]}
	}
	return items
}

func (p sessionsPage) View() string {
	if len(p.list.Items()) == 0 {
		title := p.list.Styles.Title.Render(p.list.Title)
		return title + "\n\n" + faintStyle.Render("No planned sessions this week.")
	}
	return p.list.View()
}

// sessionItem renders one planned block; name is the resolved project/habit
// label, falling back to the raw source kind when it isn't loaded yet.
type sessionItem struct {
	s    Session
	name string
}

func (i sessionItem) Title() string {
	label := i.name
	if label == "" {
		label = i.s.Source
	}
	day := i.s.ScheduledStart.Local().Format("Mon Jan 2")
	span := i.s.ScheduledStart.Local().Format("15:04") + "–" + i.s.ScheduledEnd.Local().Format("15:04")
	return fmt.Sprintf("%s  %s  %s", day, span, label)
}

func (i sessionItem) FilterValue() string { return i.Title() }
func (i sessionItem) Description() string {
	return fmt.Sprintf("[%s] %s · %s", i.s.AccountKind, i.s.Source, i.s.Status)
}
