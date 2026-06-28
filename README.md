# art

Personal scheduling agent. Blocks focus time on work + personal Google
Calendars for **projects** (target hours toward a deadline) and **habits**
(recurring practice). Writes new events only — never modifies human events.

Also triages both Gmail inboxes: classifies new mail with Gemini, archives
bulk mail, drafts replies, and labels what needs reading or thought — every
action reversible and recorded.

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

## Email triage

On each hourly tick (after the planner) Art triages new `INBOX` mail per
account: it classifies each message with Gemini structured output
(`archive` / `reply` / `read` / `thinking` / `keep`), then acts in Gmail and
records an `email_messages` row plus an `agent_runs` row (`kind=triage`).

Every action is **reversible and attributable** via `Art/*` labels: archive
removes `INBOX` and adds `Art/Archived` (mail stays in All Mail); replies are
saved as drafts (Art never sends); `read`/`thinking` are labeled in place.
Low-confidence archives are downgraded to `read` so Art never auto-archives
mail it is unsure about.

**Learning.** Before each run, a reconcile pass detects what you reversed —
mail you un-archived, drafts you discarded — and feeds those corrections into
the next run's classifier prompt. No model training.

Gmail uses the `gmail.modify` **restricted scope**, so:

- Re-link both accounts after enabling (the scope was added to the shared
  consent; existing calendar links break until re-linked).
- The consumer account (natwelch.com) needs you added as a Test User on the
  OAuth consent screen; the Workspace account (laurel.ai) may need the OAuth
  client allowlisted by an admin.
- Run with `TRIAGE_DRY_RUN=true` first: Art classifies and writes audit rows
  (`Applied=false`) without touching Gmail. Inspect `GET /emails`, then flip
  it off. See `.env.example` for all `TRIAGE_*` knobs.

## Setup

```sh
docker compose up -d db          # Postgres
cp .env.example .env             # fill in
make run                         # server
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
make build
ART_API_URL=http://localhost:8080 ART_API_AUDIENCE="$OIDC_AUDIENCE" ./bin/art
```

Install via Homebrew once released:

```sh
brew install icco/tap/art
```

## Out of scope for v1

Multi-user, mobile/web UI, push notifications, Sentry/OTel exporters,
service-account domain delegation. (The email triager classifies with
Gemini; the calendar planner is still deterministic Go.)

## End-to-end smoke

1. Both accounts linked, working hours saved.
2. `POST /sync` populates `events`.
3. `POST /projects` (`target_hours`, `deadline`, `kind`).
4. `POST /replan` — `agent_runs` succeeds; `sessions` has new `planned`
   rows; calendar has new `focusTime` events with `art_managed=true`.
5. Add a conflicting meeting manually, replan — Art slides to the next
   free slot and leaves the meeting untouched.
6. TUI shows the blocks; `r` triggers replan.
7. `TRIAGE_DRY_RUN=true`, `POST /triage/run` — `GET /emails` lists classified
   mail with `applied=false` and no Gmail changes. Flip dry-run off and rerun:
   `Art/*` labels appear, bulk mail is archived, replies become drafts. The
   TUI `digest` tab (key `4`) shows it; `t` triggers triage.
8. Un-archive one archived message, rerun — its row is marked `reversed` and
   the correction feeds the next run's prompt.
