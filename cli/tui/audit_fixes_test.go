package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// deadClient builds a Client for a URL nothing listens on: requests fail
// fast, and the fake token keeps idToken() from shelling out to gcloud.
func deadClient() *Client {
	c := NewClient(Config{APIURL: "http://127.0.0.1:1"})
	c.token = "test-token"
	c.tokenExp = time.Now().Add(time.Hour)
	return c
}

// execCmds runs a command tree (unwrapping batches) and returns the messages.
func execCmds(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, execCmds(c)...)
		}
		return out
	}
	return []tea.Msg{msg}
}

// Root broadcasts real resizes to every page, so navigation must not emit
// synthetic WindowSizeMsgs: each one re-enters Update as authoritative and
// shrinks the UI by chromeHeight again.
func TestNavigationDoesNotEmitResize(t *testing.T) {
	m := newRootWithClient(Config{}, deadClient(), true)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = mm.(rootModel)

	_, cmd := m.navigate(pageCalendar)
	for _, msg := range execCmds(cmd) {
		if sz, ok := msg.(tea.WindowSizeMsg); ok {
			t.Fatalf("navigate emitted synthetic resize %+v", sz)
		}
	}

	m.stack = []pageID{pageDashboard, pageCalendar}
	_, cmd = m.pop()
	for _, msg := range execCmds(cmd) {
		if sz, ok := msg.(tea.WindowSizeMsg); ok {
			t.Fatalf("pop emitted synthetic resize %+v", sz)
		}
	}
}

// r/s/t while an agent action is in flight must not stack another run.
func TestBusyGuardsAgentKeys(t *testing.T) {
	m := newRootWithClient(Config{}, deadClient(), true)
	m.busy = true
	for _, code := range []rune{'r', 's', 't'} {
		_, cmd := m.handleKey(tea.KeyPressMsg{Code: code})
		if cmd != nil {
			t.Errorf("key %q while busy should be a no-op", code)
		}
	}
}

// huh's only abort binding is ctrl+c, which root intercepts to quit: esc must
// close the form, or there is no way out except submitting it.
func TestProjectFormEscCancels(t *testing.T) {
	p := newProjectsPage(deadClient(), true)
	page, _ := p.Update(tea.KeyPressMsg{Code: 'a'})
	if !page.FullInput() {
		t.Fatal("form did not open")
	}
	page, _ = page.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if page.FullInput() {
		t.Fatal("esc did not cancel the form")
	}
}

func TestHabitFormEscCancels(t *testing.T) {
	p := newHabitsPage(deadClient(), true)
	page, _ := p.Update(tea.KeyPressMsg{Code: 'a'})
	if !page.FullInput() {
		t.Fatal("form did not open")
	}
	page, _ = page.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if page.FullInput() {
		t.Fatal("esc did not cancel the form")
	}
}

func TestDigestConfirmEscCancels(t *testing.T) {
	p := newDigestPage(deadClient(), true)
	pg, _ := p.Update(emailsMsg{emails: []Email{{ID: "e1", Subject: "s", Action: "archive"}}})
	pg, _ = pg.Update(tea.KeyPressMsg{Code: 'x'})
	if !pg.FullInput() {
		t.Fatal("confirm did not open")
	}
	pg, _ = pg.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if pg.FullInput() {
		t.Fatal("esc did not cancel the confirm")
	}
}

// Editing a paused habit must not silently reactivate it, and non-numeric
// input must not silently become zero.
func TestHabitFormPayload(t *testing.T) {
	_, fd, _ := newHabitForm(&Habit{Active: false, Kind: "personal", BlockDurationMinutes: 30, Cadence: Cadence{Type: "per_week", Count: 3}}, 0, 0)
	h, err := fd.habit()
	if err != nil {
		t.Fatal(err)
	}
	if h.Active {
		t.Error("editing a paused habit reactivated it")
	}
	fd.minutes = "abc"
	if _, err := fd.habit(); err == nil {
		t.Error("non-numeric minutes should error, not become 0")
	}
	fd.minutes, fd.perWeek = "30", "x"
	if _, err := fd.habit(); err == nil {
		t.Error("non-numeric per-week count should error, not become 0")
	}
}

func TestProjectFormPayload(t *testing.T) {
	_, fd, _ := newProjectForm(nil, 0, 0)
	fd.name, fd.hours, fd.deadline = "P", "nope", ""
	if _, err := fd.project(); err == nil {
		t.Error("non-numeric hours should error, not become 0")
	}
	fd.hours, fd.deadline = "4", "not-a-date"
	if _, err := fd.project(); err == nil {
		t.Error("unparseable deadline should error, not be dropped")
	}
	fd.deadline = "2026-08-01"
	pr, err := fd.project()
	if err != nil || pr.Deadline == nil || pr.TargetHours != 4 {
		t.Fatalf("valid form rejected: %+v %v", pr, err)
	}
}

// A refresh for the current week must not clobber a calendar showing a
// different week.
func TestCalendarIgnoresStaleWindow(t *testing.T) {
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.Local)
	orig := timeNow
	timeNow = func() time.Time { return now }
	defer func() { timeNow = orig }()

	p := newCalendarPage(deadClient())
	pg, _ := p.Update(tea.KeyPressMsg{Code: 'l'}) // next week
	cal := pg.(calendarPage)

	thisWeek := startOfWeek(now)
	stale := eventsMsg{
		events: []Event{{Summary: "old week"}},
		from:   thisWeek, to: thisWeek.AddDate(0, 0, 7),
	}
	pg, _ = cal.Update(stale)
	if got := pg.(calendarPage).events; len(got) != 0 {
		t.Fatalf("stale window applied: %+v", got)
	}

	fresh := eventsMsg{
		events: []Event{{Summary: "next week"}},
		from:   cal.anchor.AddDate(0, 0, -1), to: cal.anchor.AddDate(0, 0, 8),
	}
	pg, _ = pg.Update(fresh)
	if got := pg.(calendarPage).events; len(got) != 1 {
		t.Fatalf("covering window ignored: %+v", got)
	}
}

func TestDashboardStopsLoadingOnError(t *testing.T) {
	p := newDashboardPage(deadClient())
	pg, _ := p.Update(errMsg{errors.New("boom")})
	if view := pg.(dashboardPage).renderToday(); strings.Contains(view, "loading") {
		t.Fatalf("dashboard stuck on loading after error: %q", view)
	}
}

func TestLoadConfigTrimsTrailingSlash(t *testing.T) {
	t.Setenv("ART_API_URL", "https://art.example.com/")
	cfg, err := LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIURL != "https://art.example.com" {
		t.Fatalf("APIURL = %q", cfg.APIURL)
	}
}

// The run to await depends on what the server said: "started" means a new run
// distinct from whatever was latest; "running" means the in-flight run itself.
func TestPollBaseline(t *testing.T) {
	running := &AgentRun{ID: "r1", Status: "running"}
	done := &AgentRun{ID: "r0", Status: "succeeded"}
	cases := []struct {
		name   string
		latest *AgentRun
		status string
		want   string
	}{
		{"no runs", nil, "started", ""},
		{"started, prior finished", done, "started", "r0"},
		{"started, prior running", running, "started", "r1"},
		{"already running", running, "running", ""},
		{"already running, latest finished race", done, "running", "r0"},
	}
	for _, c := range cases {
		if got := pollBaseline(c.latest, c.status); got != c.want {
			t.Errorf("%s: baseline=%q want %q", c.name, got, c.want)
		}
	}
}
