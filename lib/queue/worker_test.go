package queue

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/testdb"
)

type fakeServices struct {
	order      []string
	syncErrs   map[string]string
	syncErr    error
	plannerErr error
	panicKind  models.JobKind
}

func (f *fakeServices) RunAll(context.Context) (map[string]string, error) {
	f.order = append(f.order, "sync")
	if f.panicKind == models.JobSync {
		panic("sync kaboom")
	}
	return f.syncErrs, f.syncErr
}

func (f *fakeServices) Run(context.Context) error {
	f.order = append(f.order, "planner")
	if f.panicKind == models.JobPlanner {
		panic("planner kaboom")
	}
	return f.plannerErr
}

// reconcileFake records the reconcile pass in the shared order slice.
type reconcileFake struct{ f *fakeServices }

func (r reconcileFake) Run(context.Context) error {
	r.f.order = append(r.f.order, "reconcile")
	if r.f.panicKind == models.JobReconcile {
		panic("reconcile kaboom")
	}
	return nil
}

// triageFake separates triage's RunAll signature from sync's.
type triageFake struct{ f *fakeServices }

func (t triageFake) RunAll(context.Context) error {
	t.f.order = append(t.f.order, "triage")
	if t.f.panicKind == models.JobTriage {
		panic("triage kaboom")
	}
	return nil
}

func testWorker(t *testing.T, f *fakeServices) *Worker {
	t.Helper()
	return New(testdb.Open(t), f, reconcileFake{f}, f, triageFake{f})
}

func TestDrainRunsDueJobsInOrder(t *testing.T) {
	f := &fakeServices{}
	w := testWorker(t, f)
	ctx := context.Background()
	if err := w.Queue.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	w.drain(ctx)
	want := []string{"sync", "reconcile", "planner", "triage"}
	if len(f.order) != len(want) {
		t.Fatalf("want %v, got %v", want, f.order)
	}
	for i := range want {
		if f.order[i] != want[i] {
			t.Fatalf("want %v, got %v", want, f.order)
		}
	}
	var pending, succeeded int64
	w.Queue.DB.Model(&models.Job{}).Where("status = ?", models.JobPending).Count(&pending)
	w.Queue.DB.Model(&models.Job{}).Where("status = ?", models.JobSucceeded).Count(&succeeded)
	if pending != 4 || succeeded != 4 {
		t.Fatalf("want 4 succeeded + 4 chained pending, got %d/%d", succeeded, pending)
	}
}

func TestDrainRecordsFailureForRetry(t *testing.T) {
	f := &fakeServices{plannerErr: errors.New("vertex down")}
	w := testWorker(t, f)
	ctx := context.Background()
	if err := w.Queue.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	w.drain(ctx)
	var job models.Job
	if err := w.Queue.DB.First(&job, "kind = ?", models.JobPlanner).Error; err != nil {
		t.Fatal(err)
	}
	if job.Status != models.JobPending || job.Attempts != 1 || job.LastError != "vertex down" {
		t.Fatalf("want pending retry, got %+v", job)
	}
	if !job.RunAt.After(time.Now()) {
		t.Fatalf("retry must be in the future, got %v", job.RunAt)
	}
}

func TestDrainRecoversPanic(t *testing.T) {
	f := &fakeServices{panicKind: models.JobTriage}
	w := testWorker(t, f)
	ctx := context.Background()
	if err := w.Queue.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	w.drain(ctx) // must not panic the test binary
	var job models.Job
	if err := w.Queue.DB.First(&job, "kind = ? AND status = ?", models.JobTriage, models.JobPending).Error; err != nil {
		t.Fatal(err)
	}
	if job.LastError == "" {
		t.Fatal("panic should be recorded in last_error")
	}
}

func TestDrainKeepsSyncWarnings(t *testing.T) {
	f := &fakeServices{syncErrs: map[string]string{"work": "token expired", "personal": "rate limited"}}
	w := testWorker(t, f)
	ctx := context.Background()
	if err := w.Queue.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	w.drain(ctx)
	var job models.Job
	if err := w.Queue.DB.First(&job, "kind = ? AND status = ?", models.JobSync, models.JobSucceeded).Error; err != nil {
		t.Fatal(err)
	}
	if job.LastError != "personal: rate limited; work: token expired" {
		t.Fatalf("want sorted warning string, got %q", job.LastError)
	}
}

func TestStartSeedsAndStops(t *testing.T) {
	f := &fakeServices{}
	w := testWorker(t, f)
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var n int64
		w.Queue.DB.Model(&models.Job{}).Where("status = ?", models.JobSucceeded).Count(&n)
		if n == 4 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	w.Stop()
	var n int64
	w.Queue.DB.Model(&models.Job{}).Where("status = ?", models.JobSucceeded).Count(&n)
	if n != 4 {
		t.Fatalf("want 4 succeeded jobs after start, got %d", n)
	}
}

func TestEnqueuePokesWorker(t *testing.T) {
	f := &fakeServices{}
	w := testWorker(t, f)
	if _, _, err := w.Enqueue(context.Background(), models.JobSync); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.poke:
	default:
		t.Fatal("enqueue should poke the worker")
	}
}
