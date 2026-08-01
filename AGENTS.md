# AGENTS.md

Guidance for coding agents working in this repo. Operator/usage docs live in
[README.md](README.md).

## Layout

- `./` — server entrypoint (`main.go`, binary `art-server`): chi/v5 router,
  GORM/Postgres, Google OIDC, Prometheus, graceful shutdown, background job queue.
- `./cmd/art` — Bubble Tea TUI (the `art` CLI). Talks to the server over HTTP;
  auth via `gcloud auth print-identity-token` (no stored secrets).
- `lib/api` — middleware, router, handlers. `lib/models` — GORM models.
  `lib/oauth`, `lib/calendar`, `lib/gmail`, `lib/email`, `lib/agent`,
  `lib/reconcile`, `lib/queue`, `lib/config`, `lib/db`.

## Architecture

`art TUI --(ID token)--> server --> Google Calendar/Gmail`; server ↔ Postgres.
A Postgres-backed job queue (`lib/queue`) runs three kinds on a self-chaining
grid: **sync** every 10 min (mirrors both calendars, then reconciles the plan
against that fresh mirror as its tail — `lib/reconcile`), **planner** hourly,
**triage** every 30 min. Within a shared slot they run
sync → planner → triage. The planner is plain Go (`lib/agent/plan.go`): it
books focus blocks over a rolling window (`plan_horizon_days`) by walking
projects deadline-ascending, then habits one-per-day. `commitFocus` is the
server-side source of truth for every scheduling invariant.

- **The planner deliberately calls no model.** As an ADK/Vertex Gemini agent it
  cost $737 in July 2026: ~96 re-plans a day of a prompt that spelled out a
  `for` loop, recording 0 tokens so it showed up only in the billing console.
  Don't reintroduce an LLM; the rules are mechanical.

- Schema is owned by `gorm.AutoMigrate` over `lib/models`; UUID PKs are
  generated in Go (`BeforeCreate` + `google/uuid`). No migration files.
- Art-created calendar events use `eventType=focusTime` with `art_managed=true`
  in extended properties — that flag is how Art knows what it may modify.
- The busy predicate lives in three places that must agree: `loadBusy` /
  `overlapsHard` (`lib/agent/freeslots.go`), `commitFocus`, and
  `reconcile.hasHumanConflict`. The soft titles are schedulable-over in all
  three — miss one and the next sync retracts what the planner just booked.
- `lib/settings` holds the runtime-editable config (soft titles, triage knobs,
  planner bounds) in the `settings` table, seeded by the matching env vars. Read
  it at use time — once per run — never at boot, or an edit needs a redeploy.
  Its key list is an allowlist: secrets never go in the table or the API.
- `lib/cost` prices recorded tokens and enforces `daily_budget_usd` (default
  $2); triage is the only LLM caller. Two traps: `ThoughtsTokenCount` bills as
  output but is **not** in `CandidatesTokenCount` (measured ~9.5x undercount),
  and only Flash honours `ThinkingBudget: 0`. New model calls must record both
  counts and go through the guard.
- Email triage classifies with Gemini Flash structured output and records an
  `email_messages` row + an `agent_runs` row (`kind=triage`). Message bodies are
  never persisted. Refresh tokens are AES-256-GCM sealed (`lib/oauth`).

## Conventions

- Follow icco Go patterns: chi, `github.com/icco/gutil` (logging, JSON, ETags),
  zap, GORM. Don't reimplement what gutil provides.
- `golangci-lint run` must pass. The linter **forbids `max`/`min` as parameter
  names** (they shadow builtins).
- Conventional Commits; **lowercase** PR titles.
- Coverage gate: **total ≥ 50%** (`.github/workflows/test.yml`). CI runs tests
  against a Postgres service — set `TEST_DATABASE_URL` locally to exercise
  DB-backed packages, or coverage will read low.

## Build / test

```sh
task build                       # ./bin/art
task run                         # server
go test ./...                    # set TEST_DATABASE_URL for DB-backed packages
golangci-lint run
```

## Security model (respect when changing auth)

Server-side gate (`lib/api/auth.go`): `idtoken.Validate` (audience =
`OIDC_AUDIENCE`) then the pure, unit-tested `authorize()` requires
`email_verified == true` and an `OWNER_EMAILS` match. Per-IP rate limiting
(`lib/api/router.go`) keys on the **rightmost** `X-Forwarded-For` hop (the one
the trusted proxy appends) — never the spoofable leftmost. CORS is intentionally
absent (no browser clients). Never log secrets or token contents.

## Release

`.goreleaser.yaml` builds `./cmd/art` and publishes a Homebrew cask to
`icco/homebrew-tap`. `.github/workflows/release.yml` auto-tags via
semantic-version and runs `goreleaser release`. The cross-repo cask push needs a
`GH_PAT` repo secret (the workflow falls back to `GITHUB_TOKEN`, which cannot
push to another repo).
