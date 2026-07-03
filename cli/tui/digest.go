package tui

import (
	"fmt"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
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

// confirmData holds the confirm value on the heap so huh's *bool binding
// survives the value-receiver page being copied each Update.
type confirmData struct{ ok bool }

// digestPage lists triaged email and what art proposed/did with each. Triage
// itself is launched with the global `t` key; `x` marks a decision bad and
// undoes it after a confirm.
type digestPage struct {
	client        *Client
	width, height int
	list          list.Model
	keys          keyMap

	form      *huh.Form
	cf        *confirmData
	reverseID string
}

func newDigestPage(c *Client, isDark bool) digestPage {
	d := list.NewDefaultDelegate()
	d.Styles = list.NewDefaultItemStyles(isDark)
	l := list.New(nil, d, 0, 0)
	l.Title = "Email digest"
	l.SetShowHelp(false)
	return digestPage{client: c, list: l, keys: defaultKeyMap()}
}

func (p digestPage) Title() string   { return "digest" }
func (p digestPage) FullInput() bool { return p.form != nil || p.list.SettingFilter() }
func (p digestPage) bindings() []key.Binding {
	return []key.Binding{p.keys.Archive, p.keys.Reject}
}

func (p digestPage) Init() tea.Cmd { return loadEmails(p.client) }

func (p digestPage) Update(msg tea.Msg) (Page, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		p.width, p.height = m.Width, m.Height
		p.list.SetSize(m.Width, m.Height)
		if p.form != nil {
			p.form = p.form.WithWidth(m.Width).WithHeight(m.Height)
		}
		return p, nil
	case emailsMsg:
		return p, p.list.SetItems(emailItems(m.emails))
	case tea.KeyPressMsg:
		if p.form != nil {
			return p.updateForm(m)
		}
		return p.handleKey(m)
	}
	if p.form != nil {
		return p.updateForm(msg)
	}
	return p, nil
}

func (p digestPage) handleKey(m tea.KeyPressMsg) (Page, tea.Cmd) {
	if p.list.SettingFilter() {
		var cmd tea.Cmd
		p.list, cmd = p.list.Update(m)
		return p, cmd
	}
	if key.Matches(m, p.keys.Reject) {
		if it, ok := p.list.SelectedItem().(emailItem); ok {
			p.cf = &confirmData{}
			p.reverseID = it.e.ID
			p.form = newConfirmForm(p.cf, it.e.Subject, p.width, p.height)
			return p, p.form.Init()
		}
	}
	if key.Matches(m, p.keys.Archive) {
		if it, ok := p.list.SelectedItem().(emailItem); ok {
			// Toggle is instant and reversible: archive an inbox message, or
			// move an archived one back to the inbox.
			target := !it.e.Archived
			return p, tea.Sequence(setEmailArchived(p.client, it.e.ID, target), loadEmails(p.client))
		}
	}
	var cmd tea.Cmd
	p.list, cmd = p.list.Update(m)
	return p, cmd
}

func (p digestPage) updateForm(msg tea.Msg) (Page, tea.Cmd) {
	// huh's only abort binding is ctrl+c, which root intercepts to quit;
	// esc is the cancel path.
	if k, ok := msg.(tea.KeyPressMsg); ok && k.String() == "esc" {
		p.form, p.cf, p.reverseID = nil, nil, ""
		return p, nil
	}
	form, cmd := p.form.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		p.form = f
	}
	switch p.form.State {
	case huh.StateCompleted:
		confirmed, id := p.cf.ok, p.reverseID
		p.form, p.cf, p.reverseID = nil, nil, ""
		if confirmed {
			return p, tea.Sequence(reverseEmail(p.client, id), loadEmails(p.client))
		}
		return p, nil
	case huh.StateAborted:
		p.form, p.cf, p.reverseID = nil, nil, ""
		return p, nil
	}
	return p, cmd
}

func (p digestPage) View() string {
	if p.form != nil {
		return p.form.View()
	}
	if len(p.list.Items()) == 0 {
		// Render only the title so bubbles' default "No items." doesn't stack
		// above the hint (matches the projects/habits empty states).
		title := p.list.Styles.Title.Render(p.list.Title)
		return title + "\n\n" + faintStyle.Render("No triaged mail. Press t to run triage.")
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

func newConfirmForm(cf *confirmData, subject string, w, h int) *huh.Form {
	form := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title("Mark this decision bad and undo it?").
			Description(truncate(subject, 60)).
			Value(&cf.ok),
	))
	if w > 0 {
		form = form.WithWidth(w).WithHeight(h)
	}
	return form
}
