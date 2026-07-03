package handlers_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/icco/art/lib/api/handlers"
	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/testdb"
)

type fakePlanner struct{ called chan struct{} }

func (f *fakePlanner) Run(context.Context) error {
	select {
	case f.called <- struct{}{}:
	default:
	}
	return nil
}

type panickyPlanner struct{ fakePlanner }

func (p *panickyPlanner) Run(context.Context) error {
	select {
	case p.called <- struct{}{}:
	default:
	}
	panic("planner kaboom")
}

func replanRouter(h *handlers.Handlers) http.Handler {
	r := chi.NewRouter()
	r.Post("/replan", h.ReplanRun)
	return r
}

func TestReplanRunDetaches(t *testing.T) {
	db := testdb.Open(t)
	fp := &fakePlanner{called: make(chan struct{}, 1)}
	h := &handlers.Handlers{DB: db, Planner: fp}
	r := replanRouter(h)

	w := do(t, r, "POST", "/replan", nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("replan: %d %s", w.Code, w.Body)
	}
	select {
	case <-fp.called:
	case <-time.After(2 * time.Second):
		t.Fatal("detached planner run was not invoked")
	}

	// Guard: a recent running planner run makes a second trigger a no-op.
	if err := db.Create(&models.AgentRun{
		Kind: models.AgentRunPlanner, Status: models.AgentRunRunning, StartedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	w = do(t, r, "POST", "/replan", nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("guard: %d %s", w.Code, w.Body)
	}
	select {
	case <-fp.called:
		t.Fatal("second trigger should not start another pass while one runs")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestReplanRunRecoversPanic(t *testing.T) {
	db := testdb.Open(t)
	pp := &panickyPlanner{fakePlanner{called: make(chan struct{}, 1)}}
	h := &handlers.Handlers{DB: db, Planner: pp}
	r := replanRouter(h)

	w := do(t, r, "POST", "/replan", nil)
	if w.Code != http.StatusAccepted {
		t.Fatalf("replan: %d %s", w.Code, w.Body)
	}
	select {
	case <-pp.called:
	case <-time.After(2 * time.Second):
		t.Fatal("detached planner run was not invoked")
	}
	// Let the goroutine unwind; without recovery this kills the test binary.
	time.Sleep(50 * time.Millisecond)
}
