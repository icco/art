package tui

import (
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func newTestApp() *App {
	return &App{
		cfg:        Config{APIURL: "http://localhost:8080", Audience: "x"},
		client:     NewClient(Config{APIURL: "http://localhost:8080", Audience: "x"}),
		screen:     screenWeek,
		weekAnchor: startOfWeekLocal(time.Now()),
	}
}

func TestTabSwitching(t *testing.T) {
	a := newTestApp()
	cases := []struct {
		key  string
		want screen
	}{
		{"1", screenWeek},
		{"2", screenProjects},
		{"3", screenHabits},
	}
	for _, c := range cases {
		_, _ = a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(c.key)})
		if a.screen != c.want {
			t.Errorf("key %s: got screen %v want %v", c.key, a.screen, c.want)
		}
	}
}

func TestCursorMovement(t *testing.T) {
	a := newTestApp()
	a.screen = screenProjects
	a.projects = []Project{{ID: "1"}, {ID: "2"}, {ID: "3"}}
	for range 2 {
		_, _ = a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
	}
	if a.projCursor != 2 {
		t.Fatalf("projCursor: %d", a.projCursor)
	}
	_, _ = a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	if a.projCursor != 1 {
		t.Fatalf("projCursor after up: %d", a.projCursor)
	}
}

func TestAddOpensForm(t *testing.T) {
	a := newTestApp()
	a.screen = screenProjects
	_, _ = a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if a.screen != screenAddProject {
		t.Fatalf("expected screenAddProject, got %v", a.screen)
	}
	if a.form.kind != formKindProject || len(a.form.fields) == 0 {
		t.Fatalf("form not initialized: %+v", a.form)
	}

	a = newTestApp()
	a.screen = screenHabits
	_, _ = a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if a.screen != screenAddHabit || a.form.kind != formKindHabit {
		t.Fatalf("expected habit form, got screen %v kind %s", a.screen, a.form.kind)
	}
}

func TestFormEscapes(t *testing.T) {
	a := newTestApp()
	a.screen = screenAddProject
	a.form = formState{kind: formKindProject, fields: []formField{{label: "name"}}}
	_, _ = a.handleKey(tea.KeyMsg{Type: tea.KeyEsc})
	if a.screen != screenProjects {
		t.Fatalf("esc should return to projects, got %v", a.screen)
	}
}

func TestFormTypingAndBackspace(t *testing.T) {
	a := newTestApp()
	a.screen = screenAddProject
	a.form = formState{kind: formKindProject, fields: []formField{{label: "name"}}}
	_, _ = a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	_, _ = a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	if a.form.fields[0].value != "ab" {
		t.Fatalf("typed value: %q", a.form.fields[0].value)
	}
	_, _ = a.handleKey(tea.KeyMsg{Type: tea.KeyBackspace})
	if a.form.fields[0].value != "a" {
		t.Fatalf("after backspace: %q", a.form.fields[0].value)
	}
}

func TestWeekNav(t *testing.T) {
	a := newTestApp()
	start := a.weekAnchor
	_, _ = a.handleKey(tea.KeyMsg{Type: tea.KeyRight})
	if !a.weekAnchor.Equal(start.AddDate(0, 0, 7)) {
		t.Fatalf("right arrow didn't advance")
	}
	_, _ = a.handleKey(tea.KeyMsg{Type: tea.KeyLeft})
	if !a.weekAnchor.Equal(start) {
		t.Fatalf("left arrow didn't go back")
	}
}

func TestUpdateMessages(t *testing.T) {
	a := newTestApp()
	_, _ = a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if a.width != 80 || a.height != 24 {
		t.Fatalf("window size not stored")
	}
	_, _ = a.Update(eventsLoadedMsg{{ID: "e1"}})
	if len(a.events) != 1 {
		t.Fatalf("events not stored")
	}
	_, _ = a.Update(projectsLoadedMsg{{ID: "p1"}})
	if len(a.projects) != 1 {
		t.Fatalf("projects not stored")
	}
	_, _ = a.Update(habitsLoadedMsg{{ID: "h1"}})
	if len(a.habits) != 1 {
		t.Fatalf("habits not stored")
	}
	_, _ = a.Update(statusMsg("hello"))
	if a.status != "hello" {
		t.Fatalf("status: %q", a.status)
	}
}

func TestView(t *testing.T) {
	a := newTestApp()
	a.screen = screenWeek
	if a.View() == "" {
		t.Fatal("View should not be empty")
	}
	a.screen = screenProjects
	if a.View() == "" {
		t.Fatal("View should not be empty")
	}
	a.screen = screenHabits
	if a.View() == "" {
		t.Fatal("View should not be empty")
	}
}
