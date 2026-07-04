# DB-Backed Job Queue Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the in-memory `lib/cron` ticker with a Postgres-backed job queue and in-process worker so restarts don't reset or duplicate scheduled work.

**Architecture:** A `jobs` table (GORM/AutoMigrate) holds one pending row per kind; a single worker goroutine claims due rows with `FOR UPDATE SKIP LOCKED`, runs them with per-job timeout + panic recovery, retries with backoff, and chains the next run onto the hourly clock grid. Manual API triggers enqueue into the same table. Spec: `docs/superpowers/specs/2026-07-04-db-job-queue-design.md`.

**Tech Stack:** Go 1.26, GORM/Postgres, chi, prometheus client_golang, Bubble Tea v2 TUI.

## Global Constraints

- All work on one branch `db-job-queue`, many commits; never amend or force-push.
- Conventional Commits, lowercase titles (PR title gate).
- DB tests need `TEST_DATABASE_URL` (docker `postgres:17`) and `go test -p 1 ./...`; CI enforces 50% total coverage.
- golangci-lint v2 with bodyclose, contextcheck, copyloopvar, errorlint, gocritic, gosec, intrange, misspell, nilerr, noctx, revive, unconvert, unparam, wastedassign. Exported identifiers need doc comments. Don't name parameters `max`/`min`.
- Comments: short, one line where non-obvious; markdown prose unwrapped.
- Retry backoff after attempt n: `1m * 5^(n-1)` (1m, 5m, 25m); `MaxAttempts` = 4; grid slot = `now.UTC().Truncate(time.Hour).Add(time.Hour)`; job timeout 30m; poll interval 15s.

**Test environment setup (once per session):**

```bash
docker run -d --name art-test-pg -e POSTGRES_USER=art -e POSTGRES_PASSWORD=art -e POSTGRES_DB=art_test -p 5433:5432 postgres:17
export TEST_DATABASE_URL='postgres://art:art@localhost:5433/art_test?sslmode=disable'
```

(If a compose Postgres already runs on 5432, port 5433 avoids clashing. Tear down with `docker rm -f art-test-pg` when done.)

---

### Task 1: Job model

**Files:**
- Modify: `lib/models/models.go` (add JobKind/JobStatus types + consts + Valid methods, Job struct, JobKinds helper, extend `All()`)
- Test: `lib/models/job_test.go` (new, `package models_test`)

**Interfaces:**
- Consumes: existing `models.Base`.
- Produces: `models.Job` struct; `models.JobKind` (`JobSync`, `JobPlanner`, `JobTriage`) with `Valid() bool` and `JobKinds() []JobKind`; `models.JobStatus` (`JobPending`, `JobRunning`, `JobSucceeded`, `JobFailed`) with `Valid() bool`. Partial unique index: one pending job per kind.

- [ ] **Step 1: Write the failing test**

`lib/models/job_test.go`:

```go
package models_test

import (
	"testing"
	"time"

	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/testdb"
)

func TestJobKindValid(t *testing.T) {
	for _, k := range models.JobKinds() {
		if !k.Valid() {
			t.Errorf("kind %q should be valid", k)
		}
	}
	if models.JobKind("bogus").Valid() {
		t.Error("bogus kind should be invalid")
	}
	if models.JobStatus("bogus").Valid() {
		t.Error("bogus status should be invalid")
	}
	if !models.JobPending.Valid() {
		t.Error("pending should be valid")
	}
}

func TestJobOnePendingPerKind(t *testing.T) {
	db := testdb.Open(t)
	mk := func(status models.JobStatus) error {
		return db.Create(&models.Job{
			Kind: models.JobSync, Status: status, RunAt: time.Now(), MaxAttempts: 4,
		}).Error
	}
	if err := mk(models.JobSucceeded); err != nil {
		t.Fatalf("terminal row: %v", err)
	}
	if err := mk(models.JobPending); err != nil {
		t.Fatalf("first pending: %v", err)
	}
	if err := mk(models.JobPending); err == nil {
		t.Fatal("second pending job of same kind should violate the partial unique index")
	}
	if err := db.Create(&models.Job{
		Kind: models.JobTriage, Status: models.JobPending, RunAt: time.Now(), MaxAttempts: 4,
	}).Error; err != nil {
		t.Fatalf("pending job of another kind: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./lib/models/ -run TestJob -v`
Expected: compile error — `models.Job`, `models.JobKinds` undefined.

- [ ] **Step 3: Write the implementation**

In `lib/models/models.go`, after the `AgentRunKind` type declarations add:

```go
// JobKind identifies which recurring task a queued Job runs.
type JobKind string

// JobStatus is the lifecycle status of a Job.
type JobStatus string
```

In the const block, after the AgentRun values:

```go
	JobSync    JobKind = "sync"
	JobPlanner JobKind = "planner"
	JobTriage  JobKind = "triage"

	JobPending   JobStatus = "pending"
	JobRunning   JobStatus = "running"
	JobSucceeded JobStatus = "succeeded"
	JobFailed    JobStatus = "failed"
```

Near the other Valid methods:

```go
// Valid reports whether k is one of the recognised JobKind values.
func (k JobKind) Valid() bool { return k == JobSync || k == JobPlanner || k == JobTriage }

// Valid reports whether s is one of the recognised JobStatus values.
func (s JobStatus) Valid() bool {
	switch s {
	case JobPending, JobRunning, JobSucceeded, JobFailed:
		return true
	}
	return false
}

// JobKinds returns all job kinds in their within-slot execution order.
func JobKinds() []JobKind { return []JobKind{JobSync, JobPlanner, JobTriage} }
```

After the `EmailMessage` struct:

```go
// Job is one queued, running, or finished unit of background work. The
// partial unique index keeps at most one pending job per kind; recurrence
// chains a new pending row when a job reaches a terminal status.
type Job struct {
	Base
	Kind        JobKind    `gorm:"type:varchar(16);not null;check:kind IN ('sync','planner','triage');index:idx_jobs_one_pending,unique,where:status = 'pending'" json:"kind"`
	Status      JobStatus  `gorm:"type:varchar(16);not null;default:'pending';index;check:status IN ('pending','running','succeeded','failed')" json:"status"`
	RunAt       time.Time  `gorm:"not null;index" json:"run_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
	Attempts    int        `gorm:"not null;default:0" json:"attempts"`
	MaxAttempts int        `gorm:"not null;default:4" json:"max_attempts"`
	LastError   string     `gorm:"type:text;not null;default:''" json:"last_error"`
}
```

Add `&Job{}` to the end of the slice in `All()`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -p 1 ./lib/models/ -run TestJob -v`
Expected: PASS (both tests; the DB test skips if TEST_DATABASE_URL is unset — export it first).

- [ ] **Step 5: Commit**

```bash
git add lib/models/models.go lib/models/job_test.go
git commit -m "feat: add job model for db-backed queue"
```

---

### Task 2: Queue primitives

**Files:**
- Create: `lib/queue/queue.go`
- Test: `lib/queue/queue_test.go`

**Interfaces:**
- Consumes: `models.Job`, `models.JobKind(s)`, `models.JobStatus` from Task 1.
- Produces: `queue.Queue{DB *gorm.DB; Now func() time.Time}` with methods `Enqueue(ctx, kind) (models.Job, bool, error)`, `Claim(ctx) (models.Job, error)` (returns `queue.ErrNoJob` when idle), `Finish(ctx, job, runErr error, warning string) (models.JobStatus, error)`, `Seed(ctx) error`, `Reap(ctx) error`. Package consts `maxAttempts = 4`.

- [ ] **Step 1: Write the failing tests**

`lib/queue/queue_test.go`:

```go
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
	// Second failure backs off 5m, third 25m.
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
	q.DB.First(&got, "id = ?", done.ID)
	if got.Status != models.JobSucceeded {
		t.Fatalf("terminal rows must be untouched, got %+v", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -p 1 ./lib/queue/ -v`
Expected: compile error — package doesn't exist yet.

- [ ] **Step 3: Write the implementation**

`lib/queue/queue.go`:

```go
// Package queue implements the Postgres-backed job queue and worker that
// replaced the in-memory cron scheduler; see
// docs/superpowers/specs/2026-07-04-db-job-queue-design.md.
package queue

import (
	"context"
	"errors"
	"time"

	"github.com/icco/art/lib/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// maxAttempts bounds tries per job: the first run plus three retries.
const maxAttempts = 4

// ErrNoJob is returned by Claim when nothing is due.
var ErrNoJob = errors.New("queue: no job due")

// Queue provides persistent job operations over the jobs table.
type Queue struct {
	DB *gorm.DB
	// Now is injectable for tests; nil means time.Now.
	Now func() time.Time
}

func (q *Queue) now() time.Time {
	if q.Now != nil {
		return q.Now()
	}
	return time.Now()
}

// backoff returns the retry delay after the nth attempt: 1m, 5m, 25m, …
func backoff(attempt int) time.Duration {
	d := time.Minute
	for i := 1; i < attempt; i++ {
		d *= 5
	}
	return d
}

// nextSlot returns the next top-of-hour grid slot after t, in UTC. All kinds
// recur hourly; scheduled runs stay pinned to the clock grid.
func nextSlot(t time.Time) time.Time {
	return t.UTC().Truncate(time.Hour).Add(time.Hour)
}

// onePendingConflict targets the partial unique index so inserting a pending
// job is a no-op when one already exists for that kind.
var onePendingConflict = clause.OnConflict{
	Columns:     []clause.Column{{Name: "kind"}},
	TargetWhere: clause.Where{Exprs: []clause.Expression{gorm.Expr("status = 'pending'")}},
	DoNothing:   true,
}

// Enqueue makes kind runnable now: a running job is reported as-is, an
// existing pending job is pulled forward, otherwise a fresh row is inserted.
func (q *Queue) Enqueue(ctx context.Context, kind models.JobKind) (models.Job, bool, error) {
	var job models.Job
	running := false
	err := q.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Where("kind = ? AND status = ?", kind, models.JobRunning).First(&job).Error
		if err == nil {
			running = true
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		err = tx.Where("kind = ? AND status = ?", kind, models.JobPending).First(&job).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			job = models.Job{Kind: kind, Status: models.JobPending, RunAt: q.now(), MaxAttempts: maxAttempts}
			return tx.Create(&job).Error
		}
		if err != nil {
			return err
		}
		job.RunAt = q.now()
		return tx.Model(&job).Update("run_at", job.RunAt).Error
	})
	return job, running, err
}

// claimSQL atomically claims the next due pending job; kinds sharing a slot
// run in sync → planner → triage order. SKIP LOCKED keeps a second worker or
// replica safe even though today there is only one.
const claimSQL = `
UPDATE jobs SET status = 'running', started_at = now(), attempts = attempts + 1, updated_at = now()
WHERE id = (
	SELECT id FROM jobs
	WHERE status = 'pending' AND run_at <= now()
	ORDER BY run_at, CASE kind WHEN 'sync' THEN 0 WHEN 'planner' THEN 1 ELSE 2 END
	LIMIT 1
	FOR UPDATE SKIP LOCKED
)
RETURNING *`

// Claim returns the next due job marked running, or ErrNoJob.
func (q *Queue) Claim(ctx context.Context) (models.Job, error) {
	var job models.Job
	res := q.DB.WithContext(ctx).Raw(claimSQL).Scan(&job)
	if res.Error != nil {
		return models.Job{}, res.Error
	}
	if res.RowsAffected == 0 {
		return models.Job{}, ErrNoJob
	}
	return job, nil
}

// Finish records a claimed job's outcome and returns the status it ended in.
// Retryable failures go back to pending with backoff; terminal outcomes chain
// the next recurring run onto the hourly grid. A warning (e.g. sync's
// per-account errors) is kept in last_error without failing the job.
func (q *Queue) Finish(ctx context.Context, job models.Job, runErr error, warning string) (models.JobStatus, error) {
	now := q.now()
	if runErr != nil && job.Attempts < job.MaxAttempts {
		err := q.DB.WithContext(ctx).Model(&models.Job{}).Where("id = ?", job.ID).Updates(map[string]any{
			"status":     models.JobPending,
			"run_at":     now.Add(backoff(job.Attempts)),
			"started_at": nil,
			"last_error": runErr.Error(),
		}).Error
		return models.JobPending, err
	}
	status := models.JobSucceeded
	lastError := warning
	if runErr != nil {
		status = models.JobFailed
		lastError = runErr.Error()
	}
	err := q.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Job{}).Where("id = ?", job.ID).Updates(map[string]any{
			"status":      status,
			"finished_at": now,
			"last_error":  lastError,
		}).Error; err != nil {
			return err
		}
		next := models.Job{Kind: job.Kind, Status: models.JobPending, RunAt: nextSlot(now), MaxAttempts: maxAttempts}
		return tx.Clauses(onePendingConflict).Create(&next).Error
	})
	return status, err
}

// Seed ensures every kind has a pending job; missing ones are due
// immediately, so a fresh deploy runs its first pass at boot.
func (q *Queue) Seed(ctx context.Context) error {
	for _, kind := range models.JobKinds() {
		job := models.Job{Kind: kind, Status: models.JobPending, RunAt: q.now(), MaxAttempts: maxAttempts}
		if err := q.DB.WithContext(ctx).Clauses(onePendingConflict).Create(&job).Error; err != nil {
			return err
		}
	}
	return nil
}

// Reap resets running jobs to pending-now. Called at boot, before the worker
// starts: in a single-process deployment any running row belongs to a dead
// process. Attempts were counted at claim, so crash loops still exhaust
// MaxAttempts.
func (q *Queue) Reap(ctx context.Context) error {
	return q.DB.WithContext(ctx).Model(&models.Job{}).Where("status = ?", models.JobRunning).Updates(map[string]any{
		"status":     models.JobPending,
		"run_at":     q.now(),
		"started_at": nil,
	}).Error
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -p 1 ./lib/queue/ -v`
Expected: PASS (skips without TEST_DATABASE_URL — export it).

- [ ] **Step 5: Commit**

```bash
git add lib/queue/queue.go lib/queue/queue_test.go
git commit -m "feat: add postgres-backed job queue primitives"
```

---

### Task 3: Worker

**Files:**
- Create: `lib/queue/worker.go`, `lib/queue/metrics.go`
- Test: `lib/queue/worker_test.go`

**Interfaces:**
- Consumes: `Queue` from Task 2.
- Produces: `queue.New(db, sync, planner, triage) *Worker`; `(*Worker) Start(ctx) error`, `Stop()`, `Poke()`, `Enqueue(ctx, kind) (models.Job, bool, error)`; interfaces `queue.SyncService` (`RunAll(ctx) (map[string]string, error)`), `queue.PlannerService` (`Run(ctx) error`), `queue.TriageService` (`RunAll(ctx) error`). Unexported `drain(ctx)` used by tests.

- [ ] **Step 1: Write the failing tests**

`lib/queue/worker_test.go`:

```go
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

// triageFake separates triage's RunAll from sync's.
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
	w := New(testdb.Open(t), f, f, triageFake{f})
	return w
}

func TestDrainRunsDueJobsInOrder(t *testing.T) {
	f := &fakeServices{}
	w := testWorker(t, f)
	ctx := context.Background()
	if err := w.Queue.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	w.drain(ctx)
	want := []string{"sync", "planner", "triage"}
	if len(f.order) != 3 || f.order[0] != want[0] || f.order[1] != want[1] || f.order[2] != want[2] {
		t.Fatalf("want %v, got %v", want, f.order)
	}
	var pending, succeeded int64
	w.Queue.DB.Model(&models.Job{}).Where("status = ?", models.JobPending).Count(&pending)
	w.Queue.DB.Model(&models.Job{}).Where("status = ?", models.JobSucceeded).Count(&succeeded)
	if pending != 3 || succeeded != 3 {
		t.Fatalf("want 3 succeeded + 3 chained pending, got %d/%d", succeeded, pending)
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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := w.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var n int64
		w.Queue.DB.Model(&models.Job{}).Where("status = ?", models.JobSucceeded).Count(&n)
		if n == 3 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	w.Stop()
	var n int64
	w.Queue.DB.Model(&models.Job{}).Where("status = ?", models.JobSucceeded).Count(&n)
	if n != 3 {
		t.Fatalf("want 3 succeeded jobs after start, got %d", n)
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -p 1 ./lib/queue/ -v`
Expected: compile error — `New`, `Worker` undefined.

- [ ] **Step 3: Write the implementation**

`lib/queue/metrics.go`:

```go
package queue

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	jobsProcessed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "art_jobs_processed_total",
		Help: "Job outcomes by kind; status pending means a retry was scheduled.",
	}, []string{"kind", "status"})
	jobDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "art_job_duration_seconds",
		Help:    "Job execution time by kind.",
		Buckets: prometheus.ExponentialBuckets(0.1, 4, 8),
	}, []string{"kind"})
)
```

`lib/queue/worker.go`:

```go
package queue

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/icco/art/lib/models"
	gutillog "github.com/icco/gutil/logging"
	"gorm.io/gorm"
)

const (
	pollInterval = 15 * time.Second
	// A hung Google/Vertex call must not block the queue forever.
	jobTimeout = 30 * time.Minute
)

// SyncService, PlannerService, and TriageService are the job implementations
// the worker drives, satisfied by calendar.Runner, agent.Planner, and
// email.Runner.
type (
	// SyncService runs upstream calendar syncs.
	SyncService interface {
		RunAll(ctx context.Context) (map[string]string, error)
	}
	// PlannerService executes a planner pass.
	PlannerService interface {
		Run(ctx context.Context) error
	}
	// TriageService executes an email-triage pass.
	TriageService interface {
		RunAll(ctx context.Context) error
	}
)

// Worker polls the queue and executes jobs one at a time, keeping the
// sync → planner → triage order within a shared slot.
type Worker struct {
	Queue   *Queue
	Sync    SyncService
	Planner PlannerService
	Triage  TriageService

	poke     chan struct{}
	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// New returns a Worker over db ready to be Start()ed.
func New(db *gorm.DB, sync SyncService, planner PlannerService, triage TriageService) *Worker {
	return &Worker{
		Queue:   &Queue{DB: db},
		Sync:    sync,
		Planner: planner,
		Triage:  triage,
		poke:    make(chan struct{}, 1),
		stop:    make(chan struct{}),
	}
}

// Start reaps orphaned jobs, seeds missing schedules, and launches the
// polling goroutine; overdue jobs (including seeds) run immediately.
func (w *Worker) Start(ctx context.Context) error {
	if err := w.Queue.Reap(ctx); err != nil {
		return fmt.Errorf("queue reap: %w", err)
	}
	if err := w.Queue.Seed(ctx); err != nil {
		return fmt.Errorf("queue seed: %w", err)
	}
	w.wg.Add(1)
	go func() {
		defer w.wg.Done()
		tick := time.NewTicker(pollInterval)
		defer tick.Stop()
		w.drain(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.stop:
				return
			case <-w.poke:
			case <-tick.C:
			}
			w.drain(ctx)
		}
	}()
	return nil
}

// Stop halts polling and waits for any in-flight job to return.
func (w *Worker) Stop() {
	w.stopOnce.Do(func() { close(w.stop) })
	w.wg.Wait()
}

// Poke wakes the worker before the next poll tick.
func (w *Worker) Poke() {
	select {
	case w.poke <- struct{}{}:
	default:
	}
}

// Enqueue queues kind to run now and wakes the worker. It satisfies the API
// handlers' JobsService.
func (w *Worker) Enqueue(ctx context.Context, kind models.JobKind) (models.Job, bool, error) {
	job, running, err := w.Queue.Enqueue(ctx, kind)
	if err == nil && !running {
		w.Poke()
	}
	return job, running, err
}

// drain claims and runs due jobs until the queue is empty or ctx ends.
func (w *Worker) drain(ctx context.Context) {
	log := gutillog.FromContext(ctx)
	for ctx.Err() == nil {
		select {
		case <-w.stop:
			return
		default:
		}
		job, err := w.Queue.Claim(ctx)
		if errors.Is(err, ErrNoJob) {
			return
		}
		if err != nil {
			log.Errorw("job claim failed", "err", err)
			return
		}
		w.run(ctx, job)
	}
}

// run executes one claimed job and records the outcome. The finish write
// survives ctx cancellation so a graceful shutdown records the retry instead
// of leaving the row running.
func (w *Worker) run(ctx context.Context, job models.Job) {
	log := gutillog.FromContext(ctx)
	log.Infow("job started", "job", job.ID, "kind", job.Kind, "attempt", job.Attempts)
	start := time.Now()
	jobCtx, cancel := context.WithTimeout(ctx, jobTimeout)
	warning, err := w.execute(jobCtx, job.Kind)
	cancel()
	jobDuration.WithLabelValues(string(job.Kind)).Observe(time.Since(start).Seconds())

	finishCtx, cancelFinish := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancelFinish()
	status, finishErr := w.Queue.Finish(finishCtx, job, err, warning)
	if finishErr != nil {
		log.Errorw("job finish failed", "job", job.ID, "kind", job.Kind, "err", finishErr)
		return
	}
	jobsProcessed.WithLabelValues(string(job.Kind), string(status)).Inc()
	switch {
	case err != nil && status == models.JobPending:
		log.Warnw("job failed, retry scheduled", "job", job.ID, "kind", job.Kind, "attempt", job.Attempts, "err", err)
	case err != nil:
		log.Errorw("job failed", "job", job.ID, "kind", job.Kind, "attempts", job.Attempts, "err", err)
	case warning != "":
		log.Warnw("job succeeded with warnings", "job", job.ID, "kind", job.Kind, "warnings", warning)
	default:
		log.Infow("job succeeded", "job", job.ID, "kind", job.Kind)
	}
}

// execute dispatches to the job implementation, converting panics into
// errors so one bad pass can't kill the worker.
func (w *Worker) execute(ctx context.Context, kind models.JobKind) (warning string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v\n%s", r, debug.Stack())
		}
	}()
	switch kind {
	case models.JobSync:
		accountErrs, runErr := w.Sync.RunAll(ctx)
		if runErr != nil {
			return "", runErr
		}
		return formatAccountErrors(accountErrs), nil
	case models.JobPlanner:
		return "", w.Planner.Run(ctx)
	case models.JobTriage:
		return "", w.Triage.RunAll(ctx)
	}
	return "", fmt.Errorf("unknown job kind %q", kind)
}

// formatAccountErrors flattens sync's per-account error map for last_error,
// sorted for stable output.
func formatAccountErrors(errs map[string]string) string {
	if len(errs) == 0 {
		return ""
	}
	keys := make([]string, 0, len(errs))
	for k := range errs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+": "+errs[k])
	}
	return strings.Join(parts, "; ")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -p 1 ./lib/queue/ -v`
Expected: PASS, all queue and worker tests.

- [ ] **Step 5: Commit**

```bash
git add lib/queue/worker.go lib/queue/metrics.go lib/queue/worker_test.go
git commit -m "feat: add queue worker with retries and metrics"
```

---

### Task 4: API handlers and router

**Files:**
- Create: `lib/api/handlers/jobs.go`, `lib/api/handlers/jobs_test.go`
- Modify: `lib/api/handlers/handlers.go` (Handlers struct + service interfaces), `lib/api/handlers/triage.go` (delete TriageRun + its imports/const), `lib/api/router.go` (routes)
- Delete: `lib/api/handlers/sync.go`, `lib/api/handlers/replan.go`, `lib/api/handlers/replan_test.go`
- Modify tests: `lib/api/handlers/triage_test.go`, `lib/api/handlers/handlers_test.go` (drop TriageRun/Sync/Planner fakes and routes)

**Interfaces:**
- Consumes: `models.Job`, `models.JobKind`, `models.JobStatus` (Task 1). The live implementation of the new `JobsService` is `*queue.Worker` (Task 3), but handlers only see the interface.
- Produces: `Handlers.Jobs JobsService` where `JobsService` has `Enqueue(ctx context.Context, kind models.JobKind) (job models.Job, running bool, err error)`; handlers `SyncRun`, `ReplanRun`, `TriageRun` (`202 {"status":"queued"|"running","job":{…}}`), `JobsList` (`GET /jobs?kind=&status=&limit=&offset=`), `JobsGet` (`GET /jobs/{id}`). `TriageService` loses `RunAll`; `SyncService`/`PlannerService` are deleted from handlers.

- [ ] **Step 1: Write the failing tests**

`lib/api/handlers/jobs_test.go`:

```go
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
```

Note: the `do(...)` request helper already exists in `handlers_test.go`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -p 1 ./lib/api/handlers/ -run 'TestTrigger|TestJobs' -v`
Expected: compile error — `Handlers.Jobs`, `JobsList`, `JobsGet` undefined.

- [ ] **Step 3: Write the implementation**

`lib/api/handlers/jobs.go`:

```go
package handlers

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/icco/art/lib/models"
	"gorm.io/gorm"
)

// SyncRun enqueues a calendar-sync job; the queue worker executes it.
// Clients poll /jobs/{id} for the outcome.
func (h *Handlers) SyncRun(w http.ResponseWriter, r *http.Request) {
	h.enqueueJob(w, r, models.JobSync)
}

// ReplanRun enqueues a planner pass; clients poll /agent-runs or /jobs/{id}.
func (h *Handlers) ReplanRun(w http.ResponseWriter, r *http.Request) {
	h.enqueueJob(w, r, models.JobPlanner)
}

// TriageRun enqueues an email-triage pass; clients poll /agent-runs or /jobs/{id}.
func (h *Handlers) TriageRun(w http.ResponseWriter, r *http.Request) {
	h.enqueueJob(w, r, models.JobTriage)
}

func (h *Handlers) enqueueJob(w http.ResponseWriter, r *http.Request, kind models.JobKind) {
	job, running, err := h.Jobs.Enqueue(r.Context(), kind)
	if err != nil {
		writeServerError(w, r, "enqueue "+string(kind), err)
		return
	}
	status := "queued"
	if running {
		status = "running"
	}
	writeJSON(w, r, http.StatusAccepted, map[string]any{"status": status, "job": job})
}

// JobsList responds with jobs by most recent activity. Supports optional
// kind and status filters plus standard pagination.
func (h *Handlers) JobsList(w http.ResponseWriter, r *http.Request) {
	limit, offset, ok := parsePagination(w, r)
	if !ok {
		return
	}
	q := h.DB.WithContext(r.Context())
	if kind := r.URL.Query().Get("kind"); kind != "" {
		if !models.JobKind(kind).Valid() {
			writeError(w, r, http.StatusBadRequest, "kind must be one of sync, planner, triage")
			return
		}
		q = q.Where("kind = ?", kind)
	}
	if status := r.URL.Query().Get("status"); status != "" {
		if !models.JobStatus(status).Valid() {
			writeError(w, r, http.StatusBadRequest, "status must be one of pending, running, succeeded, failed")
			return
		}
		q = q.Where("status = ?", status)
	}
	var out []models.Job
	if err := q.Order("updated_at DESC").Limit(limit).Offset(offset).Find(&out).Error; err != nil {
		writeServerError(w, r, "jobs list", err)
		return
	}
	writeJSON(w, r, http.StatusOK, out)
}

// JobsGet responds with a single job by id.
func (h *Handlers) JobsGet(w http.ResponseWriter, r *http.Request) {
	var job models.Job
	if err := h.DB.WithContext(r.Context()).First(&job, "id = ?", chi.URLParam(r, "id")).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, r, http.StatusNotFound, "job not found")
			return
		}
		writeServerError(w, r, "job get", err)
		return
	}
	writeJSON(w, r, http.StatusOK, job)
}
```

In `lib/api/handlers/handlers.go`:
- Replace the `Sync SyncService` and `Planner PlannerService` fields with `Jobs JobsService` (keep `Triage`).
- Delete the `SyncService` and `PlannerService` interface declarations.
- Remove `RunAll(ctx context.Context) error` from `TriageService` (Reverse/SetArchived stay).
- Add to the interface type block:

```go
	// JobsService enqueues background jobs; satisfied by *queue.Worker.
	JobsService interface {
		Enqueue(ctx context.Context, kind models.JobKind) (job models.Job, running bool, err error)
	}
```

Delete `lib/api/handlers/sync.go`, `lib/api/handlers/replan.go`, `lib/api/handlers/replan_test.go`. In `lib/api/handlers/triage.go` delete `TriageRun`, the `triageRunTimeout` const, and now-unused imports (`context`, `runtime/debug`, `time`, `gutillog`). In `triage_test.go` delete `TestTriageRun` and `TestTriageRunRecoversPanic` plus their fakes; in `handlers_test.go` remove the `/triage/run` route registration and any `Sync`/`Planner` fakes wired into `Handlers` literals (the compiler will point at each).

In `lib/api/router.go`, next to the `/agent-runs` route add:

```go
		r.Get("/jobs", d.H.JobsList)
		r.Get("/jobs/{id}", d.H.JobsGet)
```

(`/sync`, `/replan`, `/triage/run` routes stay, now backed by the enqueue handlers.)

- [ ] **Step 4: Run the handler tests**

Run: `go test -p 1 ./lib/api/... -v`
Expected: PASS. `go build ./...` still fails (main.go references removed fields) — fixed in Task 5.

- [ ] **Step 5: Commit**

```bash
git add lib/api
git commit -m "feat: enqueue jobs from trigger endpoints and add jobs api"
```

---

### Task 5: Wire worker in main.go, delete lib/cron

**Files:**
- Modify: `main.go`
- Delete: `lib/cron/scheduler.go`, `lib/cron/scheduler_test.go`

**Interfaces:**
- Consumes: `queue.New`, `(*Worker).Start/Stop/Enqueue` (Task 3); `handlers.Handlers.Jobs` (Task 4).
- Produces: a bootable server; `calendar.Runner`, `agent.Planner`, `email.Runner` now reach the worker (not handlers, except triage for Reverse/SetArchived).

- [ ] **Step 1: Modify main.go**

Replace the cron import with `"github.com/icco/art/lib/queue"`. Replace the handler construction and scheduler block:

```go
	worker := queue.New(gdb, syncRunner, planner, triager)

	h := &handlers.Handlers{
		Cfg:    cfg,
		DB:     gdb,
		OAuth:  oauthFlow,
		Jobs:   worker,
		Triage: triager,
	}
	router := api.NewRouter(api.Deps{Cfg: cfg, DB: gdb, H: h, Log: log})

	if err := worker.Start(ctx); err != nil {
		return err
	}
	defer worker.Stop()
```

Update the comment on the `serveErr` select that mentions cron: `stop() // cancel the worker so worker.Stop() isn't stuck behind a job`.

- [ ] **Step 2: Delete lib/cron**

```bash
git rm lib/cron/scheduler.go lib/cron/scheduler_test.go
```

- [ ] **Step 3: Verify everything builds and tests pass**

Run: `go build ./... && go test -p 1 ./...`
Expected: build OK, all tests PASS (TUI client test still passes because it isn't touched until Task 6 — if `TestClientEventsReplanSync` fails on the `/sync` response shape, that's Task 6; do Task 6 before committing a broken state. It shouldn't fail: the server-side change doesn't affect this client-side test's fake server.)

- [ ] **Step 4: Commit**

```bash
git add main.go
git commit -m "feat: replace cron scheduler with queue worker"
```

---

### Task 6: TUI sync await

**Files:**
- Modify: `cli/tui/api_client.go` (Job type, Sync signature, GetJob), `cli/tui/commands.go` (syncCalendars polls the job), `cli/tui/api_client_test.go` (fake /sync response + GetJob coverage)

**Interfaces:**
- Consumes: API shapes from Task 4 (`202 {"status","job"}`, `GET /jobs/{id}` returns a job).
- Produces: `Client.Sync(ctx) (Job, error)`, `Client.GetJob(ctx, id) (Job, error)`, `tui.Job` struct. `Replan`/`Triage` are unchanged ("queued" flows through the existing `pollBaseline` like "started").

- [ ] **Step 1: Update the client test**

In `cli/tui/api_client_test.go` `TestClientEventsReplanSync`, make the fake server's `"/sync"` case return an enqueue response and add a `"/jobs/j1"` case:

```go
		case "/sync":
			fmt.Fprint(w, `{"status":"queued","job":{"id":"j1","kind":"sync","status":"pending","run_at":"2026-07-04T10:00:00Z","last_error":""}}`)
		case "/jobs/j1":
			fmt.Fprint(w, `{"id":"j1","kind":"sync","status":"succeeded","run_at":"2026-07-04T10:00:00Z","last_error":""}`)
```

and assert:

```go
	job, err := c.Sync(ctx)
	if err != nil || job.ID != "j1" {
		t.Fatalf("sync: job=%+v err=%v", job, err)
	}
	got, err := c.GetJob(ctx, job.ID)
	if err != nil || got.Status != "succeeded" {
		t.Fatalf("get job: job=%+v err=%v", got, err)
	}
```

(Adapt to the file's existing fake-server style — keep its handler structure and assertions idioms.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cli/tui/ -run TestClientEventsReplanSync -v`
Expected: compile error — `c.Sync` returns 1 value, `GetJob` undefined.

- [ ] **Step 3: Update the client**

In `cli/tui/api_client.go`, after the `AgentRun` type add:

```go
// Job mirrors the API background-job resource.
type Job struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	Status     string     `json:"status"`
	RunAt      time.Time  `json:"run_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	LastError  string     `json:"last_error"`
}
```

Replace `Sync` and add `GetJob`:

```go
// Sync enqueues a calendar-sync job on the server and returns it; poll
// GetJob until the job reaches a terminal status.
func (c *Client) Sync(ctx context.Context) (Job, error) {
	var out struct {
		Status string `json:"status"`
		Job    Job    `json:"job"`
	}
	return out.Job, c.do(ctx, "POST", "/sync", nil, &out)
}

// GetJob fetches one background job by id.
func (c *Client) GetJob(ctx context.Context, id string) (Job, error) {
	var out Job
	return out, c.do(ctx, "GET", "/jobs/"+id, nil, &out)
}
```

- [ ] **Step 4: Update the command**

In `cli/tui/commands.go` replace `syncCalendars`:

```go
// syncCalendars enqueues a sync job and polls it until it lands.
func syncCalendars(c *Client) tea.Cmd {
	return func() tea.Msg {
		startCtx, cancel := bg()
		job, err := c.Sync(startCtx)
		cancel()
		if err != nil {
			return errMsg{err}
		}
		deadline := timeNow().Add(triagePollTimeout)
		for timeNow().Before(deadline) {
			time.Sleep(triagePollInterval)
			ctx, cancel := bg()
			j, err := c.GetJob(ctx, job.ID)
			cancel()
			if err != nil {
				continue
			}
			switch j.Status {
			case "succeeded":
				if j.LastError != "" {
					return statusMsg("sync done (account errors: " + j.LastError + ")")
				}
				return statusMsg("sync done")
			case "failed":
				return errMsg{fmt.Errorf("sync failed: %s", j.LastError)}
			}
		}
		return statusMsg("sync still running…")
	}
}
```

(The `context` and `time` imports are already present; the 2-minute inline timeout goes away.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test -p 1 ./cli/... -v`
Expected: PASS, including teatest-based TUI tests.

- [ ] **Step 6: Commit**

```bash
git add cli/tui
git commit -m "feat: poll sync job status in tui"
```

---

### Task 7: Full verification and PR

**Files:** none new.

- [ ] **Step 1: Full test suite with coverage**

```bash
go test -p 1 -coverprofile=coverage.out ./...
go tool cover -func=coverage.out | awk '/^total:/ {print $3}'
```

Expected: all PASS; total ≥ 50%.

- [ ] **Step 2: Lint and tidy**

```bash
go vet ./... && test -z "$(gofmt -l .)"
golangci-lint run
go mod tidy && git diff --exit-code go.mod go.sum
```

Expected: clean. Fix anything reported, commit fixes as `fix:` commits.

- [ ] **Step 3: Verify the running server (verify skill)**

Boot the server against a local Postgres and confirm: jobs seeded at boot, worker runs them, `GET /jobs` lists them, restart doesn't re-run (pending row in future), and a second restart with an overdue row catches up. This exercises the real flow without Google credentials (jobs will fail-and-retry on missing accounts — that's fine; what's being verified is the queue mechanics).

- [ ] **Step 4: Push and open PR**

```bash
git push -u origin db-job-queue
gh pr create --title "feat: replace in-memory cron with db-backed job queue" --body "..."
```

PR body: concise bullets — what changed (queue model + worker, enqueue endpoints, TUI sync polling, cron deleted), why (restarts reset/duplicate cron; downtime dropped runs), and the semantics (catch-up-once, clock-grid schedule, 3 retries w/ backoff). Note the spec and plan docs are included.
