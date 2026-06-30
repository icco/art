# AGENTS.md

Guidance for coding agents working in this repo. Operator/usage docs live in
[README.md](README.md).

## Layout

- `./` — server entrypoint (`main.go`, binary `art-server`): chi/v5 router,
  GORM/Postgres, Google OIDC, Prometheus, graceful shutdown, hourly cron.
- `./cmd/art` — Bubble Tea TUI (the `art` CLI). Talks to the server over HTTP;
  auth via `gcloud auth print-identity-token` (no stored secrets).
- `lib/api` — middleware, router, handlers. `lib/models` — GORM models.
  `lib/oauth`, `lib/calendar`, `lib/gmail`, `lib/email`, `lib/agent`,
  `lib/cron`, `lib/config`, `lib/db`.

## Architecture

`art TUI --(ID token)--> server --> Google Calendar/Gmail`; server ↔ Postgres;
an hourly cron runs the planner, then the email triager. The planner is
deterministic Go today — ADK/Vertex can wrap the same primitives later.

- Schema is owned by `gorm.AutoMigrate` over `lib/models`; UUID PKs are
  generated in Go (`BeforeCreate` + `google/uuid`). No migration files.
- Art-created calendar events use `eventType=focusTime` with `art_managed=true`
  in extended properties — that flag is how Art knows what it may modify.
- Email triage classifies with Gemini structured output and records an
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
