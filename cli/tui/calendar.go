package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// calendarPage shows one week of events with art-managed blocks highlighted.
type calendarPage struct {
	client        *Client
	width, height int
	anchor        time.Time
	events        []Event
	keys          keyMap
}

func newCalendarPage(c *Client) calendarPage {
	return calendarPage{client: c, anchor: startOfWeek(timeNow()), keys: defaultKeyMap()}
}

func (p calendarPage) Title() string  { return "calendar" }
func (p calendarPage) FullInput() bool { return false }

func (p calendarPage) Init() tea.Cmd {
	return loadEvents(p.client, p.anchor, p.anchor.AddDate(0, 0, 7))
}

func (p calendarPage) Update(msg tea.Msg) (Page, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		p.width, p.height = msg.Width, msg.Height
	case eventsMsg:
		p.events = msg.events
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, p.keys.PrevWeek):
			p.anchor = p.anchor.AddDate(0, 0, -7)
			return p, loadEvents(p.client, p.anchor, p.anchor.AddDate(0, 0, 7))
		case key.Matches(msg, p.keys.NextWeek):
			p.anchor = p.anchor.AddDate(0, 0, 7)
			return p, loadEvents(p.client, p.anchor, p.anchor.AddDate(0, 0, 7))
		}
	}
	return p, nil
}

func (p calendarPage) View() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Week of %s\n\n", p.anchor.Format("Mon Jan 2 2006"))

	byDay := make(map[string][]Event)
	for _, e := range p.events {
		key := e.StartTime.Local().Format("2006-01-02")
		byDay[key] = append(byDay[key], e)
	}
	for d := range 7 {
		day := p.anchor.AddDate(0, 0, d)
		fmt.Fprintf(&b, "%s\n", headingStyle.Render(day.Format("Mon Jan 2")))
		evs := byDay[day.Format("2006-01-02")]
		if len(evs) == 0 {
			b.WriteString(faintStyle.Render("  —") + "\n")
			continue
		}
		sort.Slice(evs, func(i, j int) bool { return evs[i].StartTime.Before(evs[j].StartTime) })
		for _, e := range evs {
			b.WriteString("  " + renderEventLine(e) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderEventLine(e Event) string {
	span := fmt.Sprintf("%s–%s", e.StartTime.Local().Format("15:04"), e.EndTime.Local().Format("15:04"))
	mark := ""
	if e.IsArtManaged {
		mark = "◆ "
	}
	label := fmt.Sprintf("%s  %s%s  [%s]", span, mark, e.Summary, e.AccountKind)
	return kindStyle(e.AccountKind, e.IsArtManaged).Render(label)
}
