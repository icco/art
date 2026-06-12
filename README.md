# art

Personal scheduling agent. Blocks focus time on work + personal Google
Calendars for **tasks** (one-off to-dos: "pack office 2h by friday"),
**projects** (target hours toward a deadline), and **habits** (recurring
practice). Writes new events only — never modifies human events.

## Layout

- `./` — server: chi/v5, GORM/Postgres, Google OIDC, Prometheus, cron.
- `./cmd/art` — Bubble Tea TUI + one-shot CLI (`art add`, `art status`).

Art-created events use `eventType=focusTime` with `art_managed=true` in
extended properties. Both Google accounts must be on Workspace.

## Architecture

```
+----------+      +---------+      +-------------------+
| art TUI  |----->|  server |----->| Google Calendar   |
+----------+ ID   +----+----+      +---------+---------+
              tok      |                     ^
                       v                     | new focusTime events
                  +----+----+                |
                  | Postgres|          +-----+-----+
                  +---------+--planner-+  hourly   |
                                       |   cron    |
                                       +-----------+
```

Schema is owned by `gorm.AutoMigrate` over `lib/models`. UUID PKs are
generated in Go (`BeforeCreate` + `google/uuid`).

Each cron tick (default hourly, `ART_CRON_INTERVAL`) runs **sync →
reconcile → plan**:

- **Sync** pulls events from every linked calendar incrementally.
- **Reconcile** treats your calendar edits as signal: deleting an art
  event skips the session and rebooks the work, dragging one updates the
  session, a meeting landing on a future block reschedules it, and
  finished blocks are marked happened.
- **Plan** books focus blocks inside a rolling 14-day window, deadline
  first. Tasks get one contiguous block when possible, split into ≥1h
  chunks only when needed, and are refused (status `unschedulable`, with
  nearest alternatives reported) rather than partially booked when they
  can't fit before their deadline. Busy time on *every* account blocks
  every placement, and absence-style all-day events (OOO/vacation/PTO)
  block their whole day.

The planner is deterministic Go by default and needs no GCP credentials.
Set `ART_PLANNER=llm` (+ `VERTEX_PROJECT_ID`, optionally `VERTEX_MODEL`)
to use the ADK/Gemini planner instead; it falls back to deterministic on
failure.

## Setup

```sh
docker compose up -d db          # Postgres
cp .env.example .env             # fill in
task run                         # server
```

Then link calendars (browser-consent each one):

```sh
TOKEN="$(gcloud auth print-identity-token --audiences=$OIDC_AUDIENCE)"
curl -s -X POST "http://localhost:8080/oauth/start?account=personal" \
  -H "Authorization: Bearer $TOKEN" | jq -r .url
# Repeat for ?account=work.
```

Launch the TUI and set working hours on the `hours` screen (key `5`), or
use the API (`PUT /working-hours`).

## Daily use

```sh
task build
export ART_API_URL=http://localhost:8080 ART_API_AUDIENCE="$OIDC_AUDIENCE"

./bin/art add "pack office 2h by friday"   # capture a task
./bin/art add "write review 90m #work"     # tag work tasks; default is #personal
./bin/art status                           # upcoming blocks, open tasks, last run
./bin/art                                  # interactive TUI
```

Quick-add grammar: a duration (`2h`, `90m`, `1h30m`, `1.5h`; default 1h),
an optional deadline (`by fri`, `by tomorrow`, `by eow`, `by 6/15`,
`by 2026-06-20`), and `#work`/`#personal`. Everything else is the title.

In the TUI: `1`–`5` switch screens (week/projects/habits/tasks/hours),
`n` quick-adds from anywhere, `r` replans, `s` syncs.

Install via Homebrew once released:

```sh
brew install icco/tap/art
```

## Out of scope for v1

Multi-user, mobile/web UI, push notifications, Sentry/OTel exporters,
service-account domain delegation.

## End-to-end smoke

1. Both accounts linked, working hours saved (TUI key `5`).
2. `POST /sync` populates `events`.
3. `art add "pack office 2h by friday"` — task created.
4. `POST /replan` (or wait for the cron) — `agent_runs` succeeds;
   `sessions` has new `planned` rows; the personal calendar has a new
   `focusTime` event with `art_managed=true`.
5. Delete that event in Google Calendar, replan — the session flips to
   `skipped` and a replacement block appears.
6. Add a conflicting meeting over a block, replan — Art moves its block
   and leaves the meeting untouched.
7. `art status` shows the blocks; the TUI tasks screen tracks status.
