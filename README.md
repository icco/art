# art

Art is a personal scheduling agent. It blocks focus time on your work and
personal Google Calendars for **projects** (one-off goals with target hours
and a deadline) and **habits** (recurring practice — walks, music, deep work).

It writes new events only — it never modifies or deletes events you created.

## Design

Two binaries, one repo:

- **server** (`./main.go`) — HTTP API + cron loop. chi/v5 router, Postgres
  via GORM (`gorm.io/gorm` + `gorm.io/driver/postgres`), Google OIDC auth,
  Prometheus metrics. Runs the hourly sync + planner.
- **art** (`./cmd/art`) — Bubble Tea TUI. Talks to the API. Week view,
  project/habit CRUD, replan trigger.

Calendars Art creates events on use Google's native `eventType=focusTime`
and an `art_managed=true` extended property so they're distinguishable from
human events. Both accounts (`work` and `personal`) must be Google Workspace
for `focusTime` support.

## Architecture

```
+--------------+      +---------------+      +-----------------------+
|  art (TUI)   |----->|   server      |----->|  Google Calendar API  |
| Bubble Tea   |  ID  | chi + GORM    |      |  (per-account OAuth)  |
+--------------+ tok  +-------+-------+      +-----------+-----------+
                              |                          ^
                              v                          | inserts new
                       +------+------+                   | focusTime events
                       |  Postgres   |                   |
                       |  events,    |                   |
                       |  projects,  |              +----+----+
                       |  habits,    |--planner---->|  Hourly |
                       |  sessions,  |   reads      |  cron   |
                       |  oauth toks |              +---------+
                       +-------------+
```

Schema is owned by `gorm.AutoMigrate` over the structs in `lib/models`. UUID
primary keys are generated in Go via a `BeforeCreate` hook so no Postgres
extensions are required.

The planner is currently deterministic Go (sized blocks per project until
deadline; weekly cadence for habits). LLM-orchestrated planning via Google
ADK on Vertex is left as a follow-up enhancement once non-deterministic
priority calls become useful — the same Go primitives can become tools.

## Setup

### 1. Postgres

```sh
docker compose up -d db
```

The server runs `AutoMigrate` on startup, so there's no separate migrate step.

### 2. Google OAuth client

Create an **OAuth 2.0 client (Web application)** in
[Google Cloud Console → APIs & Services → Credentials]. Authorized redirect
URI: `http://localhost:8080/oauth/callback`. Note client ID + secret.

### 3. Server config (`.env`)

Copy `.env.example` to `.env` and fill in:

- `OWNER_EMAILS` — both of your Google accounts.
- `OIDC_AUDIENCE` — the value the TUI will pass to
  `gcloud auth print-identity-token --audiences=...`. Anything stable works
  (e.g. `https://art.local`); the server only checks that inbound tokens
  carry it.
- `GOOGLE_OAUTH_*` — from step 2.
- `TOKEN_ENCRYPTION_KEY` — generate with `openssl rand -base64 32`.
- `VERTEX_*` and `VERTEX_SA_PATH` — currently optional (deterministic
  planner doesn't call Vertex). Required once the ADK planner lands.

### 4. Run the server

```sh
docker compose up --build server
# or, locally:
make run
```

### 5. Link calendars

```sh
TOKEN="$(gcloud auth print-identity-token --audiences=$OIDC_AUDIENCE)"

curl -s -X POST "http://localhost:8080/oauth/start?account=personal" \
  -H "Authorization: Bearer $TOKEN" | jq -r .url
# Open the returned URL, consent. Repeat for ?account=work.
```

### 6. Working hours

```sh
curl -s -X PUT http://localhost:8080/working-hours \
  -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '[
    {"slot_kind":"work","day_of_week":1,"start_minute":540,"end_minute":1080},
    {"slot_kind":"work","day_of_week":2,"start_minute":540,"end_minute":1080},
    {"slot_kind":"work","day_of_week":3,"start_minute":540,"end_minute":1080},
    {"slot_kind":"work","day_of_week":4,"start_minute":540,"end_minute":1080},
    {"slot_kind":"work","day_of_week":5,"start_minute":540,"end_minute":1080},
    {"slot_kind":"personal","day_of_week":1,"start_minute":1080,"end_minute":1380},
    {"slot_kind":"personal","day_of_week":2,"start_minute":1080,"end_minute":1380},
    {"slot_kind":"personal","day_of_week":3,"start_minute":1080,"end_minute":1380},
    {"slot_kind":"personal","day_of_week":4,"start_minute":1080,"end_minute":1380},
    {"slot_kind":"personal","day_of_week":5,"start_minute":1080,"end_minute":1380},
    {"slot_kind":"personal","day_of_week":6,"start_minute":480,"end_minute":1380},
    {"slot_kind":"personal","day_of_week":0,"start_minute":480,"end_minute":1380}
  ]'
```

(`start_minute`/`end_minute` are minutes past midnight in `ART_TIMEZONE`.)

### 7. Launch the TUI

```sh
make build
ART_API_URL=http://localhost:8080 ART_API_AUDIENCE="$OIDC_AUDIENCE" ./bin/art
```

## What's not in v1

- LLM-orchestrated planning (ADK + Vertex). Hooks are in place; planner can
  call into the same primitives once non-deterministic decisions are useful.
- Multi-user.
- Mobile / web UI.
- Push notifications.
- Sentry / OTel exporters (Prometheus only).
- Service account / domain delegation for the work account — v1 expects
  user OAuth on both.

## Verification

End-to-end smoke after setup:

1. Server up, both accounts linked, working hours saved.
2. `POST /sync` — `events` table populates with both accounts' history.
3. `POST /projects` with `target_hours=4, deadline=+3d, kind=work`.
4. `POST /replan` — `agent_runs` row succeeds; `sessions` table has new
   `planned` rows; calendar has new `focusTime` events with
   `art_managed=true`.
5. Manually add a meeting overlapping one of the focus blocks. `/sync` +
   `/replan` — Art doesn't touch the human meeting; if it slid the block,
   the new slot avoids the conflict.
6. TUI: `art` opens, week view shows the focus blocks, `r` triggers a
   replan, status bar reflects the run summary.
