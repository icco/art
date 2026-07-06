# Session Drift Reconciliation — Design

**Date:** 2026-07-06
**Status:** Approved, pending implementation plan
**Branch:** `feat/drift-reconciliation`

## Problem

Art creates focus-block *sessions* and mirrors Google Calendar into an *events*
table, but the two never reconcile. When the owner manually **moves**,
**resizes**, or **deletes** an Art-managed block in Google Calendar — or drops a
**human event on top of** an Art block — Art notices nothing:

- `sync` (`lib/calendar/sync.go`) only maintains the `events` mirror; it has zero
  references to `sessions`.
- No code compares `events` ↔ `sessions`. The `SessionMoved` / `SessionHappened`
  statuses are defined in the model but never assigned (dead code).
- The planner's `loadBusy` treats both the stale planned session (old time) and
  the moved mirror event (new time) as busy, so a moved block silently blocks two
  slots and its plan drifts from reality.

There is no record or log of any of this.

## Goal

On a short cadence, detect divergence between Art's plan and the actual calendar,
**heal the plan to match reality**, retract Art blocks that now conflict with
human events, and **record every change** durably and in logs.

## Non-goals

- Enforcing the one-habit-per-day rule against *manual* moves — human edits are
  respected; conflicts with human events are logged, not overridden beyond
  yielding the slot.
- Partial-overlap subtlety: any overlap with a human event yields the whole Art
  block (no attempt to shrink/split).
- Reviving the dead `moved` session status — it stays unused; moves heal in place.
- Detecting changes outside the sync window (see Scope).

## Cadence and placement

A new **`reconcile` queue job**. Both **sync and reconcile move to a 10-minute
grid**; planner and triage stay hourly.

Ordering when kinds share a slot (top of the hour): **sync → reconcile → planner
→ triage**, so the mirror is fresh before reconcile, and the plan is corrected
before the planner runs.

This requires generalizing the queue's fixed top-of-hour scheduling into a
per-kind cadence:

- `cadence(kind) time.Duration`: `sync=10m`, `reconcile=10m`, `planner=1h`,
  `triage=1h`.
- `nextGrid(t, interval) = t.UTC().Truncate(interval).Add(interval)` replaces the
  hardcoded `nextSlot` in `Queue.Finish`. `nextSlot` becomes `nextGrid(t, time.Hour)`.
- Claim ordering `CASE` in `claimSQL` extends to
  `sync=0, reconcile=1, planner=2, triage=3`.
- `Seed` and boot catch-up already iterate `JobKinds()`, so adding `reconcile`
  there gives it a pending row and one exactly-once catch-up on boot.

Sync is incremental (sync-token deltas only), so a 10-minute cadence is cheap in
Google API terms.

## Scope

Reconcile considers **planned** sessions that have a non-null `GoogleEventID`,
across **all linked calendars and both accounts** — sessions already carry
`account_kind` + `calendar_id`, so no per-calendar iteration is needed. The prior
"current week only" narrowing is dropped.

Bounded by the **sync window** (`now − HistoryWindow … now + FutureWindow`, the
constants sync already uses): outside it the mirror can't distinguish "deleted
upstream" from "never synced," so a session whose time falls outside the window is
left untouched. A block dragged beyond the sync horizon is a documented
limitation — it is reconciled once it (or the horizon) re-enters the window.

## Reconcile actions

For each in-scope session, match it to its mirror event by
`(account_kind, calendar_id, google_event_id)` and act:

1. **Moved / resized** — mirror event exists but its start/end differs from the
   session's:
   - If `Session.PlannedStart` is null, copy the current
     `ScheduledStart`/`ScheduledEnd` into new nullable columns
     `PlannedStart`/`PlannedEnd` (records the original slot; null = never moved).
   - Update `ScheduledStart`/`ScheduledEnd` to the event's time.
   - Count `moved`. *(pure DB)*

2. **Deleted upstream** — session has a `GoogleEventID` but no mirror row (sync
   drops cancelled/removed events):
   - Delete the session row (matches the retract endpoint). Its habit/project
     frees up and the planner rebooks next pass. Count `deleted`. *(pure DB)*

3. **Human-event conflict** — a non-Art event overlaps the session's *current*
   time:
   - Art yields: retract the block via `calendar.Manager.DeleteManaged`
     (delete the calendar event **and** the row), so the calendar isn't left
     double-booked; the planner rebooks it elsewhere next pass.
   - Count `conflicts`. *(needs OAuth — reuses the `calendar.Manager` added in
     PR #45)*

Per-session evaluation order: handle **deleted** first (if no mirror row, done);
else **heal move**; then evaluate **conflict** against the healed time. A block
moved into a conflict is healed then retracted — net effect is retract, which is
correct.

**"Overlap" / "human event" definition.** Reconcile reuses the planner's
`loadBusy` busy-event predicate (`status <> 'cancelled' AND (all_day = false OR
event_type = 'outOfOffice')`) **plus `is_art_managed = false`**, so a session
never conflicts with its own synced event and "conflict" means exactly what the
planner would have avoided. The overlap test is the existing
`b.end > start && b.start < end`. The shared predicate should be factored into a
small reusable helper rather than duplicated.

## Safety guard: freshness

All destructive actions (delete + conflict-retract) require a trustworthy mirror.
Before acting, reconcile checks that a sync succeeded recently — the maximum
`SyncState.LastSyncedAt` across the owner's linked calendars must be within a
**freshness window** (default 30 min = 3× the 10-minute cadence). If sync is stale
(e.g. a Google outage), reconcile **skips the entire pass** and records
`skipped_stale` — never mass-deleting sessions off a stale mirror. Because a
skipped pass is non-destructive and the healing is idempotent, the next fresh pass
catches up.

## Audit

Both surfaces, per the approved decision:

- **Durable:** one `AgentRun{kind: reconcile}` per pass, with
  `summary {moved, deleted, conflicts, skipped_stale}`. Surfaces in
  `GET /agent-runs` and the TUI runs view with no new UI. Follows the existing
  planner/triage AgentRun pattern (create `running` row, update to
  `succeeded`/`failed` with a JSON summary).
- **Live:** a structured log line per change, e.g.
  `session drift: moved <id> <t1>→<t2>`, `session drift: deleted <id>`,
  `session drift: conflict retract <id> vs <event-id>`.

## Components

- **`lib/models`**
  - `Session.PlannedStart, PlannedEnd *time.Time` (nullable) — added via
    AutoMigrate.
  - `JobReconcile JobKind = "reconcile"`, included in `JobKinds()`.
  - `AgentRunReconcile AgentRunKind = "reconcile"`.

- **`lib/queue`** — `cadence(kind)` + `nextGrid`; `Finish` chains on the
  per-kind grid; `claimSQL` ordering gains `reconcile`; worker dispatch routes
  the `reconcile` kind to the reconcile runner.

- **`lib/reconcile`** (new package) — `Runner{DB *gorm.DB, Cal CalendarService,
  TZ *time.Location, Now func() time.Time}` with `Run(ctx) error`. `CalendarService`
  is the small interface `DeleteManaged(ctx, account, calendarID, eventID) error`
  (satisfied by `*calendar.Manager`), mirroring the handler dependency added in
  #45, so the runner is unit-testable with a fake. Pure DB except conflict-retract.

- **`main.go`** — construct `&reconcile.Runner{DB: gdb, Cal:
  &calendar.Manager{OAuth: oauthFlow}, TZ: cfg.Timezone}`; pass to `queue.New`.

- **`queue.New(...)`** — signature gains the reconcile runner; worker holds it
  alongside sync/planner/triage.

## Data flow (per 10-minute cycle)

```
sync job      → incremental pull → events mirror updated
reconcile job → freshness check → for each in-scope planned session:
                  deleted?  → drop row
                  moved?    → record original + heal time
                  conflict? → DeleteManaged + drop row
                → write AgentRun{reconcile} summary + per-change logs
planner (hourly) → sees corrected sessions, rebooks freed/retracted work
```

## Testing

`lib/reconcile` is unit-testable with `testdb` + a fake `CalendarService`:

- **Moved:** seed a session + a mirror event at a different time → session time
  updated, `PlannedStart/End` populated once, `moved=1`.
- **Deleted:** session with `GoogleEventID`, no mirror row → row gone, `deleted=1`.
- **Conflict:** session + overlapping non-Art event → `DeleteManaged` called, row
  gone, `conflicts=1`; an overlapping *Art* event (its own) does **not** trigger.
- **Stale guard:** `SyncState.LastSyncedAt` old → pass no-ops, `skipped_stale=true`,
  nothing deleted.
- **Out-of-window:** session outside the sync window is ignored.
- **AgentRun:** a `reconcile` run row is written with the expected summary.

`lib/queue` tests: `nextGrid` for 10-minute and hourly intervals; `Finish` chains
`reconcile` onto the 10-minute grid and `planner` onto the hourly grid; claim
order returns sync→reconcile→planner→triage when they share a slot.

## Rollout

- AutoMigrate adds the two nullable columns (no backfill; null = never moved).
- `JobKinds()` gains `reconcile`; `Seed` inserts its pending row on boot.
- Existing pending sync jobs keep their `run_at`; after the next `Finish` they
  chain onto the 10-minute grid.
- No config required; cadence and freshness are constants (a
  `RECONCILE_FRESHNESS` env override is optional, not required for v1).
