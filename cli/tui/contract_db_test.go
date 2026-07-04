package tui

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/icco/art/lib/api/handlers"
	"github.com/icco/art/lib/testdb"
)

// Drives the real client against the real handlers so client/server request
// contract drift (e.g. sending fields DisallowUnknownFields rejects) fails here.
func TestClientAgainstRealHandlers(t *testing.T) {
	db := testdb.Open(t)
	h := &handlers.Handlers{DB: db}
	r := chi.NewRouter()
	r.Post("/projects", h.ProjectsCreate)
	r.Patch("/projects/{id}", h.ProjectsUpdate)
	r.Post("/habits", h.HabitsCreate)
	r.Patch("/habits/{id}", h.HabitsUpdate)
	server := httptest.NewServer(r)
	defer server.Close()

	c := stubClient(server)
	ctx := context.Background()

	hb, err := c.CreateHabit(ctx, Habit{Name: "Walk", BlockDurationMinutes: 30, Cadence: Cadence{Type: "per_week", Count: 3}, Active: true})
	if err != nil {
		t.Fatalf("create habit: %v", err)
	}
	if _, err := c.UpdateHabit(ctx, hb.ID, Habit{Name: "Walk more", BlockDurationMinutes: 45, Cadence: Cadence{Type: "per_week", Count: 2}, Active: true}); err != nil {
		t.Fatalf("update habit: %v", err)
	}

	p, err := c.CreateProject(ctx, Project{Name: "Book", Kind: "work", TargetHours: 40})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := c.UpdateProject(ctx, p.ID, Project{Name: "Book", Kind: "work", TargetHours: 20}); err != nil {
		t.Fatalf("update project: %v", err)
	}
}
