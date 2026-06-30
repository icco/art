package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

type emailItem struct{ e Email }

func (i emailItem) FilterValue() string { return i.e.Subject }
func (i emailItem) Title() string {
	tag := i.e.Action
	switch {
	case i.e.Reversed:
		tag = "↶" + tag
	case !i.e.Applied:
		tag = "~" + tag // proposed only (dry run)
	}
	return fmt.Sprintf("%-9s %s", tag, i.e.Subject)
}

func (i emailItem) Description() string {
	from := truncate(i.e.From, 32)
	if i.e.Summary != "" {
		return from + " · " + i.e.Summary
	}
	return from
}

// digestPage lists triaged email and what art proposed/did with each. Triage
// itself is launched with the global `t` key.
type digestPage struct {
	client        *Client
	width, height int
	list          list.Model
}

func newDigestPage(c *Client, isDark bool) digestPage {
	d := list.NewDefaultDelegate()
	d.Styles = list.NewDefaultItemStyles(isDark)
	l := list.New(nil, d, 0, 0)
	l.Title = "Email digest"
	l.SetShowHelp(false)
	return digestPage{client: c, list: l}
}

func (p digestPage) Title() string  { return "digest" }
func (p digestPage) FullInput() bool { return p.list.SettingFilter() }

func (p digestPage) Init() tea.Cmd { return loadEmails(p.client) }

func (p digestPage) Update(msg tea.Msg) (Page, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		p.width, p.height = m.Width, m.Height
		p.list.SetSize(m.Width, m.Height)
		return p, nil
	case emailsMsg:
		return p, p.list.SetItems(emailItems(m.emails))
	case tea.KeyPressMsg:
		var cmd tea.Cmd
		p.list, cmd = p.list.Update(m)
		return p, cmd
	}
	return p, nil
}

func (p digestPage) View() string {
	if len(p.list.Items()) == 0 {
		return strings.TrimRight(p.list.View(), "\n") + "\n\n" + faintStyle.Render("No triaged mail. Press t to run triage.")
	}
	return p.list.View()
}

func emailItems(emails []Email) []list.Item {
	items := make([]list.Item, len(emails))
	for i, e := range emails {
		items[i] = emailItem{e}
	}
	return items
}
