package tui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type capturedReq struct {
	mu     sync.Mutex
	method string
	path   string
	body   []byte
}

func captureServer(t *testing.T, c *capturedReq, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		c.mu.Lock()
		c.method, c.path, c.body = r.Method, r.URL.Path, body
		c.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte("{}"))
	}))
}

func TestProjectSubmitFormCreate(t *testing.T) {
	var rec capturedReq
	server := captureServer(t, &rec, http.StatusCreated)
	defer server.Close()

	p := projectsPage{
		client: stubClient(server),
		fd:     &projectForm{name: "Book", kind: "work", hours: "40", deadline: "2026-08-01"},
	}
	msg := p.submitForm()()
	if _, ok := msg.(errMsg); ok {
		t.Fatalf("submit returned error: %v", msg)
	}
	if rec.method != http.MethodPost || rec.path != "/projects" {
		t.Fatalf("expected POST /projects, got %s %s", rec.method, rec.path)
	}
	var got Project
	if err := json.Unmarshal(rec.body, &got); err != nil {
		t.Fatalf("decode body: %v (%s)", err, rec.body)
	}
	if got.Name != "Book" || got.Kind != "work" || got.TargetHours != 40 || got.Deadline == nil {
		t.Fatalf("payload mismatch: %+v", got)
	}
}

func TestProjectSubmitFormEditPatches(t *testing.T) {
	var rec capturedReq
	server := captureServer(t, &rec, http.StatusOK)
	defer server.Close()

	p := projectsPage{
		client: stubClient(server),
		editID: "p1",
		fd:     &projectForm{name: "Book", kind: "personal", hours: "5"},
	}
	if _, ok := p.submitForm()().(errMsg); ok {
		t.Fatal("edit submit errored")
	}
	if rec.method != http.MethodPatch || rec.path != "/projects/p1" {
		t.Fatalf("expected PATCH /projects/p1, got %s %s", rec.method, rec.path)
	}
}

func TestHabitSubmitFormCreate(t *testing.T) {
	var rec capturedReq
	server := captureServer(t, &rec, http.StatusCreated)
	defer server.Close()

	p := habitsPage{
		client: stubClient(server),
		fd:     &habitForm{name: "Run", kind: "personal", minutes: "30", perWeek: "3"},
	}
	if _, ok := p.submitForm()().(errMsg); ok {
		t.Fatal("habit submit errored")
	}
	if rec.method != http.MethodPost || rec.path != "/habits" {
		t.Fatalf("expected POST /habits, got %s %s", rec.method, rec.path)
	}
	var got Habit
	if err := json.Unmarshal(rec.body, &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.body)
	}
	if got.Name != "Run" || got.BlockDurationMinutes != 30 || got.Cadence.Count != 3 {
		t.Fatalf("payload mismatch: %+v", got)
	}
}
