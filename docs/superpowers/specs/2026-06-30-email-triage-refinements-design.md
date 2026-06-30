# Email Triage Refinements — Design

Date: 2026-06-30
Branch: `email-triage-refinements`

## Background

Email triage (`lib/email`, `lib/gmail`) classifies inbox mail with Gemini and
applies reversible Gmail actions. An investigation into "emails get labeled
archived but stay in the inbox" found the triage logic is correct: Gmail's inbox
is thread-keyed, so a conversation stays visible while a sibling message keeps
`INBOX`. No bug. This spec covers four follow-on improvements.

## A. Seven-day window, both accounts

Both accounts already run each pass. Only the window/cap defaults change, in
`lib/config/load.go`:

- `BackfillDays`: `14 → 7`
- `ReconcileDays`: `14 → 7`
- `MaxPerRun`: `50 → 1000`

`MaxPerRun` is reused as the reconcile row cap (`runner.go` passes it as
`maxRows`); raising it to 1000 also widens reconcile coverage per run. This is
intended — Gmail read volume stays well within quota. Update `load_test.go`
default assertions.

## B. Log all failures

- `lib/email/reconcile.go` (~line 48): the `detectReversal` error path currently
  `continue`s silently. Add
  `log.Warnw("reconcile: detect reversal failed", "account", kind, "id", row.GmailMessageID, "err", err)`
  before continuing (logger via `gutillog.FromContext`).
- Count reconcile errors and surface them in the run summary as
  `reconcile_errors` so failures show in `/agent-runs`, not only logs.

All other failure paths (get/classify/apply/persist/setup) already log; no change.

## C. Simplify taxonomy: drop `read` and `thinking`

Final category set: `archive` / `reply` / `keep`. Labels applied:
`Art/Triaged` (all), `Art/Archived` (archive), `Art/Reply` (reply).

Changes:

- `lib/email/prompt.md`: reduce to archive/reply/keep. `keep` absorbs the old
  read+thinking meaning ("worth your eyes, needs thought, personal, or
  uncertain — left untouched"). Fix the safety lines that say "prefer read or
  keep" → "prefer keep" and "classify as thinking or keep" → "classify as keep".
- `lib/email/classifier.go`: `classificationSchema()` enum → `archive, reply,
  keep`.
- `lib/email/triager.go` `decideAction`: remove the `read` and `thinking` cases.
  Low-confidence archive downgrades to `ActionKeep` (no label) instead of
  `ActionRead`. Add a `default: d.Action = models.ActionKeep` guard so any
  unmapped category is treated as keep.
- `lib/gmail/labels.go`: remove `LabelRead` and `LabelThinking` consts and drop
  them from `ArtLabels` → `[LabelTriaged, LabelArchived, LabelReply]`.

Kept deliberately (no DB migration, historical rows stay valid):

- `models.EmailRead`, `EmailThinking`, `ActionRead`, `ActionThinking` constants,
  marked deprecated.
- `EmailCategory.Valid()` keeps `read`/`thinking` (so category filters on
  historical rows still validate).
- The Postgres `CHECK` constraints on `category`/`action` are unchanged.

Existing `Art/Read` / `Art/Thinking` Gmail labels and already-tagged messages are
left untouched (harmless orphans). `EnsureLabels` simply stops managing them.

## D. Mark-bad / undo a decision (TUI)

A manual reversal that undoes any triaged decision and feeds the classifier the
correction, reusing the existing reversal machinery (`reversed`,
`reversal_kind`, `reconciled_at`, `buildCorrections`).

### Reversal semantics

Reverse the Gmail side-effect by action, keep `Art/Triaged` (so it is not
re-triaged into the same mistake), set `reversed=true` and `reconciled_at=now`:

| Row action | Gmail undo | `reversal_kind` |
|---|---|---|
| `archived` | re-add `INBOX`, remove `Art/Archived` | `unarchived` |
| `reply` | delete the draft (if `DraftID` set), remove `Art/Reply` | `draft_deleted` |
| `read` / `thinking` / `keep` / other | none — record correction only | `miscategorized` (new) |

`unarchived` and `draft_deleted` reuse the existing `buildCorrections`
phrasings. Add one `miscategorized` case: "You categorized mail from %q (subject
%q) as %s; Nat marked that wrong — reconsider similar mail." (uses the row's
`Category`). Idempotent: a row already `reversed` is a no-op.

Only `archived` and `reply` touch Gmail, and both their labels (`Art/Archived`,
`Art/Reply`) remain in `ArtLabels`, so their IDs come from `EnsureLabels`.
Historical `read`/`thinking` rows keep their now-orphaned label (harmless); the
reversal just records the correction.

### Components

- `lib/gmail/actions.go`: `DeleteDraft(ctx, draftID) error` wrapping
  `Users.Drafts.Delete`; treat 404 as success (reuse `isNotFound`).
- `lib/email`: `Runner.Reverse(ctx, emailID string) (models.EmailMessage, error)`
  — load row by ID (`ErrRecordNotFound` if missing), build the account's Gmail
  client, perform the undo, update the row, return it. The reversal core takes a
  small gmailer interface (`EnsureLabels`, `ModifyLabels`, `DeleteDraft`) so it
  unit-tests with a fake. New tests cover each action + idempotency + not-found.
- `lib/api`: `POST /emails/{id}/reverse` → handler `EmailReverse`. Extend
  `TriageService` with `Reverse(ctx, id) (models.EmailMessage, error)`. Map
  `ErrRecordNotFound`→404, other→500, success→200 with the updated row. Runs
  synchronously (single quick Gmail call). Add a route in `router.go`.
- `cli/tui`:
  - `api_client.go`: `ReverseEmail(ctx, id) (Email, error)` → `POST
    /emails/{id}/reverse`.
  - `keys.go`: new `Reject` binding on `x` ("mark bad"); add to `FullHelp`.
  - `digest.go`: handle `x` on the selected item — open a `huh` confirm
    ("Mark this decision bad and undo it?"), on confirm call `ReverseEmail`
    then reload the list. Mirror the `projects.go` form-state pattern. Reversed
    rows already render with the `↶` prefix.

## Out of scope

- No re-triage loop after reversal (matches existing reconcile behavior).
- No bulk reverse, no new DB columns, no constraint migration.

## Commit plan (one branch)

1. config defaults (window + cap)
2. reconcile/runner failure logging
3. taxonomy simplification (prompt, schema, decideAction, labels)
4. gmail `DeleteDraft` + `email.Runner.Reverse` + tests
5. `POST /emails/{id}/reverse` API + TUI `x`-to-reverse

## Tests touched

`load_test.go`, `triager_test.go` (decideAction cases + apply), classifier
expectations, new reversal tests, handler tests, `api_client` tests.
