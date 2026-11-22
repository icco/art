package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// deadClient points at a closed port so requests fail fast without gcloud.
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

// A synthetic WindowSizeMsg re-enters Update and shrinks the UI by chromeHeight.
func TestNavigationDoesNotEmitResize(t *testing.T) {
	m := newRootWithClient(Config{}, deadClient(), true)
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m = mm.(rootModel)

	_, cmd := m.navigate(pageDigest)
	for _, msg := range execCmds(cmd) {
		if sz, ok := msg.(tea.WindowSizeMsg); ok {
			t.Fatalf("navigate emitted synthetic resize %+v", sz)
		}
	}

	m.stack = []pageID{pageDashboard, pageDigest}
	_, cmd = m.pop()
	for _, msg := range execCmds(cmd) {
		if sz, ok := msg.(tea.WindowSizeMsg); ok {
			t.Fatalf("pop emitted synthetic resize %+v", sz)
		}
	}
}

func TestBusyGuardsAgentKeys(t *testing.T) {
	m := newRootWithClient(Config{}, deadClient(), true)
	m.busy = true
	for _, code := range []rune{'t'} {
		_, cmd := m.handleKey(tea.KeyPressMsg{Code: code})
		if cmd != nil {
			t.Errorf("key %q while busy should be a no-op", code)
		}
	}
}

// huh's only abort binding is ctrl+c, which root intercepts to quit the app.
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

func TestTriageKeyClearsStaleError(t *testing.T) {
	m := newRootWithClient(Config{}, deadClient(), true)
	m.err = errAPIStatus
	mm, _ := m.handleKey(tea.KeyPressMsg{Code: 't'})
	res := mm.(rootModel)
	if res.err != nil {
		t.Fatalf("expected err to be cleared, got: %v", res.err)
	}
	if !res.busy || res.status != "triaging…" {
		t.Fatalf("expected busy and status triaging…, got busy=%v status=%q", res.busy, res.status)
	}
}

func TestNavigateDashboardResetsStack(t *testing.T) {
	m := newRootWithClient(Config{}, deadClient(), true)
	mm, _ := m.navigate(pageSettings)
	m = mm.(rootModel)
	mm, _ = m.navigate(pageDigest)
	m = mm.(rootModel)
	mm, _ = m.navigate(pageDashboard)
	m = mm.(rootModel)
	if len(m.stack) != 1 || m.stack[0] != pageDashboard {
		t.Fatalf("expected stack [pageDashboard], got %+v", m.stack)
	}
}

func TestPopReturnsDestinationInit(t *testing.T) {
	m := newRootWithClient(Config{}, deadClient(), true)
	m.stack = []pageID{pageDashboard, pageSettings}
	mm, cmd := m.pop()
	m = mm.(rootModel)
	if m.current() != pageDashboard {
		t.Fatalf("expected current page pageDashboard, got %v", m.current())
	}
	if cmd == nil {
		t.Fatal("expected non-nil cmd from pop to refresh destination page")
	}
}
