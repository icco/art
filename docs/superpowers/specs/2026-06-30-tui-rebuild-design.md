# art TUI rebuild — design

Date: 2026-06-30
Status: approved

## Why

The current `cli/tui` is a single god-model (`app.go`) that hand-renders every
screen and has two real bugs:

1. **No refresh after mutations.** Add/delete/replan/sync/triage set a status
   string but never reload the affected data, so the UI silently goes stale —
   the change appears to do nothing.
2. **State mutation inside command goroutines.** `submitForm` writes
   `a.screen` from within a `tea.Cmd`, racing the update loop.

The fix is a clean rebuild around the Bubble Tea "model tree" pattern (per
leg100's *Building Bubble Tea Programs*), using `charmbracelet/bubbles`
components and `charmbracelet/huh` for forms.

## Scope

Rethink the UX around a **glanceable dashboard** home screen, plus the existing
surfaces rebuilt cleanly: calendar, projects, habits, email digest. One new
read-only server endpoint (`GET /agent-runs`) backs the dashboard's run-status
tile.

Out of scope for v1: reversing email actions (no server endpoint).
Working-hours editing is **Phase 2** (a real planner-input gap, but not core).

## Architecture

Single `tui` package, one file per concern (model-tree is about model
composition, not package boundaries — one package avoids root↔page import
cycles):

```
cli/tui/
  tui.go        Run(cfg) — builds root model, runs the tea.Program
  root.go       rootModel: shared deps + chrome + message router
  page.go       Page interface, pageID enum, navigation messages
  keys.go       keyMap (key.Binding) + ShortHelp/FullHelp
  styles.go     shared lipgloss theme
  config.go     Config + LoadConfig (kept)
  client.go     API client + typed resources (renamed from api_client.go)
  commands.go   tea.Cmd constructors — all HTTP I/O, returning typed messages
  messages.go   shared msg types
  dashboard.go  calendar.go  projects.go  habits.go  digest.go
  forms.go      huh-based add/edit forms
```

### Root model

Owns shared state (`cfg`, `*Client`, `width/height`), global chrome (title bar,
transient status/error line, `help.Model`, `spinner.Model`), the `current` page
and a small **nav stack**. Routes every message three ways:

- **global** — quit (`q`/`ctrl+c`), `?` help toggle, top-level nav keys,
  `r`/`s`/`t` actions;
- **to current page** — everything else;
- **broadcast** `tea.WindowSizeMsg` to all pages, which recompute layout with
  `lipgloss.Height/Width` (no magic numbers).

### Pages

Implement `Page` (`tea.Model` + `Title()`), use **value receivers**, built from
bubbles (`list`, `table`, `viewport`, `textinput`, `key`, `help`, `spinner`).
Navigation is a `navigateMsg{to pageID}` returned as a `tea.Cmd`; root
pushes/pops the stack and calls the target's `Init()`.

Top-level nav: `1` dashboard · `2` calendar · `3` projects · `4` habits ·
`5` digest. `esc` pops back to dashboard.

### Hard rules (fix the bugs by construction)

- **No state mutation inside a command goroutine.** Commands close over copied
  values and return messages; all mutation happens in `Update`.
- **Mutations always reload.** Every create/update/delete/replan/sync/triage
  command returns `tea.Batch(statusMsg, <reload of affected data>)`.

## Screens

### Dashboard (home)

Four independently-loaded tiles (each fills in with a spinner until its data
arrives), composed with `lipgloss.JoinHorizontal/Vertical`:

- **Today** ← `GET /events` (today's window); art-managed (`◆`) highlighted.
- **Projects** ← `GET /projects`; progress bar from `ScheduledHours/TargetHours`,
  deadline, `done ✓`.
- **Habits** ← `GET /sessions` (this week); cadence dots: filled = `happened`,
  hollow = `planned`, against the habit's per-week count.
- **Last runs** ← `GET /agent-runs`; latest planner + triage run with status and
  relative time.

### Calendar

Week view; `←/→` week nav; art-managed blocks highlighted; scroll via
`viewport`.

### Projects / Habits

Bubbles `list`; `a` add, `e` edit, `d` delete (with confirm). Add/edit open a
**huh** form; on submit the command POSTs/PATCHes and the page reloads its list.
Project rows show inline progress.

### Digest

Bubbles `list` of triaged emails + a `viewport` for the selected email's
summary/draft; `t` runs triage then reloads. Display only (no reverse endpoint).

## Server change

`GET /agent-runs` with optional `?kind=planner|triage` and `?limit=N`, returning
recent `AgentRun` rows `ORDER BY started_at DESC` — the same query `ReplanRun`
already runs internally. Adds: chi route, `AgentRunsList` handler, handler test.
Client gets `ListRuns(ctx, kind, limit)`.

## Error handling

- `errMsg{err}` → root stores it, renders a red status line; never crashes.
- `statusMsg(string)` → transient status (e.g. "project created",
  "replan: succeeded").
- Spinner shows while a load is in flight; pages show "loading…" until first
  data.

## Testing

- Each page model is unit-testable: construct, send msgs, assert `View()`
  contains expected strings.
- Pure helpers (progress bar, cadence dots, week math, relative time) get table
  tests.
- Client tested against `httptest` (extends existing `api_client_test.go`).
- `teatest` for one or two end-to-end flows (golden files optional).
- Keep total coverage ≥ 50% (CI gate); `AgentRunsList` handler gets a test.

## Entry point

Keep the `--help`/`--version` handling in `cmd/art/main.go` (update the help
keybindings to match the new nav). `tui.Run(cfg)` wraps program creation.

## Conventions

icco Go patterns (chi, gutil, zap, GORM); `golangci-lint` must pass (no
`max`/`min` param names); Conventional Commits; one branch (`rebuild-tui`),
multiple commits.
