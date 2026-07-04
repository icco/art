package handlers_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/icco/art/lib/api/handlers"
	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/testdb"
)

// fakeJobs records enqueues and returns a canned job.
type fakeJobs struct {
	kinds   []models.JobKind
	running bool
}

func (f *fakeJobs) Enqueue(_ context.Context, kind models.JobKind) (models.Job, bool, error) {
	f.kinds = append(f.kinds, kind)
	return models.Job{Kind: kind, Status: models.JobPending, RunAt: time.Now()}, f.running, nil
}

func jobsRouter(h *handlers.Handlers) http.Handler {
	r := chi.NewRouter()
	r.Post("/sync", h.SyncRun)
	r.Post("/replan", h.ReplanRun)
	r.Post("/triage/run", h.TriageRun)
	r.Get("/jobs", h.JobsList)
	r.Get("/jobs/{id}", h.JobsGet)
	return r
}

func TestTriggerEndpointsEnqueue(t *testing.T) {
	f := &fakeJobs{}
	h := &handlers.Handlers{DB: testdb.Open(t), Jobs: f}
	r := jobsRouter(h)
	for path, kind := range map[string]models.JobKind{
		"/sync": models.JobSync, "/replan": models.JobPlanner, "/triage/run": models.JobTriage,
	} {
		w := do(t, r, "POST", path, nil)
		if w.Code != http.StatusAccepted {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
		if !strings.Contains(w.Body.String(), `"status":"queued"`) {
			t.Fatalf("%s: want queued status, got %s", path, w.Body)
		}
		found := false
		for _, k := range f.kinds {
			if k == kind {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: kind %s not enqueued (got %v)", path, kind, f.kinds)
		}
	}
}

func TestTriggerReportsRunning(t *testing.T) {
	f := &fakeJobs{running: true}
	h := &handlers.Handlers{DB: testdb.Open(t), Jobs: f}
	w := do(t, jobsRouter(h), "POST", "/replan", nil)
	if w.Code != http.StatusAccepted || !strings.Contains(w.Body.String(), `"status":"running"`) {
		t.Fatalf("want running status, got %d %s", w.Code, w.Body)
	}
}

func TestJobsListAndGet(t *testing.T) {
	db := testdb.Open(t)
	h := &handlers.Handlers{DB: db, Jobs: &fakeJobs{}}
	r := jobsRouter(h)
	job := models.Job{Kind: models.JobSync, Status: models.JobSucceeded, RunAt: time.Now(), MaxAttempts: 4}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}

	w := do(t, r, "GET", "/jobs?kind=sync&status=succeeded", nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), job.ID) {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	w = do(t, r, "GET", "/jobs?kind=bogus", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad kind: %d", w.Code)
	}
	w = do(t, r, "GET", "/jobs?status=bogus", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad status: %d", w.Code)
	}

	w = do(t, r, "GET", "/jobs/"+job.ID, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"kind":"sync"`) {
		t.Fatalf("get: %d %s", w.Code, w.Body)
	}
	missing := "00000000-0000-0000-0000-000000000000"
	w = do(t, r, "GET", "/jobs/"+missing, nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing job: %d %s", w.Code, w.Body)
	}
}
