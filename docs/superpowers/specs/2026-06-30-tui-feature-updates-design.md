# TUI feature updates: primary calendar, email status, project/habit UX

Date: 2026-06-30

Three changes to art's Bubble Tea TUI and the backend that supports it.

## 1. Calendar shows only the primary calendars

Today `/events` returns events from every synced calendar, and the calendar page renders all of them. We want the TUI to show only each account's primary calendar.

**Server** — `EventsList` (`lib/api/handlers/events.go`) gains a `?calendar=primary` query param. When set, restrict events to those whose `(account_kind, calendar_id)` matches an account's `primary_calendar_id`:

```sql
WHERE (account_kind, calendar_id) IN
  (SELECT kind, primary_calendar_id FROM accounts WHERE primary_calendar_id <> '')
```

The row-value subquery handles the empty case correctly: if no account has a primary calendar set, the subquery yields no rows and the result is empty — never "all events". Without the param the endpoint behaves exactly as before.

**TUI** — `Client.ListEvents` (`cli/tui/api_client.go`) always appends `&calendar=primary`. The calendar page renders only `events` (no `Session` rows), so this fully controls the view.

## 2. Art always writes focus blocks to the primary calendar

We are dropping support for a separate "art calendar". Art's scheduled blocks always go to the account's primary calendar.

- `lib/agent/llm.go:342-345`: remove the `ArtCalendarID` override; always use `client.Account.PrimaryCalendarID`.
- `lib/models/models.go`: remove the `ArtCalendarID` field from `Account`.
- Migration: GORM AutoMigrate does not drop columns, so add an explicit `DROP COLUMN IF EXISTS art_calendar_id` migration. The orphaned column is harmless if left, but we drop it for cleanliness.

Nothing currently sets `ArtCalendarID` (the OAuth store only writes `PrimaryCalendarID`), so there is no data to migrate and no test references to update.

Side effect: because art now writes to the primary calendar, its `◆` blocks have `calendar_id == primary_calendar_id` and therefore remain visible under the primary-only filter from section 1.

## 3. Email: toggle a message between archived and inbox

The digest page can only undo a past triage decision today (`x`). We add the ability to actively switch a message's status between archived and inbox, and feed that choice back into the classifier's learning — within the hard constraint that triage may only label or archive, never write/draft/send.

### State transitions

The toggle direction is chosen from the message's current `Archived` state (kept accurate — see below). The learning signal is derived from art's original `Category` versus the desired end state, not from a fixed reversal kind. This keeps toggling self-healing.

| Art `Category` | Target | Gmail labels | DB: Archived / Action | Learning: reversed / reversal_kind |
|---|---|---|---|---|
| keep / reply | archive | +Art/Archived, −INBOX | true / archived | disagreement → true / `manual_archived` |
| archive | archive | +Art/Archived, −INBOX | true / archived | agreement → false / `""` |
| archive | inbox | +INBOX, −Art/Archived | false / keep | disagreement → true / `unarchived` |
| keep / reply | inbox | +INBOX, −Art/Archived | false / keep | agreement → false / `""` |

`reconciled_at` is stamped `now` in every case. `Applied` is set true. Because `buildCorrections` only selects rows where `reversed AND reconciled_at >= cutoff`, an agreement row (`reversed=false`) drops out of the correction set — so archiving a "keep" email and then toggling it back to the inbox clears the correction instead of poisoning the classifier with "you archived this, Nat moved it back" when art never archived it.

### Stale-Archived fix

`reverseAndRecord` (`lib/email/reverse.go`) currently updates only `reversed`, `reversal_kind`, and `reconciled_at`, leaving `Archived=true` after it un-archives a message in Gmail. That makes `Archived` an unreliable direction indicator. Fix: on the un-archive path, also set `Archived=false`. After this, `Archived` faithfully mirrors "INBOX removed" and both reverse and the new toggle keep it accurate.

### Pieces

- `lib/email`: new `SetArchived(ctx, emailID string, archived bool) (models.EmailMessage, error)` implementing the table above. Idempotent: a no-op (returns the row) when the message is already in the requested state. Reuses the `reversalGmailer` label-only interface and `EnsureLabels`/`ModifyLabels`.
- `lib/email/reconcile.go`: add a `manual_archived` case to `buildCorrections` — "You left an email from %q (subject %q) in the inbox; Nat archived it — prefer 'archive' for similar mail." Add the `reversalManualArchived` constant.
- API: new route `POST /emails/{id}/archive` with JSON body `{"archived": bool}` → handler `EmailSetArchived` (`lib/api/handlers/triage.go`). 404 on unknown id, mirroring `EmailReverse`.
- TUI: `Email` struct gains `Archived bool`. New `a` binding on the digest = "archive ↔ inbox", computing the target from the selected item's `Archived` and calling a new `setEmailArchived` command, then reloading. The toggle is instant (no confirm): it is non-destructive and reversible by toggling back. The `x`/reverse path keeps its confirm dialog unchanged.

## 4. Projects and habits: discoverable and de-glitched

Two gaps: the add/edit/delete keys are not discoverable, and the add/remove flow has form/list glitches.

### Discoverability

Help is global and non-contextual today. The always-visible footer renders `keyMap.ShortHelp()` (nav keys 1–5, ?, q only), so `a`/`e`/`d` never appear; `?` (`FullHelp`) shows them on every page regardless of relevance.

Fix: add a `bindings() []key.Binding` method to the `Page` interface. Each page returns the keys it actually handles (projects/habits: add/edit/delete; digest: mark-bad + archive toggle; calendar: prev/next week; dashboard: its own). The root composes global nav bindings with the current page's bindings for both the footer short help and the `?` full help. Pages that handle no extra keys return nil.

Add empty-state hints to the projects and habits pages ("No projects yet — press a to add."), matching the hint the digest page already shows.

### Glitch fixes

Rather than guess, build a `teatest` harness that drives the root model with an injected `Client` (`newRootWithClient`) backed by an `httptest` server returning canned JSON. Reproduce the glitches deterministically, then fix them. Likely candidates: list selection jumping to the top after a reload-on-edit, and form sizing on open/resize — but the repro decides the actual fixes.

Delete confirmation is out of scope: accidental deletes were not reported, so `d` stays instant-delete. (Easy to add later if wanted.)

## Testing

- Section 1: handler test for `?calendar=primary` — filters to primary, returns empty when no account has a primary calendar, unchanged without the param.
- Section 2 (agent/model): existing agent tests still pass with the override removed; confirm focus blocks target the primary calendar.
- Section 3: table-driven test of `SetArchived` covering all four rows plus idempotent no-op and toggle-back-clears-correction; `buildCorrections` test for the `manual_archived` phrasing; handler test for `POST /emails/{id}/archive` (success + 404).
- Section 4: teatest coverage for contextual help, empty states, and each reproduced glitch.

Coverage stays above the 50% gate; golangci-lint clean (no `max`/`min` parameter names); lowercase PR title.

## Delivery

One branch, roughly six commits:

1. feat: filter calendar events to primary calendars (server + TUI)
2. refactor: always write art focus blocks to the primary calendar (drop ArtCalendarID)
3. feat: email archive/inbox toggle with learning (lib/email + reverse Archived fix)
4. feat: expose email archive toggle endpoint and wire into digest TUI
5. feat: contextual per-page help and empty-state hints
6. fix: project/habit add-remove glitches (teatest)
