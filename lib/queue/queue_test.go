package queue

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.icco.me/art/lib/models"
	"go.icco.me/art/lib/testdb"
	"gorm.io/gorm"
)

var (
	errTestBoom = errors.New("boom")
	errTestDead = errors.New("dead")
)

// fixedQueue pins the clock so grid and backoff math is assertable.
func fixedQueue(t *testing.T) (*Queue, time.Time) {
	t.Helper()
	now := time.Date(2026, 7, 4, 10, 20, 0, 0, time.UTC)
	return &Queue{DB: testdb.Open(t), Now: func() time.Time { return now }}, now
}

// TestClaimJudgesDuenessByTheGoClock pins a bug that stopped the queue dead:
// run_at comes from Go's clock, so comparing it to the database's now() meant
// an app clock milliseconds ahead claimed nothing.
func TestClaimJudgesDuenessByTheGoClock(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	// An hour ahead of the database stands in for ordinary container drift.
	ahead := time.Now().Add(time.Hour)
	q := &Queue{DB: db, Now: func() time.Time { return ahead }}

	if err := q.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Claim(ctx); err != nil {
		t.Fatalf("a job seeded as due must be claimable when the app clock leads the database's: %v", err)
	}
	// TestClaimDueness covers the other direction.
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
	job, running, err := q.Enqueue(context.Background(), models.JobTriage)
	if err != nil || running {
		t.Fatalf("enqueue: running=%v err=%v", running, err)
	}
	if job.Status != models.JobPending || !job.RunAt.Equal(now) {
		t.Fatalf("want pending at %v, got %+v", now, job)
	}
}

func TestEnqueuePullsPendingForward(t *testing.T) {
	q, now := fixedQueue(t)
	future := mustJob(t, q, models.JobTriage, models.JobPending, now.Add(time.Hour), 0)
	job, running, err := q.Enqueue(context.Background(), models.JobTriage)
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
	run := mustJob(t, q, models.JobTriage, models.JobRunning, now, 1)
	job, running, err := q.Enqueue(context.Background(), models.JobTriage)
	if err != nil || !running {
		t.Fatalf("enqueue: running=%v err=%v", running, err)
	}
	if job.ID != run.ID {
		t.Fatalf("want the running row back, got %+v", job)
	}
}

func TestClaimDueness(t *testing.T) {
	q, now := fixedQueue(t)
	mustJob(t, q, models.JobTriage, models.JobPending, now.Add(-time.Minute), 0)
	job, err := q.Claim(context.Background())
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if job.Status != models.JobRunning || job.Attempts != 1 || job.StartedAt == nil {
		t.Fatalf("claimed job not marked running: %+v", job)
	}
	if _, err := q.Claim(context.Background()); !errors.Is(err, ErrNoJob) {
		t.Fatalf("a claimed job must not be claimed again, got %v", err)
	}

	mustJob(t, q, models.JobTriage, models.JobPending, time.Now().Add(time.Hour), 0)
	if _, err := q.Claim(context.Background()); !errors.Is(err, ErrNoJob) {
		t.Fatalf("future job must not be claimable, got %v", err)
	}
}

func TestFinishRetriesWithBackoff(t *testing.T) {
	q, now := fixedQueue(t)
	job := mustJob(t, q, models.JobTriage, models.JobRunning, now, 1)
	status, err := q.Finish(context.Background(), job, errTestBoom, "")
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
	if _, err := q.Finish(context.Background(), got, errTestBoom, ""); err != nil {
		t.Fatal(err)
	}
	q.DB.First(&got, "id = ?", job.ID)
	if !got.RunAt.Equal(now.Add(5 * time.Minute)) {
		t.Fatalf("want +5m backoff, got %v", got.RunAt)
	}
}

func TestFinishTerminalChainsNextSlot(t *testing.T) {
	q, now := fixedQueue(t)
	job := mustJob(t, q, models.JobTriage, models.JobRunning, now, 1)
	status, err := q.Finish(context.Background(), job, nil, "")
	if err != nil || status != models.JobSucceeded {
		t.Fatalf("finish: status=%v err=%v", status, err)
	}
	var next models.Job
	if err := q.DB.First(&next, "kind = ? AND status = ?", models.JobTriage, models.JobPending).Error; err != nil {
		t.Fatalf("chained job: %v", err)
	}
	// Triage runs every 30m: from 10:20 the next grid slot is 10:30.
	wantSlot := time.Date(2026, 7, 4, 10, 30, 0, 0, time.UTC)
	if !next.RunAt.Equal(wantSlot) {
		t.Fatalf("want next run on grid %v, got %v", wantSlot, next.RunAt)
	}

	// Exhausted attempts + error → failed, still chains.
	fail := mustJob(t, q, models.JobTriage, models.JobRunning, now, maxAttempts)
	status, err = q.Finish(context.Background(), fail, errTestDead, "")
	if err != nil || status != models.JobFailed {
		t.Fatalf("finish exhausted: status=%v err=%v", status, err)
	}
	var chained int64
	q.DB.Model(&models.Job{}).Where("kind = ? AND status = ?", models.JobTriage, models.JobPending).Count(&chained)
	if chained != 1 {
		t.Fatalf("want chained triage job, got %d", chained)
	}
}

func TestFinishSuccessKeepsWarning(t *testing.T) {
	q, now := fixedQueue(t)
	job := mustJob(t, q, models.JobTriage, models.JobRunning, now, 1)
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
	job := mustJob(t, q, models.JobTriage, models.JobRunning, now, 1)
	mustJob(t, q, models.JobTriage, models.JobPending, now, 0) // e.g. a manual enqueue raced the finish
	if _, err := q.Finish(context.Background(), job, nil, ""); err != nil {
		t.Fatalf("finish with existing pending: %v", err)
	}
	var pending int64
	q.DB.Model(&models.Job{}).Where("kind = ? AND status = ?", models.JobTriage, models.JobPending).Count(&pending)
	if pending != 1 {
		t.Fatalf("want 1 pending triage job, got %d", pending)
	}
}

func TestSeedCreatesMissingOnly(t *testing.T) {
	q, now := fixedQueue(t)
	existing := mustJob(t, q, models.JobTriage, models.JobPending, now.Add(30*time.Minute), 0)
	if err := q.Seed(context.Background()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	var jobs []models.Job
	q.DB.Where("status = ?", models.JobPending).Order("kind").Find(&jobs)
	if len(jobs) != 1 || jobs[0].ID != existing.ID || !jobs[0].RunAt.Equal(existing.RunAt) {
		t.Fatalf("seed must not touch or duplicate the existing pending row: %+v", jobs)
	}
}

func TestDropRetiredKinds(t *testing.T) {
	q, now := fixedQueue(t)
	stale := mustJob(t, q, models.JobKind("planner"), models.JobPending, now, 0)
	keep := mustJob(t, q, models.JobTriage, models.JobPending, now, 0)
	if err := q.DropRetiredKinds(context.Background()); err != nil {
		t.Fatalf("drop retired: %v", err)
	}
	if err := q.DB.First(&models.Job{}, "id = ?", stale.ID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("retired planner row should be deleted, got %v", err)
	}
	if err := q.DB.First(&models.Job{}, "id = ?", keep.ID).Error; err != nil {
		t.Fatalf("served kind must survive: %v", err)
	}
}

func TestReapResetsRunning(t *testing.T) {
	q, now := fixedQueue(t)
	orphan := mustJob(t, q, models.JobTriage, models.JobRunning, now.Add(-2*time.Hour), 2)
	done := mustJob(t, q, models.JobTriage, models.JobSucceeded, now.Add(-2*time.Hour), 1)
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

// TestReapStaleLeavesFreshRunningJobsAlone is the safety property: this runs
// while the worker is live, so it must never yank a job out from under a
// worker that is still executing it.
func TestReapStaleLeavesFreshRunningJobsAlone(t *testing.T) {
	q, now := fixedQueue(t)
	j := mustJob(t, q, models.JobTriage, models.JobRunning, now.Add(-time.Minute), 1)
	started := now.Add(-time.Minute)
	if err := q.DB.Model(&j).Update("started_at", started).Error; err != nil {
		t.Fatal(err)
	}

	n, err := q.ReapStale(context.Background(), 2*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("reaped %d fresh job(s); a running worker would lose its row", n)
	}
	var got models.Job
	if err := q.DB.First(&got, "id = ?", j.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != models.JobRunning {
		t.Fatalf("want still running, got %s", got.Status)
	}
}

// TestReapStaleRequeuesAbandonedJobs covers the actual outage: a finish write
// that never landed left triage running forever, and because the successor is
// scheduled inside that same transaction, the kind stopped entirely.
func TestReapStaleRequeuesAbandonedJobs(t *testing.T) {
	q, now := fixedQueue(t)
	j := mustJob(t, q, models.JobTriage, models.JobRunning, now.Add(-24*time.Hour), 1)
	if err := q.DB.Model(&j).Update("started_at", now.Add(-24*time.Hour)).Error; err != nil {
		t.Fatal(err)
	}

	n, err := q.ReapStale(context.Background(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("want 1 reaped, got %d", n)
	}
	var got models.Job
	if err := q.DB.First(&got, "id = ?", j.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != models.JobPending {
		t.Fatalf("want pending, got %s", got.Status)
	}
	if got.StartedAt != nil {
		t.Fatalf("want started_at cleared, got %v", got.StartedAt)
	}
	// Attempts are counted at claim, so a repeatedly abandoned job still
	// exhausts MaxAttempts instead of looping forever.
	if got.Attempts != 1 {
		t.Fatalf("want attempts preserved at 1, got %d", got.Attempts)
	}
}

// TestReapStaleIgnoresRunningRowsWithNoStartedAt guards the NULL case: without
// the started_at IS NOT NULL predicate these would reap on every tick.
func TestReapStaleIgnoresRunningRowsWithNoStartedAt(t *testing.T) {
	q, now := fixedQueue(t)
	mustJob(t, q, models.JobTriage, models.JobRunning, now.Add(-24*time.Hour), 1)

	n, err := q.ReapStale(context.Background(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("reaped %d row(s) with a null started_at", n)
	}
}
