package queue

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.icco.me/art/lib/models"
	"go.icco.me/art/lib/testdb"
)

var errTestVertexDown = errors.New("vertex down")

type triageFake struct {
	runs  int
	err   error
	panic bool
}

func (f *triageFake) RunAll(context.Context) error {
	f.runs++
	if f.panic {
		panic("triage kaboom")
	}
	return f.err
}

func testWorker(t *testing.T, f *triageFake) *Worker {
	t.Helper()
	return New(testdb.Open(t), f)
}

func TestDrainRunsDueJobs(t *testing.T) {
	f := &triageFake{}
	w := testWorker(t, f)
	ctx := context.Background()
	if err := w.Queue.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	w.drain(ctx)
	if f.runs != 1 {
		t.Fatalf("want 1 triage run, got %d", f.runs)
	}
	var pending, succeeded int64
	w.Queue.DB.Model(&models.Job{}).Where("status = ?", models.JobPending).Count(&pending)
	w.Queue.DB.Model(&models.Job{}).Where("status = ?", models.JobSucceeded).Count(&succeeded)
	if pending != 1 || succeeded != 1 {
		t.Fatalf("want 1 succeeded + 1 chained pending, got %d/%d", succeeded, pending)
	}
}

func TestDrainRecordsFailureForRetry(t *testing.T) {
	f := &triageFake{err: errTestVertexDown}
	w := testWorker(t, f)
	ctx := context.Background()
	if err := w.Queue.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	w.drain(ctx)
	var job models.Job
	if err := w.Queue.DB.First(&job, "kind = ?", models.JobTriage).Error; err != nil {
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
	f := &triageFake{panic: true}
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

func TestStartSeedsAndStops(t *testing.T) {
	f := &triageFake{}
	w := testWorker(t, f)
	if err := w.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var n int64
		w.Queue.DB.Model(&models.Job{}).Where("status = ?", models.JobSucceeded).Count(&n)
		if n == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	w.Stop()
	var n int64
	w.Queue.DB.Model(&models.Job{}).Where("status = ?", models.JobSucceeded).Count(&n)
	if n != 1 {
		t.Fatalf("want 1 succeeded job after start, got %d", n)
	}
}

func TestEnqueuePokesWorker(t *testing.T) {
	w := testWorker(t, &triageFake{})
	if _, _, err := w.Enqueue(context.Background(), models.JobTriage); err != nil {
		t.Fatal(err)
	}
	select {
	case <-w.poke:
	default:
		t.Fatal("enqueue should poke the worker")
	}
}
