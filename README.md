# art

Personal scheduling agent. Blocks focus time on work + personal Google
Calendars for **projects** (target hours toward a deadline) and **habits**
(recurring practice). Writes new events only — never modifies human events.

## Layout

- `./` — server: chi/v5, GORM/Postgres, Google OIDC, Prometheus, hourly cron.
- `./cmd/art` — Bubble Tea TUI.

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

The planner is deterministic Go today. ADK/Vertex orchestration can wrap
the same primitives later.

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

Set working hours (minutes past midnight in `ART_TIMEZONE`):

```sh
curl -s -X PUT http://localhost:8080/working-hours \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '[{"slot_kind":"work","day_of_week":1,"start_minute":540,"end_minute":1080}, ...]'
```

Launch the TUI:

```sh
task build
ART_API_URL=http://localhost:8080 ART_API_AUDIENCE="$OIDC_AUDIENCE" ./bin/art
```

Install via Homebrew once released:

```sh
brew install icco/tap/art
```

## Out of scope for v1

LLM-orchestrated planning, multi-user, mobile/web UI, push notifications,
Sentry/OTel exporters, service-account domain delegation.

## End-to-end smoke

1. Both accounts linked, working hours saved.
2. `POST /sync` populates `events`.
3. `POST /projects` (`target_hours`, `deadline`, `kind`).
4. `POST /replan` — `agent_runs` succeeds; `sessions` has new `planned`
   rows; calendar has new `focusTime` events with `art_managed=true`.
5. Add a conflicting meeting manually, replan — Art slides to the next
   free slot and leaves the meeting untouched.
6. TUI shows the blocks; `r` triggers replan.
