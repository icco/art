# Session Drift Reconciliation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A 10-minute `reconcile` queue job that heals Art's session plan against manual Google Calendar edits — moved/resized blocks update in place (recording the original slot), deleted blocks drop their session, and human-event conflicts make Art yield the slot — recording every change in a `reconcile` AgentRun and structured logs.

**Architecture:** A new `lib/reconcile.Runner` compares `planned` sessions to the synced `events` mirror (pure DB, plus `calendar.Manager.DeleteManaged` for conflict retracts). It runs as a new `reconcile` queue kind. Both `sync` and `reconcile` move to a 10-minute grid; `planner`/`triage` stay hourly, via a new per-kind cadence in the queue. A freshness guard skips destructive actions when the mirror is stale.

**Tech Stack:** Go, GORM + PostgreSQL, the existing `lib/queue` worker, `lib/calendar.Manager` (from PR #45), `gutil` zap logging.

## Global Constraints

- Test DB requires `TEST_DATABASE_URL`; run the suite with `go test -p 1 ./...` (package binaries share one schema).
- `testdb.Open` drops + `AutoMigrate`s from `models.All()`; it does **not** run `lib/db/conn.go` custom migrations. So on a fresh test DB, CHECK constraints come from the model tags (already include `reconcile` after Task 1); the Task 2 migration only matters for the prod upgrade path.
- GORM names field check constraints `chk_<table>_<column>`.
- Match existing style: value/pointer receivers as in neighboring code, `gutillog.FromContext(ctx)` for logging, `datatypes.JSON` for AgentRun summaries.
- Commit message trailer: `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.
- Never force push.

---

### Task 1: Model — reconcile kinds + session planned columns

**Files:**
- Modify: `lib/models/models.go`
- Test: `lib/models/job_test.go`

**Interfaces:**
- Produces: `models.JobReconcile JobKind = "reconcile"`; `models.AgentRunReconcile AgentRunKind = "reconcile"`; `JobKinds()` returns `[sync, reconcile, planner, triage]`; `Session.PlannedStart, PlannedEnd *time.Time`.

- [ ] **Step 1: Write the failing test**

Add to `lib/models/job_test.go`:

```go
func TestJobKindsIncludeReconcileInOrder(t *testing.T) {
	got := models.JobKinds()
	want := []models.JobKind{models.JobSync, models.JobReconcile, models.JobPlanner, models.JobTriage}
	if len(got) != len(want) {
		t.Fatalf("JobKinds len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("JobKinds[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if !models.JobReconcile.Valid() {
		t.Fatal("JobReconcile should be Valid")
	}
	if !models.AgentRunReconcile.Valid() {
		t.Fatal("AgentRunReconcile should be Valid")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./lib/models/ -run TestJobKindsIncludeReconcileInOrder`
Expected: FAIL — `undefined: models.JobReconcile` (build error).

- [ ] **Step 3: Implement the model changes**

In `lib/models/models.go`, add the constants alongside the existing ones:

```go
	AgentRunPlanner   AgentRunKind = "planner"
	AgentRunTriage    AgentRunKind = "triage"
	AgentRunReconcile AgentRunKind = "reconcile"

	JobSync      JobKind = "sync"
	JobReconcile JobKind = "reconcile"
	JobPlanner   JobKind = "planner"
	JobTriage    JobKind = "triage"
```

Update the two `Valid()` methods:

```go
func (k AgentRunKind) Valid() bool {
	return k == AgentRunPlanner || k == AgentRunTriage || k == AgentRunReconcile
}

func (k JobKind) Valid() bool {
	return k == JobSync || k == JobReconcile || k == JobPlanner || k == JobTriage
}
```

Update `JobKinds()` (order = within-slot execution order):

```go
func JobKinds() []JobKind { return []JobKind{JobSync, JobReconcile, JobPlanner, JobTriage} }
```

Widen the CHECK constraint tags. On `AgentRun.Kind`:

```go
	Kind      AgentRunKind   `gorm:"type:varchar(16);not null;default:'planner';index;check:kind IN ('planner','triage','reconcile')" json:"kind"`
```

On `Job.Kind`:

```go
	Kind        JobKind    `gorm:"type:varchar(16);not null;check:kind IN ('sync','reconcile','planner','triage');index:idx_jobs_one_pending,unique,where:status = 'pending'" json:"kind"`
```

Add the two nullable columns to `Session` (after `ScheduledEnd`):

```go
	PlannedStart   *time.Time    `json:"planned_start,omitempty"`
	PlannedEnd     *time.Time    `json:"planned_end,omitempty"`
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./lib/models/ -run TestJobKindsIncludeReconcileInOrder`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add lib/models/models.go lib/models/job_test.go
git commit -m "feat(models): add reconcile job/agent-run kinds and session planned columns

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 2: Prod migration — widen kind CHECK constraints

**Files:**
- Modify: `lib/db/conn.go`
- Test: `lib/db/conn_test.go` (create if absent)

**Interfaces:**
- Consumes: `models.Job`, `models.AgentRun` (Task 1).
- Produces: `migrateKindConstraints(db *gorm.DB) error`, called from `Open`.

**Why:** `AutoMigrate` creates a missing CHECK but never alters an existing one. Existing prod DBs have `chk_jobs_kind` / `chk_agent_runs_kind` without `reconcile`; inserting a reconcile row would violate them. Fresh test DBs already get the new constraint from the Task 1 tags, so this is prod-upgrade-only. Mirrors the existing `migrateEmailCategories`.

- [ ] **Step 1: Write the failing test**

Create `lib/db/conn_test.go`:

```go
package db

import (
	"testing"

	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/testdb"
)

func TestMigrateKindConstraintsIdempotent(t *testing.T) {
	db := testdb.Open(t)
	// Fresh testdb already has the widened constraint from the model tags;
	// the migration must detect that and no-op, twice, without error.
	if err := migrateKindConstraints(db); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := migrateKindConstraints(db); err != nil {
		t.Fatalf("second run: %v", err)
	}
	// A reconcile job must insert cleanly under the constraint.
	if err := db.Create(&models.Job{Kind: models.JobReconcile, Status: models.JobPending, MaxAttempts: 4}).Error; err != nil {
		t.Fatalf("insert reconcile job: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./lib/db/ -run TestMigrateKindConstraintsIdempotent`
Expected: FAIL — `undefined: migrateKindConstraints`.

- [ ] **Step 3: Implement the migration**

In `lib/db/conn.go`, add the call inside `Open` after the existing migrations:

```go
	if err := dropArtCalendarColumn(db); err != nil {
		return nil, fmt.Errorf("drop art calendar column: %w", err)
	}
	if err := migrateKindConstraints(db); err != nil {
		return nil, fmt.Errorf("migrate kind constraints: %w", err)
	}
	return db, nil
```

Add the function:

```go
// migrateKindConstraints widens the jobs.kind and agent_runs.kind CHECK
// constraints to admit 'reconcile'. AutoMigrate creates a missing constraint
// but never alters an existing one, so this is explicit. Idempotent: skips when
// the constraint already admits reconcile, safe on a fresh database.
func migrateKindConstraints(db *gorm.DB) error {
	widen := func(model any, table, name, def string) error {
		m := db.Migrator()
		if m.HasConstraint(model, name) {
			var cur string
			if err := db.Raw(
				`SELECT pg_get_constraintdef(oid) FROM pg_constraint
				 WHERE conname = ? AND conrelid = ?::regclass`, name, table).
				Scan(&cur).Error; err != nil {
				return err
			}
			if cur != "" && strings.Contains(cur, "reconcile") {
				return nil
			}
			if err := m.DropConstraint(model, name); err != nil {
				return err
			}
		}
		return db.Exec(def).Error
	}
	if err := widen(&models.Job{}, "jobs", "chk_jobs_kind",
		`ALTER TABLE jobs ADD CONSTRAINT chk_jobs_kind
		 CHECK (kind IN ('sync','reconcile','planner','triage'))`); err != nil {
		return err
	}
	return widen(&models.AgentRun{}, "agent_runs", "chk_agent_runs_kind",
		`ALTER TABLE agent_runs ADD CONSTRAINT chk_agent_runs_kind
		 CHECK (kind IN ('planner','triage','reconcile'))`)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./lib/db/ -run TestMigrateKindConstraintsIdempotent`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add lib/db/conn.go lib/db/conn_test.go
git commit -m "feat(db): widen job/agent-run kind constraints for reconcile

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 3: Queue — per-kind cadence

**Files:**
- Modify: `lib/queue/queue.go`
- Test: `lib/queue/queue_test.go`

**Interfaces:**
- Consumes: `models.JobReconcile` (Task 1).
- Produces: `cadence(kind models.JobKind) time.Duration`; `nextGrid(t time.Time, interval time.Duration) time.Time`. `Finish` chains each kind on its own grid; `claimSQL` orders sync→reconcile→planner→triage.

- [ ] **Step 1: Write the failing test**

Add to `lib/queue/queue_test.go` (fixedQueue's `now` is `2026-07-04 10:20:00 UTC`):

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./lib/queue/ -run TestFinishChainsPerKindGrid`
Expected: FAIL — reconcile/sync chain to `11:00` (old top-of-hour `nextSlot`), not `10:30`.

- [ ] **Step 3: Implement per-kind cadence**

In `lib/queue/queue.go`, replace `nextSlot`:

```go
// cadence is how often a kind repeats: sync and reconcile run every 10 minutes
// so manual calendar edits are caught quickly; planner and triage run hourly.
func cadence(kind models.JobKind) time.Duration {
	switch kind {
	case models.JobSync, models.JobReconcile:
		return 10 * time.Minute
	default:
		return time.Hour
	}
}

// nextGrid returns the next interval-aligned slot after t, in UTC.
func nextGrid(t time.Time, interval time.Duration) time.Time {
	return t.UTC().Truncate(interval).Add(interval)
}
```

In `Finish`, change the chained-run construction:

```go
		next := models.Job{Kind: job.Kind, Status: models.JobPending, RunAt: nextGrid(now, cadence(job.Kind)), MaxAttempts: maxAttempts}
```

In `claimSQL`, update the ordering `CASE`:

```go
	ORDER BY run_at, CASE kind WHEN 'sync' THEN 0 WHEN 'reconcile' THEN 1 WHEN 'planner' THEN 2 ELSE 3 END
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./lib/queue/ -run 'TestFinish'`
Expected: PASS (new `TestFinishChainsPerKindGrid`; existing `TestFinishTerminalChainsNextSlot` still passes — planner still chains to 11:00).

- [ ] **Step 5: Commit**

```bash
git add lib/queue/queue.go lib/queue/queue_test.go
git commit -m "feat(queue): per-kind cadence, 10-min grid for sync and reconcile

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 4: Worker — dispatch the reconcile kind

**Files:**
- Modify: `lib/queue/worker.go`
- Test: `lib/queue/worker_test.go`

**Interfaces:**
- Consumes: `models.JobReconcile` (Task 1).
- Produces: `ReconcileService interface { Run(ctx context.Context) error }`; `Worker.Reconcile ReconcileService`; new signature `New(db, sync SyncService, reconcile ReconcileService, planner PlannerService, triage TriageService) *Worker`.

- [ ] **Step 1: Update the worker test to the new order and fake**

In `lib/queue/worker_test.go`, add a reconcile fake after `triageFake`:

```go
// reconcileFake records the reconcile pass in the shared order slice.
type reconcileFake struct{ f *fakeServices }

func (r reconcileFake) Run(context.Context) error {
	r.f.order = append(r.f.order, "reconcile")
	if r.f.panicKind == models.JobReconcile {
		panic("reconcile kaboom")
	}
	return nil
}
```

Update `testWorker`:

```go
func testWorker(t *testing.T, f *fakeServices) *Worker {
	t.Helper()
	return New(testdb.Open(t), f, reconcileFake{f}, f, triageFake{f})
}
```

Update `TestDrainRunsDueJobsInOrder`'s expectations:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./lib/queue/ -run TestDrainRunsDueJobsInOrder`
Expected: FAIL — `New` takes 4 args (build error / too many arguments).

- [ ] **Step 3: Implement the worker changes**

In `lib/queue/worker.go`, add the interface to the `type (...)` block:

```go
	// ReconcileService heals sessions against the synced calendar mirror.
	ReconcileService interface {
		Run(ctx context.Context) error
	}
```

Add the field to `Worker` (after `Sync`):

```go
	Queue     *Queue
	Sync      SyncService
	Reconcile ReconcileService
	Planner   PlannerService
	Triage    TriageService
```

Update `New`:

```go
func New(db *gorm.DB, sync SyncService, reconcile ReconcileService, planner PlannerService, triage TriageService) *Worker {
	return &Worker{
		Queue:     &Queue{DB: db},
		Sync:      sync,
		Reconcile: reconcile,
		Planner:   planner,
		Triage:    triage,
		poke:      make(chan struct{}, 1),
		stop:      make(chan struct{}),
	}
}
```

Add the dispatch case in `execute` (after `JobSync`):

```go
	case models.JobReconcile:
		return "", w.Reconcile.Run(ctx)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./lib/queue/`
Expected: PASS (all queue + worker tests).

- [ ] **Step 5: Commit**

```bash
git add lib/queue/worker.go lib/queue/worker_test.go
git commit -m "feat(queue): dispatch the reconcile job kind

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 5: The reconcile Runner

**Files:**
- Create: `lib/reconcile/reconcile.go`
- Test: `lib/reconcile/reconcile_test.go`

**Interfaces:**
- Consumes: `models.Session/Event/SyncState/AgentRun`, `models.SessionPlanned`, `models.AgentRunReconcile`, `calendar.HistoryWindow`, `calendar.FutureWindow`.
- Produces: `type Runner struct { DB *gorm.DB; Cal CalendarService; TZ *time.Location; Now func() time.Time }` with `Run(ctx context.Context) error`; `type CalendarService interface { DeleteManaged(ctx context.Context, account models.AccountKind, calendarID, eventID string) error }`.

- [ ] **Step 1: Write the failing tests**

Create `lib/reconcile/reconcile_test.go`:

```go
package reconcile

import (
	"context"
	"testing"
	"time"

	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/testdb"
	"gorm.io/gorm"
)

type fakeCal struct {
	calls []string
	err   error
}

func (f *fakeCal) DeleteManaged(_ context.Context, account models.AccountKind, calendarID, eventID string) error {
	f.calls = append(f.calls, string(account)+" "+calendarID+" "+eventID)
	return f.err
}

// fixedNow anchors the pass; sessions sit comfortably inside the sync window.
var fixedNow = time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

func newRunner(t *testing.T, cal CalendarService) (*Runner, *gorm.DB) {
	t.Helper()
	db := testdb.Open(t)
	// A fresh sync so the freshness guard passes.
	synced := fixedNow.Add(-time.Minute)
	if err := db.Create(&models.SyncState{AccountKind: models.AccountPersonal, CalendarID: "primary", LastSyncedAt: &synced}).Error; err != nil {
		t.Fatal(err)
	}
	return &Runner{DB: db, Cal: cal, TZ: time.UTC, Now: func() time.Time { return fixedNow }}, db
}

func eid(s string) *string { return &s }

func seedSession(t *testing.T, db *gorm.DB, evID string, start time.Time) models.Session {
	t.Helper()
	s := models.Session{
		Source: models.SourceProject, SourceID: "00000000-0000-0000-0000-000000000001",
		AccountKind: models.AccountPersonal, CalendarID: "primary", GoogleEventID: eid(evID),
		ScheduledStart: start, ScheduledEnd: start.Add(time.Hour), Status: models.SessionPlanned,
	}
	if err := db.Create(&s).Error; err != nil {
		t.Fatal(err)
	}
	return s
}

func seedEvent(t *testing.T, db *gorm.DB, evID string, start time.Time, artManaged bool) {
	t.Helper()
	e := models.Event{
		AccountKind: models.AccountPersonal, CalendarID: "primary", GoogleEventID: evID,
		StartTime: start, EndTime: start.Add(time.Hour), Status: "confirmed",
		EventType: "default", IsArtManaged: artManaged,
	}
	if err := db.Create(&e).Error; err != nil {
		t.Fatal(err)
	}
}

func latestRun(t *testing.T, db *gorm.DB) models.AgentRun {
	t.Helper()
	var run models.AgentRun
	if err := db.Where("kind = ?", models.AgentRunReconcile).Order("started_at DESC").First(&run).Error; err != nil {
		t.Fatal(err)
	}
	return run
}

func TestReconcileHealsMovedSession(t *testing.T) {
	r, db := newRunner(t, &fakeCal{})
	planned := fixedNow.Add(24 * time.Hour)
	s := seedSession(t, db, "ev-move", planned)
	moved := planned.Add(3 * time.Hour)
	seedEvent(t, db, "ev-move", moved, true)

	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	var got models.Session
	if err := db.First(&got, "id = ?", s.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !got.ScheduledStart.Equal(moved) {
		t.Fatalf("scheduled_start = %v, want healed %v", got.ScheduledStart, moved)
	}
	if got.PlannedStart == nil || !got.PlannedStart.Equal(planned) {
		t.Fatalf("planned_start = %v, want original %v", got.PlannedStart, planned)
	}
	run := latestRun(t, db)
	if run.Status != models.AgentRunSucceeded {
		t.Fatalf("run status = %v", run.Status)
	}
}

func TestReconcileDeletesOrphanSession(t *testing.T) {
	r, db := newRunner(t, &fakeCal{})
	s := seedSession(t, db, "ev-gone", fixedNow.Add(24*time.Hour))
	// No matching event row -> deleted upstream.

	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Model(&models.Session{}).Where("id = ?", s.ID).Count(&n)
	if n != 0 {
		t.Fatalf("orphan session should be deleted, %d remain", n)
	}
}

func TestReconcileRetractsOnHumanConflict(t *testing.T) {
	cal := &fakeCal{}
	r, db := newRunner(t, cal)
	start := fixedNow.Add(24 * time.Hour)
	s := seedSession(t, db, "ev-art", start)
	seedEvent(t, db, "ev-art", start, true)       // the session's own art event
	seedEvent(t, db, "ev-human", start, false)    // overlapping human event

	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(cal.calls) != 1 || cal.calls[0] != "personal primary ev-art" {
		t.Fatalf("DeleteManaged calls = %v, want one for ev-art", cal.calls)
	}
	var n int64
	db.Model(&models.Session{}).Where("id = ?", s.ID).Count(&n)
	if n != 0 {
		t.Fatalf("conflicting session should be retracted, %d remain", n)
	}
}

func TestReconcileIgnoresOwnEventNoConflict(t *testing.T) {
	cal := &fakeCal{}
	r, db := newRunner(t, cal)
	start := fixedNow.Add(24 * time.Hour)
	s := seedSession(t, db, "ev-art", start)
	seedEvent(t, db, "ev-art", start, true) // only its own art event overlaps

	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(cal.calls) != 0 {
		t.Fatalf("no conflict expected, got DeleteManaged %v", cal.calls)
	}
	var n int64
	db.Model(&models.Session{}).Where("id = ?", s.ID).Count(&n)
	if n != 1 {
		t.Fatalf("session should survive, %d remain", n)
	}
}

func TestReconcileSkipsWhenSyncStale(t *testing.T) {
	cal := &fakeCal{}
	db := testdb.Open(t)
	stale := fixedNow.Add(-2 * time.Hour)
	if err := db.Create(&models.SyncState{AccountKind: models.AccountPersonal, CalendarID: "primary", LastSyncedAt: &stale}).Error; err != nil {
		t.Fatal(err)
	}
	r := &Runner{DB: db, Cal: cal, TZ: time.UTC, Now: func() time.Time { return fixedNow }}
	s := seedSession(t, db, "ev-gone", fixedNow.Add(24*time.Hour)) // would be deleted if fresh

	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Model(&models.Session{}).Where("id = ?", s.ID).Count(&n)
	if n != 1 {
		t.Fatalf("stale pass must not delete, %d remain", n)
	}
	run := latestRun(t, db)
	if got := string(run.Summary); !contains(got, "\"skipped_stale\":true") {
		t.Fatalf("summary = %s, want skipped_stale true", got)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./lib/reconcile/`
Expected: FAIL — package/`Runner` not defined (build error).

- [ ] **Step 3: Implement the Runner**

Create `lib/reconcile/reconcile.go`:

```go
// Package reconcile heals Art's session plan against the synced calendar mirror:
// it updates moved blocks, drops deleted ones, and retracts blocks a human event
// now overlaps.
package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/icco/art/lib/calendar"
	"github.com/icco/art/lib/models"
	gutillog "github.com/icco/gutil/logging"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// freshness bounds how stale the events mirror may be before reconcile refuses
// destructive actions: 3x the 10-minute sync cadence.
const freshness = 30 * time.Minute

// CalendarService retracts an Art-managed calendar event; satisfied by
// *calendar.Manager.
type CalendarService interface {
	DeleteManaged(ctx context.Context, account models.AccountKind, calendarID, eventID string) error
}

// Runner heals planned sessions against the events mirror on each pass.
type Runner struct {
	DB  *gorm.DB
	Cal CalendarService
	TZ  *time.Location
	// Now is injectable for tests; nil means time.Now.
	Now func() time.Time
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Run performs one reconcile pass and records it as a reconcile AgentRun.
func (r *Runner) Run(ctx context.Context) error {
	run := models.AgentRun{Kind: models.AgentRunReconcile, StartedAt: r.now(), Status: models.AgentRunRunning}
	if err := r.DB.WithContext(ctx).Create(&run).Error; err != nil {
		return err
	}
	summary := map[string]any{"moved": 0, "deleted": 0, "conflicts": 0, "skipped_stale": false}
	runErr := r.reconcile(ctx, summary)
	return r.finish(ctx, run.ID, summary, runErr)
}

func (r *Runner) reconcile(ctx context.Context, summary map[string]any) error {
	log := gutillog.FromContext(ctx)
	now := r.now()
	if !r.mirrorFresh(ctx, now) {
		summary["skipped_stale"] = true
		log.Warnw("reconcile skipped: sync mirror stale")
		return nil
	}

	windowStart := now.Add(-calendar.HistoryWindow)
	windowEnd := now.Add(calendar.FutureWindow)

	var sessions []models.Session
	if err := r.DB.WithContext(ctx).
		Where("status = ? AND google_event_id IS NOT NULL AND scheduled_start >= ? AND scheduled_start < ?",
			models.SessionPlanned, windowStart, windowEnd).
		Find(&sessions).Error; err != nil {
		return err
	}
	for _, s := range sessions {
		if err := r.reconcileOne(ctx, summary, s); err != nil {
			return err
		}
	}
	return nil
}

// mirrorFresh reports whether some calendar synced within the freshness window.
func (r *Runner) mirrorFresh(ctx context.Context, now time.Time) bool {
	var latest *time.Time
	if err := r.DB.WithContext(ctx).Model(&models.SyncState{}).
		Select("max(last_synced_at)").Scan(&latest).Error; err != nil {
		return false
	}
	return latest != nil && now.Sub(*latest) <= freshness
}

func (r *Runner) reconcileOne(ctx context.Context, summary map[string]any, s models.Session) error {
	log := gutillog.FromContext(ctx)

	var ev models.Event
	err := r.DB.WithContext(ctx).
		Where("account_kind = ? AND calendar_id = ? AND google_event_id = ?",
			s.AccountKind, s.CalendarID, *s.GoogleEventID).
		First(&ev).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if delErr := r.DB.WithContext(ctx).Delete(&models.Session{}, "id = ?", s.ID).Error; delErr != nil {
			return delErr
		}
		log.Infow("session drift: deleted", "session", s.ID)
		inc(summary, "deleted")
		return nil
	}
	if err != nil {
		return err
	}

	if !ev.StartTime.Equal(s.ScheduledStart) || !ev.EndTime.Equal(s.ScheduledEnd) {
		updates := map[string]any{"scheduled_start": ev.StartTime, "scheduled_end": ev.EndTime}
		if s.PlannedStart == nil {
			ps, pe := s.ScheduledStart, s.ScheduledEnd
			updates["planned_start"] = &ps
			updates["planned_end"] = &pe
		}
		if upErr := r.DB.WithContext(ctx).Model(&models.Session{}).Where("id = ?", s.ID).Updates(updates).Error; upErr != nil {
			return upErr
		}
		log.Infow("session drift: moved", "session", s.ID, "from", s.ScheduledStart, "to", ev.StartTime)
		inc(summary, "moved")
		s.ScheduledStart, s.ScheduledEnd = ev.StartTime, ev.EndTime
	}

	conflict, err := r.hasHumanConflict(ctx, s)
	if err != nil {
		return err
	}
	if conflict {
		if delErr := r.Cal.DeleteManaged(ctx, s.AccountKind, s.CalendarID, *s.GoogleEventID); delErr != nil {
			return delErr
		}
		if delErr := r.DB.WithContext(ctx).Delete(&models.Session{}, "id = ?", s.ID).Error; delErr != nil {
			return delErr
		}
		log.Infow("session drift: conflict retract", "session", s.ID, "event", *s.GoogleEventID)
		inc(summary, "conflicts")
	}
	return nil
}

// hasHumanConflict reports whether a non-Art busy event overlaps the session,
// using the same busy predicate as the planner's loadBusy.
func (r *Runner) hasHumanConflict(ctx context.Context, s models.Session) (bool, error) {
	var n int64
	err := r.DB.WithContext(ctx).Model(&models.Event{}).
		Where(`account_kind = ? AND is_art_managed = false AND status <> 'cancelled'
		       AND (all_day = false OR event_type = 'outOfOffice')
		       AND end_time > ? AND start_time < ?`,
			s.AccountKind, s.ScheduledStart, s.ScheduledEnd).
		Count(&n).Error
	return n > 0, err
}

func (r *Runner) finish(ctx context.Context, id string, summary map[string]any, runErr error) error {
	status := models.AgentRunSucceeded
	errStr := ""
	if runErr != nil {
		status = models.AgentRunFailed
		errStr = runErr.Error()
	}
	body, _ := json.Marshal(summary)
	t := r.now()
	if err := r.DB.WithContext(context.WithoutCancel(ctx)).Model(&models.AgentRun{}).Where("id = ?", id).Updates(map[string]any{
		"ended_at": &t,
		"status":   string(status),
		"summary":  datatypes.JSON(body),
		"error":    errStr,
	}).Error; err != nil {
		return errors.Join(runErr, err)
	}
	return runErr
}

func inc(summary map[string]any, key string) {
	if v, ok := summary[key].(int); ok {
		summary[key] = v + 1
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./lib/reconcile/`
Expected: PASS (all five tests).

- [ ] **Step 5: Commit**

```bash
git add lib/reconcile/reconcile.go lib/reconcile/reconcile_test.go
git commit -m "feat(reconcile): heal sessions against the synced calendar

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

### Task 6: Wire the reconciler into the server

**Files:**
- Modify: `main.go`

**Interfaces:**
- Consumes: `reconcile.Runner` (Task 5), `calendar.Manager`, the new `queue.New` signature (Task 4).

- [ ] **Step 1: Implement the wiring**

In `main.go`, add the import `"github.com/icco/art/lib/reconcile"` (keep imports grouped/sorted), then construct the reconciler and update `queue.New`:

```go
	syncRunner := &calendar.Runner{DB: gdb, OAuth: oauthFlow, TZ: cfg.Timezone}
	reconciler := &reconcile.Runner{DB: gdb, Cal: &calendar.Manager{OAuth: oauthFlow}, TZ: cfg.Timezone}
	planner := &agent.Planner{Cfg: cfg, DB: gdb, OAuth: oauthFlow}
	triager := &email.Runner{Cfg: cfg, DB: gdb, OAuth: oauthFlow}

	worker := queue.New(gdb, syncRunner, reconciler, planner, triager)
```

- [ ] **Step 2: Verify the build**

Run: `go build ./...`
Expected: success (no output).

- [ ] **Step 3: Full suite + vet + gofmt**

Run:
```bash
go vet ./...
test -z "$(gofmt -l .)" || { gofmt -l .; exit 1; }
go test -p 1 ./...
```
Expected: vet clean, gofmt clean, all packages `ok`.

- [ ] **Step 4: Commit**

```bash
git add main.go
git commit -m "feat: run the reconcile job in the worker

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Self-Review

**Spec coverage:**
- Cadence & placement (new reconcile kind; sync+reconcile 10-min; sync→reconcile→planner→triage) → Tasks 1, 3, 4.
- Per-kind cadence in the queue → Task 3.
- Scope (planned + GoogleEventID, all calendars/accounts, sync-window bound) → Task 5 `reconcile` query.
- Move heal + record original (`PlannedStart/End`) → Task 1 (columns) + Task 5 (`reconcileOne`).
- Delete orphan session → Task 5.
- Human-event conflict retract via `DeleteManaged`, own-event excluded → Task 5 (`hasHumanConflict`, tests cover both).
- Freshness guard → Task 5 (`mirrorFresh`, stale test).
- Audit: reconcile AgentRun summary + per-change logs → Task 5 (`Run`/`finish`, `log.Infow` per change); AgentRun kind + constraint → Tasks 1, 2.
- Components (`lib/reconcile`, model fields, queue/worker/main wiring) → Tasks 1–6.
- Rollout (AutoMigrate columns; Seed adds reconcile; constraint migration) → Task 1 (columns via AutoMigrate, Seed uses `JobKinds()`), Task 2 (constraints).

**Placeholder scan:** none — every step has concrete code and commands.

**Type consistency:** `CalendarService.DeleteManaged(ctx, account models.AccountKind, calendarID, eventID string) error` matches `calendar.Manager` (PR #45) and the Task 5 fake; `New(db, sync, reconcile, planner, triage)` argument order matches Task 4's worker fields and Task 6's call; `cadence`/`nextGrid` names match Task 3's usage in `Finish`; summary keys (`moved`/`deleted`/`conflicts`/`skipped_stale`) are consistent between `Run` and the tests.
