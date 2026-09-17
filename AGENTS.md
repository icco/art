# AGENTS.md

Guidance for coding agents working in this repo. Operator docs live in [README.md](README.md).

## Layout

- `./` — Server entrypoint (`main.go`, binary `art-server`): chi/v5 router, GORM/Postgres, Google OIDC, Prometheus, graceful shutdown, background job queue.
- `./cmd/art` — Bubble Tea TUI CLI entrypoint.
- `cli/tui/` — TUI application logic and components. Talks to server over HTTP; auth via `gcloud auth print-identity-token`.
- `lib/` — Domain packages: `api`, `models`, `oauth`, `calendar`, `gmail`, `email`, `agent`, `reconcile`, `queue`, `config`, `db`, `settings`, `cost`.

## Architecture & Invariants

- **Flow**: `art TUI --(ID token)--> server --> Google Calendar/Gmail`; server ↔ Postgres.
- **Job Queue (`lib/queue`)**: Self-chaining Postgres queue: `sync` (every 10m; mirrors calendars and reconciles), `planner` (hourly), `triage` (every 30m). Slot execution order: `sync` → `planner` → `triage`.
- **Planner (`lib/agent/plan.go`)**: Deterministic Go scheduling over `plan_horizon_days`. **Deliberately calls no LLM**; do not reintroduce model calls to planning. `commitFocus` is the server-side source of truth for scheduling invariants.
- **Busy Predicate**: Must remain strictly identical across `loadBusy`/`overlapsHard` (`lib/agent/freeslots.go`), `commitFocus`, and `reconcile.hasHumanConflict`.
- **Database**: GORM with `AutoMigrate` (`lib/models`); UUID PKs via Go `BeforeCreate` + `google/uuid`. No migration files.
- **Calendar Events**: Created events use `eventType=focusTime` with `art_managed=true` extended property.
- **Settings (`lib/settings`)**: Runtime config in `settings` table seeded from env. Read at use time (never cache at boot). Secrets are strictly forbidden in this table or API.
- **LLM Usage & Cost Guard (`lib/cost`)**: Triage is the only LLM caller (Gemini Flash structured output). Record both candidate and thoughts token counts. Do not persist email message bodies. Refresh tokens are AES-256-GCM encrypted.

## Conventions

- Follow icco Go conventions: chi router, `github.com/icco/gutil` (logging, JSON, ETags), zap, GORM.
- PR titles and commits must follow Conventional Commits with lowercase subjects.
- `golangci-lint run` must pass. **Forbids `max`/`min` as parameter names** (shadowing builtins).
- Coverage gate: total ≥ 50% (`.github/workflows/test.yml`). Set `TEST_DATABASE_URL` locally for DB-backed tests.

## Commands

```sh
task build         # builds ./bin/art
task run           # run server
task test          # run tests (-p 1 required, shares one DB schema; TEST_DATABASE_URL required for DB tests)
task lint          # run vet and gofmt
golangci-lint run  # run full linters
task tidy          # tidy go modules
```

## Security & Auth

- Gate (`lib/api/auth.go`): `idtoken.Validate` (`OIDC_AUDIENCE`), pure `authorize()` requiring `email_verified == true` and `OWNER_EMAILS` match.
- Rate limiting: Keyed on **rightmost** `X-Forwarded-For` hop.
- Never log secrets or token contents.
