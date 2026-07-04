# DB-backed job queue design

2026-07-04. Replaces the in-memory `lib/cron` ticker with a Postgres-backed queue and async worker so restarts don't reset or duplicate scheduled work.

## Problem

`lib/cron/scheduler.go` runs sync, planner, and triage via an in-memory hourly `time.Ticker`, with an immediate run at boot. Nothing persists, so every restart fires an extra run and resets the phase, and downtime silently drops runs. The cron "isn't always firing."

## Decisions

- Catch up if overdue: on boot, run anything whose scheduled time passed while down — exactly once, regardless of downtime length.
- In-process workers: no new binary or container; the server process runs the worker.
- Manual triggers (`POST /sync`, `/replan`, `/triage/run`) enqueue into the same queue.
- Retries: up to 3 with exponential backoff (1m, 5m, 25m), then failed until the next scheduled run.
- Hand-rolled thin queue on GORM (no River/gue): the claim primitive is ~30 lines of SQL, and no library provides persistent recurring schedules with catch-up — River's periodic jobs are in-memory and reset on restart, the exact bug being fixed.

## Data model

New GORM model `Job` in `lib/models`, added to `models.All()`:

- `Base` (uuid, created/updated)
- `Kind` — `sync` | `planner` | `triage` (varchar + CHECK, matching existing enum style)
- `Status` — `pending` | `running` | `succeeded` | `failed`
- `RunAt time.Time` (indexed), `StartedAt`, `FinishedAt` (`*time.Time`)
- `Attempts int`, `MaxAttempts int` (default 4 = first try + 3 retries), `LastError string`
- Partial unique index: at most one `pending` job per kind

## Scheduling

No schedules table; recurrence is chained and pinned to clock times:

- When a job reaches a terminal status (succeeded or final failure), the worker enqueues the next pending job of that kind at the next grid slot after now: `now.Truncate(time.Hour).Add(time.Hour)` in UTC. Per-kind intervals are code constants (all hourly today).
- Boot seeder: ensure each kind has a pending job; seed with `run_at = now()` if none (first deploy runs immediately).
- Boot reaper: any `running` row is an orphan from a crashed process (single-process deployment) — reset to `pending` with `run_at = now()`. Attempts were incremented at claim, so a crash-looping job still exhausts `MaxAttempts`.
- Catch-up falls out of `run_at <= now()`: an overdue pending row runs once at boot, then the chain snaps back to the grid.
- Manual runs, retries, and catch-up runs execute off-grid; the next scheduled run always lands on the grid.

## Execution

New `lib/queue` package replacing `lib/cron`:

- Single worker goroutine (concurrency 1), polling every ~15s; manual enqueues ping an in-process channel to skip the poll wait.
- Atomic claim via GORM `Raw`:

```sql
UPDATE jobs SET status='running', started_at=now(), attempts=attempts+1
WHERE id = (
  SELECT id FROM jobs
  WHERE status='pending' AND run_at <= now()
  ORDER BY run_at, CASE kind WHEN 'sync' THEN 0 WHEN 'planner' THEN 1 ELSE 2 END
  LIMIT 1 FOR UPDATE SKIP LOCKED
)
RETURNING *
```

- The `CASE kind` ordering plus concurrency 1 preserves today's sync → planner → triage sequence within a shared slot, so the planner sees freshly synced events. `SKIP LOCKED` future-proofs a second worker or replica.
- Each job runs under a 30-minute timeout context (per-job version of today's `runOnceTimeout`) with panic recovery; a panic is a failure.
- Failure with attempts remaining: back to `pending`, `run_at = now + backoff`. Success or final failure: terminal status, `last_error` recorded, next recurring job chained.
- Per-account sync errors (today a warning, not a failure) keep that semantic: the job succeeds with the error map serialized into `last_error`; only a top-level error triggers retries.
- Shutdown: SIGTERM cancels the main ctx; the in-flight job aborts, is marked failed-with-retry, and `Stop()` waits for the worker goroutine. Graceful restarts and hard crashes converge on the same recovery path (retry row or reaper).

## API and TUI

- `POST /sync`, `/replan`, `/triage/run` become thin enqueues returning `202 {status: "queued", job: {...}}`. If a pending job of that kind exists, pull its `run_at` to now instead of inserting; if one is running, return `{status: "running"}`. The detached-goroutine + agent-runs in-flight guard code in replan/triage handlers is deleted.
- New `GET /jobs`: recent jobs, filterable by kind/status, with a limit.
- TUI: replan/triage flows unchanged (already detached + polled). Sync switches from synchronous to the same await pattern, polling `GET /jobs` until its job is terminal, then reloading. Per-account sync errors move from the POST response body to the job's `last_error`.

## Observability

- Jobs table is the source of truth; `agent_runs` unchanged (runners keep writing it).
- Prometheus: `art_jobs_processed_total{kind, status}`, `art_job_duration_seconds{kind}`.
- Structured logs on job start/finish/retry via gutil logging.

## Testing

- `lib/queue` tests against `lib/testdb` (`TEST_DATABASE_URL`, `-p 1`): claim ordering/atomicity, retry backoff and exhaustion, grid chaining, boot seeding, orphan reaping, catch-up, enqueue dedupe. Runners injected as funcs so tests never hit Google/Vertex.
- Handler tests for the three trigger endpoints and `GET /jobs`.
- Maintain the 50% coverage gate.

## Cleanup

- Delete `lib/cron` and its tests; `main.go` wires `queue` with the same graceful-shutdown shape.
- No deploy changes on mist: same single container, same env.

## Out of scope

- Multi-process workers (would need lease/heartbeat instead of the boot reaper).
- Per-kind configurable intervals via env; constants are fine until proven otherwise.
- A TUI jobs page beyond what the sync await needs.
