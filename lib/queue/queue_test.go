package queue

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/testdb"
)

// fixedQueue pins the clock so grid and backoff math is assertable.
func fixedQueue(t *testing.T) (*Queue, time.Time) {
	t.Helper()
	now := time.Date(2026, 7, 4, 10, 20, 0, 0, time.UTC)
	return &Queue{DB: testdb.Open(t), Now: func() time.Time { return now }}, now
}

func mustJob(t *testing.T, q *Queue, kind models.JobKind, status models.JobStatus, runAt time.Time, attempts int) models.Job {
	t.Helper()
	j := models.Job{Kind: kind, Status: status, RunAt: runAt, Attempts: attempts, MaxAttempts: maxAttempts}
	if err := q.DB.Create(&j).Error; err != nil {
		t.Fatalf("create job: %v", err)
	}
	return j
}

func TestEnqueueCreatesPending(t *testing.T) {
	q, now := fixedQueue(t)
	job, running, err := q.Enqueue(context.Background(), models.JobSync)
	if err != nil || running {
		t.Fatalf("enqueue: running=%v err=%v", running, err)
	}
	if job.Status != models.JobPending || !job.RunAt.Equal(now) {
		t.Fatalf("want pending at %v, got %+v", now, job)
	}
}

func TestEnqueuePullsPendingForward(t *testing.T) {
	q, now := fixedQueue(t)
	future := mustJob(t, q, models.JobSync, models.JobPending, now.Add(time.Hour), 0)
	job, running, err := q.Enqueue(context.Background(), models.JobSync)
	if err != nil || running {
		t.Fatalf("enqueue: running=%v err=%v", running, err)
	}
	if job.ID != future.ID || !job.RunAt.Equal(now) {
		t.Fatalf("want existing row pulled to now, got %+v", job)
	}
	var count int64
	q.DB.Model(&models.Job{}).Count(&count)
	if count != 1 {
		t.Fatalf("want 1 row, got %d", count)
	}
}

func TestEnqueueReportsRunning(t *testing.T) {
	q, now := fixedQueue(t)
	run := mustJob(t, q, models.JobSync, models.JobRunning, now, 1)
	job, running, err := q.Enqueue(context.Background(), models.JobSync)
	if err != nil || !running {
		t.Fatalf("enqueue: running=%v err=%v", running, err)
	}
	if job.ID != run.ID {
		t.Fatalf("want the running row back, got %+v", job)
	}
}

func TestClaimOrderAndDueness(t *testing.T) {
	q, now := fixedQueue(t)
	past := now.Add(-time.Minute)
	// Insert out of order; claim must return sync, planner, triage.
	mustJob(t, q, models.JobTriage, models.JobPending, past, 0)
	mustJob(t, q, models.JobPlanner, models.JobPending, past, 0)
	mustJob(t, q, models.JobSync, models.JobPending, past, 0)
	var got []models.JobKind
	for {
		job, err := q.Claim(context.Background())
		if errors.Is(err, ErrNoJob) {
			break
		}
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		if job.Status != models.JobRunning || job.Attempts != 1 || job.StartedAt == nil {
			t.Fatalf("claimed job not marked running: %+v", job)
		}
		got = append(got, job.Kind)
	}
	want := []models.JobKind{models.JobSync, models.JobPlanner, models.JobTriage}
	for i := range want {
		if i >= len(got) || got[i] != want[i] {
			t.Fatalf("claim order: want %v, got %v", want, got)
		}
	}

	mustJob(t, q, models.JobSync, models.JobPending, time.Now().Add(time.Hour), 0)
	if _, err := q.Claim(context.Background()); !errors.Is(err, ErrNoJob) {
		t.Fatalf("future job must not be claimable, got %v", err)
	}
}

func TestFinishRetriesWithBackoff(t *testing.T) {
	q, now := fixedQueue(t)
	job := mustJob(t, q, models.JobSync, models.JobRunning, now, 1)
	status, err := q.Finish(context.Background(), job, errors.New("boom"), "")
	if err != nil || status != models.JobPending {
		t.Fatalf("finish: status=%v err=%v", status, err)
	}
	var got models.Job
	q.DB.First(&got, "id = ?", job.ID)
	if got.Status != models.JobPending || !got.RunAt.Equal(now.Add(time.Minute)) || got.LastError != "boom" {
		t.Fatalf("want pending retry at +1m, got %+v", got)
	}
	// Second failure backs off 5m.
	got.Attempts = 2
	if _, err := q.Finish(context.Background(), got, errors.New("boom"), ""); err != nil {
		t.Fatal(err)
	}
	q.DB.First(&got, "id = ?", job.ID)
	if !got.RunAt.Equal(now.Add(5 * time.Minute)) {
		t.Fatalf("want +5m backoff, got %v", got.RunAt)
	}
}

func TestFinishTerminalChainsNextSlot(t *testing.T) {
	q, now := fixedQueue(t)
	job := mustJob(t, q, models.JobPlanner, models.JobRunning, now, 1)
	status, err := q.Finish(context.Background(), job, nil, "")
	if err != nil || status != models.JobSucceeded {
		t.Fatalf("finish: status=%v err=%v", status, err)
	}
	var next models.Job
	if err := q.DB.First(&next, "kind = ? AND status = ?", models.JobPlanner, models.JobPending).Error; err != nil {
		t.Fatalf("chained job: %v", err)
	}
	wantSlot := time.Date(2026, 7, 4, 11, 0, 0, 0, time.UTC)
	if !next.RunAt.Equal(wantSlot) {
		t.Fatalf("want next run on grid %v, got %v", wantSlot, next.RunAt)
	}

	// Exhausted attempts + error → failed, still chains.
	fail := mustJob(t, q, models.JobSync, models.JobRunning, now, maxAttempts)
	status, err = q.Finish(context.Background(), fail, errors.New("dead"), "")
	if err != nil || status != models.JobFailed {
		t.Fatalf("finish exhausted: status=%v err=%v", status, err)
	}
	var chained int64
	q.DB.Model(&models.Job{}).Where("kind = ? AND status = ?", models.JobSync, models.JobPending).Count(&chained)
	if chained != 1 {
		t.Fatalf("want chained sync job, got %d", chained)
	}
}

func TestFinishChainsPerKindGrid(t *testing.T) {
	cases := []struct {
		kind models.JobKind
		want time.Time
	}{
		{models.JobReconcile, time.Date(2026, 7, 4, 10, 30, 0, 0, time.UTC)},
		{models.JobSync, time.Date(2026, 7, 4, 10, 30, 0, 0, time.UTC)},
		{models.JobPlanner, time.Date(2026, 7, 4, 11, 0, 0, 0, time.UTC)},
		{models.JobTriage, time.Date(2026, 7, 4, 11, 0, 0, 0, time.UTC)},
	}
	for _, c := range cases {
		q, _ := fixedQueue(t)
		job := mustJob(t, q, c.kind, models.JobRunning, q.now(), 1)
		if _, err := q.Finish(context.Background(), job, nil, ""); err != nil {
			t.Fatalf("%s finish: %v", c.kind, err)
		}
		var next models.Job
		if err := q.DB.First(&next, "kind = ? AND status = ?", c.kind, models.JobPending).Error; err != nil {
			t.Fatalf("%s chained job: %v", c.kind, err)
		}
		if !next.RunAt.Equal(c.want) {
			t.Fatalf("%s chained to %v, want %v", c.kind, next.RunAt, c.want)
		}
	}
}

func TestFinishSuccessKeepsWarning(t *testing.T) {
	q, now := fixedQueue(t)
	job := mustJob(t, q, models.JobSync, models.JobRunning, now, 1)
	if _, err := q.Finish(context.Background(), job, nil, "work: token expired"); err != nil {
		t.Fatal(err)
	}
	var got models.Job
	q.DB.First(&got, "id = ?", job.ID)
	if got.Status != models.JobSucceeded || got.LastError != "work: token expired" {
		t.Fatalf("want succeeded with warning, got %+v", got)
	}
}

func TestFinishChainToleratesExistingPending(t *testing.T) {
	q, now := fixedQueue(t)
	job := mustJob(t, q, models.JobSync, models.JobRunning, now, 1)
	mustJob(t, q, models.JobSync, models.JobPending, now, 0) // e.g. a manual enqueue raced the finish
	if _, err := q.Finish(context.Background(), job, nil, ""); err != nil {
		t.Fatalf("finish with existing pending: %v", err)
	}
	var pending int64
	q.DB.Model(&models.Job{}).Where("kind = ? AND status = ?", models.JobSync, models.JobPending).Count(&pending)
	if pending != 1 {
		t.Fatalf("want 1 pending sync job, got %d", pending)
	}
}

func TestSeedCreatesMissingOnly(t *testing.T) {
	q, now := fixedQueue(t)
	existing := mustJob(t, q, models.JobSync, models.JobPending, now.Add(30*time.Minute), 0)
	if err := q.Seed(context.Background()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var jobs []models.Job
	q.DB.Where("status = ?", models.JobPending).Order("kind").Find(&jobs)
	if len(jobs) != 3 {
		t.Fatalf("want 3 pending jobs, got %d", len(jobs))
	}
	for _, j := range jobs {
		if j.Kind == models.JobSync {
			if j.ID != existing.ID || !j.RunAt.Equal(existing.RunAt) {
				t.Fatalf("seed must not touch existing pending row: %+v", j)
			}
			continue
		}
		if !j.RunAt.Equal(now) {
			t.Fatalf("seeded %s should be due now, got %v", j.Kind, j.RunAt)
		}
	}
}

func TestReapResetsRunning(t *testing.T) {
	q, now := fixedQueue(t)
	orphan := mustJob(t, q, models.JobPlanner, models.JobRunning, now.Add(-2*time.Hour), 2)
	done := mustJob(t, q, models.JobSync, models.JobSucceeded, now.Add(-2*time.Hour), 1)
	if err := q.Reap(context.Background()); err != nil {
		t.Fatalf("reap: %v", err)
	}
	var got models.Job
	q.DB.First(&got, "id = ?", orphan.ID)
	if got.Status != models.JobPending || !got.RunAt.Equal(now) || got.Attempts != 2 {
		t.Fatalf("want orphan pending now with attempts kept, got %+v", got)
	}
	var doneRow models.Job
	q.DB.First(&doneRow, "id = ?", done.ID)
	if doneRow.Status != models.JobSucceeded {
		t.Fatalf("terminal rows must be untouched, got %+v", doneRow)
	}
}
