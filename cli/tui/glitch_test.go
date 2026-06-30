package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// rootOnProjectsAddForm navigates a fresh root to projects and opens the add form.
func rootOnProjectsAddForm(t *testing.T) rootModel {
	t.Helper()
	root := newRootWithClient(Config{}, nil, false)
	var m tea.Model = root
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(navigateMsg{to: pageProjects})
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(tea.KeyPressMsg{Code: 'a'})
	return m.(rootModel)
}

// TestFooterHidesActionsWhileFormOpen guards the form glitch: the footer must
// not advertise list-action keys while a form is capturing keystrokes, since
// pressing them types into the form instead.
func TestFooterHidesActionsWhileFormOpen(t *testing.T) {
	rm := rootOnProjectsAddForm(t)
	if !rm.pages[rm.current()].FullInput() {
		t.Fatal("add form should be open (FullInput)")
	}
	got := rm.View().Content
	for _, bad := range []string{"a add", "e edit", "d delete"} {
		if strings.Contains(got, bad) {
			t.Errorf("footer must not advertise %q while a form captures input:\n%s", bad, got)
		}
	}
}

// TestProjectsEmptyStateIsClean guards the list glitch: an empty list shows a
// single hint, not the bubbles default "No items" stacked with the hint.
func TestProjectsEmptyStateIsClean(t *testing.T) {
	var p Page = newProjectsPage(nil, false)
	p, _ = p.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	p, _ = p.Update(projectsMsg{nil})
	got := p.View()
	if strings.Contains(got, "No items") {
		t.Errorf("empty state should show only the hint, not the list default:\n%s", got)
	}
	if !strings.Contains(got, "press a to add") {
		t.Errorf("empty state missing add hint:\n%s", got)
	}
}
