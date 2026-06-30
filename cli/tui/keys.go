package tui

import "charm.land/bubbles/v2/key"

// keyMap holds every global key binding. Pages may define their own local
// bindings (e.g. list navigation) on top of these.
type keyMap struct {
	Dashboard key.Binding
	Calendar  key.Binding
	Projects  key.Binding
	Habits    key.Binding
	Digest    key.Binding

	PrevWeek key.Binding
	NextWeek key.Binding

	Add    key.Binding
	Edit   key.Binding
	Delete key.Binding

	Replan  key.Binding
	Sync    key.Binding
	Triage  key.Binding
	Reject  key.Binding
	Archive key.Binding

	Back key.Binding
	Help key.Binding
	Quit key.Binding
}

func defaultKeyMap() keyMap {
	return keyMap{
		Dashboard: key.NewBinding(key.WithKeys("1"), key.WithHelp("1", "dashboard")),
		Calendar:  key.NewBinding(key.WithKeys("2"), key.WithHelp("2", "calendar")),
		Projects:  key.NewBinding(key.WithKeys("3"), key.WithHelp("3", "projects")),
		Habits:    key.NewBinding(key.WithKeys("4"), key.WithHelp("4", "habits")),
		Digest:    key.NewBinding(key.WithKeys("5"), key.WithHelp("5", "digest")),

		PrevWeek: key.NewBinding(key.WithKeys("left", "h"), key.WithHelp("←/h", "prev week")),
		NextWeek: key.NewBinding(key.WithKeys("right", "l"), key.WithHelp("→/l", "next week")),

		Add:    key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "add")),
		Edit:   key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit")),
		Delete: key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),

		Replan:  key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "replan")),
		Sync:    key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "sync")),
		Triage:  key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "triage")),
		Reject:  key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "mark bad")),
		Archive: key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "archive ↔ inbox")),

		Back: key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
		Help: key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit: key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
	}
}

// pageHelp adapts the global navigation keys plus the current page's own action
// keys into a help.KeyMap, so the footer and full help show what the active
// page can actually do instead of a fixed global list.
type pageHelp struct {
	keys keyMap
	page []key.Binding
}

// ShortHelp implements help.KeyMap: the page's actions, then help and quit.
func (h pageHelp) ShortHelp() []key.Binding {
	out := append([]key.Binding{}, h.page...)
	return append(out, h.keys.Help, h.keys.Quit)
}

// FullHelp implements help.KeyMap: navigation, the page's actions, then the
// global run/back/help/quit keys.
func (h pageHelp) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{h.keys.Dashboard, h.keys.Calendar, h.keys.Projects, h.keys.Habits, h.keys.Digest},
		h.page,
		{h.keys.Replan, h.keys.Sync, h.keys.Triage, h.keys.Back, h.keys.Help, h.keys.Quit},
	}
}
