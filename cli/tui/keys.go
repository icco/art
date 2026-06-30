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

// ShortHelp implements help.KeyMap.
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Dashboard, k.Calendar, k.Projects, k.Habits, k.Digest, k.Help, k.Quit}
}

// FullHelp implements help.KeyMap.
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Dashboard, k.Calendar, k.Projects, k.Habits, k.Digest},
		{k.PrevWeek, k.NextWeek, k.Add, k.Edit, k.Delete, k.Reject},
		{k.Replan, k.Sync, k.Triage, k.Back, k.Help, k.Quit},
	}
}
