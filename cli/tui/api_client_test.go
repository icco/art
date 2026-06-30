package tui

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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
		_ = json.NewEncoder(w).Encode([]Project{{ID: "1", Name: "A"}})
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
		_ = json.NewEncoder(w).Encode(Project{ID: "2", Name: "B"})
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

func TestClientHabits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "POST":
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(Habit{ID: "h1", Name: "Walk"})
		case "GET":
			_ = json.NewEncoder(w).Encode([]Habit{{ID: "h1"}})
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
			_ = json.NewEncoder(w).Encode([]Event{{ID: "e1"}})
		case "/replan":
			_ = json.NewEncoder(w).Encode(AgentRun{Status: "succeeded"})
		case "/sync":
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()
	c := stubClient(server)
	ctx := context.Background()
	if _, err := c.ListEvents(ctx, time.Now(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	res, err := c.Replan(ctx)
	if err != nil || res.Status != "succeeded" {
		t.Fatalf("replan: %+v %v", res, err)
	}
	if err := c.Sync(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestClientListEventsPrimaryOnly(t *testing.T) {
	var gotCalendar string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCalendar = r.URL.Query().Get("calendar")
		_ = json.NewEncoder(w).Encode([]Event{{ID: "e1"}})
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
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		_ = json.NewEncoder(w).Encode(Email{ID: "e1", Archived: true})
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
