package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// rootOnSettingsForm navigates a fresh root to settings and opens the edit form.
func rootOnSettingsForm(t *testing.T) rootModel {
	t.Helper()
	root := newRootWithClient(Config{}, nil, false)
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(navigateMsg{to: pageSettings})
	m, _ = m.Update(settingsMsg{settings: Settings{TriageBackfillDays: 7}})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'e'})
	return m.(rootModel)
}

// TestFooterHidesActionsWhileFormOpen guards the form glitch: the footer must
// not advertise page-action keys while a form is capturing keystrokes, since
// pressing them types into the form instead.
func TestFooterHidesActionsWhileFormOpen(t *testing.T) {
	rm := rootOnSettingsForm(t)
	if !rm.pages[rm.current()].FullInput() {
		t.Fatal("edit form should be open (FullInput)")
	}
	got := rm.View().Content
	for _, bad := range []string{"e edit", "t triage"} {
		if strings.Contains(got, bad) {
			t.Errorf("footer must not advertise %q while a form captures input:\n%s", bad, got)
		}
	}
}
