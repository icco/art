package tui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// encodeJSON writes a fake server response, failing the test on error.
func encodeJSON(t *testing.T, w io.Writer, v any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("encode fake response: %v", err)
	}
}

// decodeRequest parses a fake server request body, failing the test on error.
func decodeRequest(t *testing.T, r io.Reader, v any) {
	t.Helper()
	if err := json.NewDecoder(r).Decode(v); err != nil {
		t.Errorf("decode request body: %v", err)
	}
}

// stubClient builds a Client pointed at server with a pre-cached fake token so
// idToken() doesn't try to shell out to gcloud.
func stubClient(server *httptest.Server) *Client {
	c := NewClient(Config{APIURL: server.URL})
	c.token = "test-token"
	c.tokenExp = time.Now().Add(time.Hour)
	return c
}

func TestClientListProjects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing auth header")
		}
		encodeJSON(t, w, []Project{{ID: "1", Name: "A"}})
	}))
	defer server.Close()
	got, err := stubClient(server).ListProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "A" {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestClientCreateProject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method: %s", r.Method)
		}
		w.WriteHeader(http.StatusCreated)
		encodeJSON(t, w, Project{ID: "2", Name: "B"})
	}))
	defer server.Close()
	got, err := stubClient(server).CreateProject(context.Background(), Project{Name: "B"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "2" {
		t.Fatalf("got %+v", got)
	}
}

func TestClientDeleteProject(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	if err := stubClient(server).DeleteProject(context.Background(), "abc"); err != nil {
		t.Fatal(err)
	}
}

func TestClientDeleteSession(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.Method + " " + r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	if err := stubClient(server).DeleteSession(context.Background(), "sess-1"); err != nil {
		t.Fatal(err)
	}
	if gotPath != "DELETE /sessions/sess-1" {
		t.Fatalf("got %q, want DELETE /sessions/sess-1", gotPath)
	}
}

func TestClientHabits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "POST":
			w.WriteHeader(http.StatusCreated)
			encodeJSON(t, w, Habit{ID: "h1", Name: "Walk"})
		case "GET":
			encodeJSON(t, w, []Habit{{ID: "h1"}})
		case "DELETE":
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer server.Close()
	c := stubClient(server)
	ctx := context.Background()
	if _, err := c.CreateHabit(ctx, Habit{Name: "Walk"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListHabits(ctx); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteHabit(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
}

func TestClientEventsReplanSync(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/events":
			encodeJSON(t, w, []Event{{ID: "e1"}})
		case "/replan":
			encodeJSON(t, w, map[string]string{"status": "started"})
		case "/sync":
			encodeJSON(t, w, map[string]any{
				"status": "queued",
				"job":    Job{ID: "j1", Kind: "sync", Status: "pending"},
			})
		case "/jobs/j1":
			encodeJSON(t, w, Job{ID: "j1", Kind: "sync", Status: "succeeded"})
		}
	}))
	defer server.Close()
	c := stubClient(server)
	ctx := context.Background()
	if _, err := c.ListEvents(ctx, time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	status, err := c.Replan(ctx)
	if err != nil || status != "started" {
		t.Fatalf("replan: %q %v", status, err)
	}
	job, err := c.Sync(ctx)
	if err != nil || job.ID != "j1" {
		t.Fatalf("sync: job=%+v err=%v", job, err)
	}
	got, err := c.GetJob(ctx, job.ID)
	if err != nil || got.Status != "succeeded" {
		t.Fatalf("get job: job=%+v err=%v", got, err)
	}
}

func TestClientListEventsPrimaryOnly(t *testing.T) {
	var gotCalendar string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCalendar = r.URL.Query().Get("calendar")
		encodeJSON(t, w, []Event{{ID: "e1"}})
	}))
	defer server.Close()
	if _, err := stubClient(server).ListEvents(context.Background(), time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if gotCalendar != "primary" {
		t.Fatalf("calendar param = %q, want primary", gotCalendar)
	}
}

func TestClientSetEmailArchived(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		decodeRequest(t, r.Body, &gotBody)
		encodeJSON(t, w, Email{ID: "e1", Archived: true})
	}))
	defer server.Close()

	got, err := stubClient(server).SetEmailArchived(context.Background(), "e1", true)
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != "POST" || gotPath != "/emails/e1/archive" {
		t.Fatalf("request = %s %s, want POST /emails/e1/archive", gotMethod, gotPath)
	}
	if gotBody["archived"] != true {
		t.Errorf("body archived = %v, want true", gotBody["archived"])
	}
	if !got.Archived {
		t.Errorf("returned email not archived: %+v", got)
	}
}

// The server decodes mutations with DisallowUnknownFields, so request bodies
// must not carry server-computed fields like id or scheduled_hours.
func TestClientMutationBodiesMatchServerContract(t *testing.T) {
	allowedProject := map[string]bool{
		"name": true, "description": true, "kind": true,
		"target_hours": true, "deadline": true, "status": true,
	}
	allowedHabit := map[string]bool{
		"name": true, "description": true, "kind": true,
		"block_duration_minutes": true, "cadence": true, "active": true,
	}
	cases := []struct {
		name    string
		allowed map[string]bool
		call    func(*Client) error
	}{
		{"create project", allowedProject, func(c *Client) error {
			_, err := c.CreateProject(context.Background(), Project{Name: "B", Kind: "work", TargetHours: 1})
			return err
		}},
		{"update project", allowedProject, func(c *Client) error {
			_, err := c.UpdateProject(context.Background(), "p1", Project{Name: "B", Kind: "work", TargetHours: 1})
			return err
		}},
		{"create habit", allowedHabit, func(c *Client) error {
			_, err := c.CreateHabit(context.Background(), Habit{Name: "Walk", BlockDurationMinutes: 30, Cadence: Cadence{Type: "per_week", Count: 3}})
			return err
		}},
		{"update habit", allowedHabit, func(c *Client) error {
			_, err := c.UpdateHabit(context.Background(), "h1", Habit{Name: "Walk", BlockDurationMinutes: 30, Cadence: Cadence{Type: "per_week", Count: 3}})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rec capturedReq
			server := captureServer(t, &rec, http.StatusOK)
			defer server.Close()
			if err := tc.call(stubClient(server)); err != nil {
				t.Fatal(err)
			}
			var body map[string]any
			if err := json.Unmarshal(rec.body, &body); err != nil {
				t.Fatalf("decode body: %v (%s)", err, rec.body)
			}
			for k := range body {
				if !tc.allowed[k] {
					t.Errorf("request body contains field %q the server rejects", k)
				}
			}
		})
	}
}

func TestClientErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusBadRequest)
	}))
	defer server.Close()
	_, err := stubClient(server).ListProjects(context.Background())
	if err == nil {
		t.Fatal("expected error on 400 response")
	}
}
