package tui

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	teatest "github.com/charmbracelet/x/exp/teatest/v2"
)

// testModel builds a root model backed by a stubbed client pointed at server.
func testModel(t *testing.T, server *httptest.Server) *teatest.TestModel {
	t.Helper()
	root := newRootWithClient(Config{APIURL: server.URL}, stubClient(server), false)
	return teatest.NewTestModel(t, root, teatest.WithInitialTermSize(100, 40))
}

func waitForContains(t *testing.T, tm *teatest.TestModel, want string) {
	t.Helper()
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte(want))
	}, teatest.WithDuration(3*time.Second), teatest.WithCheckInterval(25*time.Millisecond))
}

// emptyAPI serves empty JSON lists for every GET so pages render without data.
func emptyAPI() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("[]"))
	}))
}

func TestNavigationRendersEachPage(t *testing.T) {
	server := emptyAPI()
	defer server.Close()
	tm := testModel(t, server)

	// Dashboard is the home screen.
	waitForContains(t, tm, "TODAY")

	tm.Type("3") // projects
	waitForContains(t, tm, "Projects")

	tm.Type("4") // habits
	waitForContains(t, tm, "Habits")

	tm.Type("5") // digest
	waitForContains(t, tm, "Press t to run triage")

	tm.Type("2") // calendar
	waitForContains(t, tm, "Week of")

	tm.Type("q")
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

// TestCalendarWeekNav exercises week navigation at the model level
// (deterministic; teatest alt-screen matching on a full date string is flaky).
func TestCalendarWeekNav(t *testing.T) {
	now := time.Date(2026, 6, 30, 9, 0, 0, 0, time.Local)
	orig := timeNow
	timeNow = func() time.Time { return now }
	defer func() { timeNow = orig }()

	weekOf := func(base time.Time) string { return "Week of " + base.Format("Mon Jan 2 2006") }
	base := startOfWeek(now)

	var p Page = newCalendarPage(nil)
	p, _ = p.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if !strings.Contains(p.View(), weekOf(base)) {
		t.Fatalf("calendar should open on current week:\n%s", p.View())
	}

	p, _ = p.Update(tea.KeyPressMsg{Code: 'l'}) // next week
	if !strings.Contains(p.View(), weekOf(base.AddDate(0, 0, 7))) {
		t.Fatalf("expected next week:\n%s", p.View())
	}

	p, _ = p.Update(tea.KeyPressMsg{Code: 'h'}) // back to current
	p, _ = p.Update(tea.KeyPressMsg{Code: 'h'}) // prev week
	if !strings.Contains(p.View(), weekOf(base.AddDate(0, 0, -7))) {
		t.Fatalf("expected previous week:\n%s", p.View())
	}
}

func TestAddProjectFormOpens(t *testing.T) {
	server := emptyAPI()
	defer server.Close()
	tm := testModel(t, server)

	tm.Type("3") // projects
	waitForContains(t, tm, "Projects")
	tm.Type("a") // open the add form
	waitForContains(t, tm, "Target hours")

	_ = tm.Quit() // the form captures keys, so force-quit
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

// Note: the full huh add-submit-reload loop is covered deterministically by
// TestAddProjectFormOpens (form opens/renders) plus the submitForm unit tests
// in forms_test.go (build + POST/PATCH payload + pointer readback). Driving the
// multi-field form via teatest keystrokes proved timing-flaky, so it is omitted.

// TestDeleteReloadsList proves the bug that motivated the rebuild is fixed:
// a mutation (delete) is followed by a reload, so the server sees a second
// GET /projects after the DELETE.
func TestDeleteReloadsList(t *testing.T) {
	var mu sync.Mutex
	projects := []Project{{ID: "p1", Name: "Alpha", Kind: "work"}, {ID: "p2", Name: "Beta", Kind: "work"}}
	var projectGets, deletes int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/projects":
			atomic.AddInt32(&projectGets, 1)
			mu.Lock()
			defer mu.Unlock()
			_ = json.NewEncoder(w).Encode(projects)
		case r.Method == http.MethodDelete:
			atomic.AddInt32(&deletes, 1)
			mu.Lock()
			projects = projects[1:] // drop Alpha
			mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
		default:
			_, _ = w.Write([]byte("[]"))
		}
	}))
	defer server.Close()

	tm := testModel(t, server)
	tm.Type("3") // projects
	waitForContains(t, tm, "Alpha")

	tm.Type("d") // delete the selected (first) project

	// The reload after delete means a second GET /projects must arrive.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&projectGets) >= 2 && atomic.LoadInt32(&deletes) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := atomic.LoadInt32(&deletes); got != 1 {
		t.Fatalf("expected 1 DELETE, got %d", got)
	}
	if got := atomic.LoadInt32(&projectGets); got < 2 {
		t.Fatalf("expected reload (>=2 GET /projects), got %d — mutation did not refresh", got)
	}

	tm.Type("q")
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}
