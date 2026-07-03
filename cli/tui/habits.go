package tui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
)

type habitForm struct {
	name, kind, minutes, perWeek string
	active                       bool
}

type habitItem struct{ h Habit }

func (i habitItem) Title() string       { return i.h.Name }
func (i habitItem) FilterValue() string { return i.h.Name }
func (i habitItem) Description() string {
	state := "active"
	if !i.h.Active {
		state = "paused"
	}
	return fmt.Sprintf("[%s] %dmin × %d/%s · %s",
		i.h.Kind, i.h.BlockDurationMinutes, i.h.Cadence.Count, i.h.Cadence.Type, state)
}

type habitsPage struct {
	client        *Client
	width, height int
	list          list.Model

	form   *huh.Form
	fd     *habitForm
	editID string
	keys   keyMap
}

func newHabitsPage(c *Client, isDark bool) habitsPage {
	d := list.NewDefaultDelegate()
	d.Styles = list.NewDefaultItemStyles(isDark)
	l := list.New(nil, d, 0, 0)
	l.Title = "Habits"
	l.SetShowHelp(false)
	return habitsPage{client: c, list: l, keys: defaultKeyMap()}
}

func (p habitsPage) Title() string   { return "habits" }
func (p habitsPage) FullInput() bool { return p.form != nil || p.list.SettingFilter() }
func (p habitsPage) bindings() []key.Binding {
	return []key.Binding{p.keys.Add, p.keys.Edit, p.keys.Delete}
}

func (p habitsPage) Init() tea.Cmd { return loadHabits(p.client) }

func (p habitsPage) Update(msg tea.Msg) (Page, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		p.width, p.height = m.Width, m.Height
		p.list.SetSize(m.Width, m.Height)
		if p.form != nil {
			p.form = p.form.WithWidth(m.Width).WithHeight(m.Height)
		}
		return p, nil
	case habitsMsg:
		return p, p.list.SetItems(habitItems(m.habits))
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

func (p habitsPage) handleKey(m tea.KeyPressMsg) (Page, tea.Cmd) {
	if p.list.SettingFilter() {
		var cmd tea.Cmd
		p.list, cmd = p.list.Update(m)
		return p, cmd
	}
	switch {
	case key.Matches(m, p.keys.Add):
		p.form, p.fd, p.editID = newHabitForm(nil, p.width, p.height)
		return p, p.form.Init()
	case key.Matches(m, p.keys.Edit):
		if it, ok := p.list.SelectedItem().(habitItem); ok {
			h := it.h
			p.form, p.fd, p.editID = newHabitForm(&h, p.width, p.height)
			return p, p.form.Init()
		}
	case key.Matches(m, p.keys.Delete):
		if it, ok := p.list.SelectedItem().(habitItem); ok {
			return p, tea.Sequence(deleteHabit(p.client, it.h.ID), loadHabits(p.client))
		}
	}
	var cmd tea.Cmd
	p.list, cmd = p.list.Update(m)
	return p, cmd
}

func (p habitsPage) updateForm(msg tea.Msg) (Page, tea.Cmd) {
	// huh's only abort binding is ctrl+c, which root intercepts to quit;
	// esc is the cancel path.
	if k, ok := msg.(tea.KeyPressMsg); ok && k.String() == "esc" {
		p.form, p.fd, p.editID = nil, nil, ""
		return p, nil
	}
	form, cmd := p.form.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		p.form = f
	}
	switch p.form.State {
	case huh.StateCompleted:
		submit := p.submitForm()
		p.form, p.fd, p.editID = nil, nil, ""
		return p, tea.Sequence(submit, loadHabits(p.client))
	case huh.StateAborted:
		p.form, p.fd, p.editID = nil, nil, ""
		return p, nil
	}
	return p, cmd
}

func (p habitsPage) submitForm() tea.Cmd {
	h, err := p.fd.habit()
	if err != nil {
		return func() tea.Msg { return errMsg{err} }
	}
	if p.editID != "" {
		return updateHabit(p.client, p.editID, h)
	}
	return createHabit(p.client, h)
}

// habit builds the request payload, rejecting values the field validators
// should have caught rather than silently zeroing them. Active carries the
// edited habit's state through: editing a paused habit must not reactivate it.
func (fd *habitForm) habit() (Habit, error) {
	mins, err := strconv.Atoi(strings.TrimSpace(fd.minutes))
	if err != nil {
		return Habit{}, fmt.Errorf("block minutes %q is not a number", fd.minutes)
	}
	count, err := strconv.Atoi(strings.TrimSpace(fd.perWeek))
	if err != nil {
		return Habit{}, fmt.Errorf("per-week count %q is not a number", fd.perWeek)
	}
	return Habit{
		Name:                 strings.TrimSpace(fd.name),
		Kind:                 fd.kind,
		BlockDurationMinutes: mins,
		Cadence:              Cadence{Type: "per_week", Count: count},
		Active:               fd.active,
	}, nil
}

func (p habitsPage) View() string {
	if p.form != nil {
		return p.form.View()
	}
	if len(p.list.Items()) == 0 {
		title := p.list.Styles.Title.Render(p.list.Title)
		return title + "\n\n" + faintStyle.Render("No habits yet — press a to add.")
	}
	return p.list.View()
}

func habitItems(habits []Habit) []list.Item {
	items := make([]list.Item, len(habits))
	for i, h := range habits {
		items[i] = habitItem{h}
	}
	return items
}

func newHabitForm(h *Habit, w, ht int) (*huh.Form, *habitForm, string) {
	fd := &habitForm{kind: "personal", minutes: "30", perWeek: "3", active: true}
	editID := ""
	if h != nil {
		editID = h.ID
		fd.name = h.Name
		fd.kind = h.Kind
		fd.minutes = strconv.Itoa(h.BlockDurationMinutes)
		fd.perWeek = strconv.Itoa(h.Cadence.Count)
		fd.active = h.Active
	}
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("Name").Value(&fd.name).Validate(huh.ValidateNotEmpty()),
		huh.NewSelect[string]().Title("Kind").Options(huh.NewOptions("work", "personal")...).Value(&fd.kind),
		huh.NewInput().Title("Block minutes").Value(&fd.minutes).Validate(validateInt),
		huh.NewInput().Title("Per-week count").Value(&fd.perWeek).Validate(validateInt),
		huh.NewConfirm().Title("Active").Affirmative("active").Negative("paused").Value(&fd.active),
	))
	if w > 0 {
		form = form.WithWidth(w).WithHeight(ht)
	}
	return form, fd, editID
}
