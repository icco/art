# Email Triage Refinements Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Widen the triage window, log all reconcile failures, simplify the classifier taxonomy to archive/reply/keep, and add a TUI action to mark a decision bad and undo it.

**Architecture:** Go server (`lib/...`) + charm.land v2 TUI (`cli/tui`). Triage lives in `lib/email` (Gemini classifier, triager, reconcile, runner) over `lib/gmail`. Persistence is GORM with AutoMigrate only; one explicit idempotent migration is added in `lib/db/conn.go`. The reverse feature reuses the existing reversal columns (`reversed`, `reversal_kind`, `reconciled_at`) and the `buildCorrections` learning path.

**Tech Stack:** Go, GORM/Postgres, chi v5 router, `github.com/icco/gutil` logging/render, `google.golang.org/genai` (Vertex), charm.land/bubbletea v2 + huh v2.

## Global Constraints

- Commit titles: lowercase Conventional Commits. End every commit message with `Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>`.
- Branch: `email-triage-refinements` (already checked out). One branch, sequential commits. Never `git commit --amend` or force-push.
- Lint: `golangci-lint run` must pass; do not name parameters `max` or `min`.
- Coverage gate: 50%. Keep new code tested.
- DB-backed tests use `testdb.Open`, which **skips** unless `TEST_DATABASE_URL` is set. Run DB tests with that env var pointing at a scratch Postgres.
- No new DB columns. Only the `category` CHECK constraint changes, via the explicit migration in Task 4.
- Markdown prose: one line per paragraph/bullet, no hard wraps.

---

### Task 1: Config defaults — 7-day window, 1000 cap

**Files:**
- Modify: `lib/config/load.go:76-78`
- Test: `lib/config/load_test.go` (extend `TestLoadValidate`)

- [ ] **Step 1: Write the failing test**

In `lib/config/load_test.go`, inside `TestLoadValidate`, add these assertions just before the closing `}` of the function (after the `cfg.Vertex.Location` check at line ~88):

```go
	if cfg.Triage.BackfillDays != 7 || cfg.Triage.ReconcileDays != 7 {
		t.Errorf("triage window defaults = %d/%d, want 7/7", cfg.Triage.BackfillDays, cfg.Triage.ReconcileDays)
	}
	if cfg.Triage.MaxPerRun != 1000 {
		t.Errorf("MaxPerRun default = %d, want 1000", cfg.Triage.MaxPerRun)
	}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `TEST_DATABASE_URL= go test ./lib/config/ -run TestLoadValidate -v`
Expected: FAIL — got `14/14` and `50`.

- [ ] **Step 3: Change the defaults**

In `lib/config/load.go`, change lines 76-78 to:

```go
			BackfillDays:        envInt("TRIAGE_BACKFILL_DAYS", 7),
			MaxPerRun:           envInt("TRIAGE_MAX_PER_RUN", 1000),
			ConfidenceThreshold: envFloat("TRIAGE_CONFIDENCE_THRESHOLD", 0.8),
			ReconcileDays:       envInt("TRIAGE_RECONCILE_DAYS", 7),
```

(Only the three numeric literals change: `14→7`, `50→1000`, `14→7`. Leave `ConfidenceThreshold` as-is.)

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./lib/config/ -run TestLoadValidate -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add lib/config/load.go lib/config/load_test.go
git commit -m "feat: widen triage window to 7d and raise per-run cap to 1000

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 2: Log and count reconcile failures

**Files:**
- Modify: `lib/email/reconcile.go` (`Reconcile` signature + log on `detectReversal` error)
- Modify: `lib/email/runner.go` (capture the count into the run summary)
- Test: `lib/email/reconcile_test.go`

**Interfaces:**
- Produces: `Reconcile(ctx context.Context, db *gorm.DB, kind models.AccountKind, gm reconcileGmailer, withinDays, maxRows int) (int, error)` — the `int` is the number of per-row `detectReversal` failures (non-fatal). A fatal DB error returns `(0, err)`.

- [ ] **Step 1: Update the existing caller and add the error-count test**

In `lib/email/reconcile_test.go`:

Add `"errors"` to the imports.

Replace the `fakeReconcileGmail` type and its two methods (lines 13-24) with:

```go
type fakeReconcileGmail struct {
	inInbox     map[string]bool // message ID -> currently back in the inbox
	draftExists map[string]bool // draft ID -> still present
	err         error           // if set, both lookups return it
}

func (f *fakeReconcileGmail) HasInboxLabel(_ context.Context, id string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.inInbox[id], nil
}

func (f *fakeReconcileGmail) GetDraft(_ context.Context, id string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.draftExists[id], nil
}
```

Change the `Reconcile` call at line 59 from:

```go
	if err := Reconcile(ctx, db, models.AccountPersonal, gm, 14, 50); err != nil {
		t.Fatal(err)
	}
```

to:

```go
	if _, err := Reconcile(ctx, db, models.AccountPersonal, gm, 14, 50); err != nil {
		t.Fatal(err)
	}
```

Add this new test at the end of the file:

```go
func TestReconcileCountsErrors(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()

	seedRow(t, db, "g1", models.ActionArchived, true, "", models.EmailArchive)
	seedRow(t, db, "g2", models.ActionReply, true, "d2", models.EmailReply)

	gm := &fakeReconcileGmail{err: errors.New("gmail unavailable")}
	n, err := Reconcile(ctx, db, models.AccountPersonal, gm, 14, 50)
	if err != nil {
		t.Fatalf("transient per-row errors must not be fatal: %v", err)
	}
	if n != 2 {
		t.Errorf("reconcile error count = %d, want 2", n)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `TEST_DATABASE_URL=$TEST_DATABASE_URL go test ./lib/email/ -run 'TestReconcile' -v`
Expected: COMPILE FAIL — `Reconcile` returns one value, not two.

- [ ] **Step 3: Change `Reconcile` to log and count**

In `lib/email/reconcile.go`, add the logging import. The import block becomes:

```go
import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/icco/art/lib/models"
	gutillog "github.com/icco/gutil/logging"
	"gorm.io/gorm"
)
```

Change the signature and the loop. Replace the function body from `func Reconcile(... ) error {` through its final `return nil`:

```go
func Reconcile(ctx context.Context, db *gorm.DB, kind models.AccountKind, gm reconcileGmailer, withinDays, maxRows int) (int, error) {
	cutoff := time.Now().AddDate(0, 0, -withinDays)
	var rows []models.EmailMessage
	if err := db.WithContext(ctx).
		Where("account_kind = ? AND applied AND NOT reversed AND action IN ? AND created_at >= ?",
			kind, []string{string(models.ActionArchived), string(models.ActionReply)}, cutoff).
		Order("reconciled_at ASC NULLS FIRST").
		Limit(maxRows).
		Find(&rows).Error; err != nil {
		return 0, err
	}

	log := gutillog.FromContext(ctx)
	now := time.Now()
	errCount := 0
	for i := range rows {
		row := &rows[i]
		reversalKind, err := detectReversal(ctx, gm, row)
		if err != nil {
			// Transient Gmail error: log it, count it, and leave the row for a
			// later run rather than wedging the pass.
			log.Warnw("reconcile: detect reversal failed", "account", kind, "id", row.GmailMessageID, "err", err)
			errCount++
			continue
		}
		updates := map[string]any{"reconciled_at": &now}
		if reversalKind != "" {
			updates["reversed"] = true
			updates["reversal_kind"] = reversalKind
		}
		if err := db.WithContext(ctx).Model(&models.EmailMessage{}).
			Where("id = ?", row.ID).Updates(updates).Error; err != nil {
			return errCount, err
		}
	}
	return errCount, nil
}
```

- [ ] **Step 4: Capture the count in the runner**

In `lib/email/runner.go`, replace the reconcile loop (the `// Phase 1` block, currently lines ~72-80):

```go
	// Phase 1: reconcile prior actions so the corrections block is current.
	for _, kind := range order {
		gm, ok := clients[kind]
		if !ok {
			continue
		}
		n, err := Reconcile(ctx, r.DB, kind, gm, r.Cfg.Triage.ReconcileDays, r.Cfg.Triage.MaxPerRun)
		if err != nil {
			log.Warnw("reconcile failed", "account", kind, "err", err)
		}
		counts["reconcile_errors"] += n
	}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./lib/email/ -run 'TestReconcile|TestBuildCorrections' -v`
Expected: PASS (including `TestReconcileCountsErrors` with count 2).

- [ ] **Step 6: Commit**

```bash
git add lib/email/reconcile.go lib/email/runner.go lib/email/reconcile_test.go
git commit -m "fix: log and count reconcile reversal-check failures

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 3: Simplify taxonomy to archive/reply/keep

**Files:**
- Modify: `lib/email/prompt.md`
- Modify: `lib/email/classifier.go` (`classificationSchema` enum)
- Modify: `lib/email/triager.go` (`decideAction`)
- Modify: `lib/gmail/labels.go` (drop `LabelRead`/`LabelThinking`)
- Modify: `lib/models/models.go` (drop constants, narrow `Valid()` and the `category` CHECK tag)
- Modify: `lib/api/handlers/triage.go` (category error message)
- Test: `lib/email/triager_test.go`, `lib/email/classifier_test.go`

**Interfaces:**
- Produces: classifier emits only `archive`/`reply`/`keep`. `decideAction` returns `ActionKeep` for everything except high-confidence archive and reply. `gmail.ArtLabels == [LabelTriaged, LabelArchived, LabelReply]`.

- [ ] **Step 1: Update the tests first**

In `lib/email/triager_test.go`, replace the `cases` slice in `TestDecideAction` (lines 16-31) with:

```go
	cases := []struct {
		name        string
		cat         models.EmailCategory
		conf        float64
		wantAction  models.EmailAction
		wantArchive bool
		wantDraft   bool
		wantLabel   string
	}{
		{"archive high confidence", models.EmailArchive, 0.95, models.ActionArchived, true, false, gmail.LabelArchived},
		{"archive low confidence downgrades to keep", models.EmailArchive, 0.5, models.ActionKeep, false, false, ""},
		{"reply drafts", models.EmailReply, 0.9, models.ActionReply, false, true, gmail.LabelReply},
		{"keep is inert", models.EmailKeep, 0.9, models.ActionKeep, false, false, ""},
	}
```

Replace the fake `EnsureLabels` map (lines 70-76) with:

```go
func (f *fakeGmail) EnsureLabels(context.Context) (map[string]string, error) {
	return map[string]string{
		gmail.LabelTriaged:  "L_TRIAGED",
		gmail.LabelArchived: "L_ARCHIVED",
		gmail.LabelReply:    "L_REPLY",
	}, nil
}
```

In `TestRunAccountIdempotent` (line ~205), change the `byID` map so neither message uses the removed `read` category:

```go
	byID := map[string]Classification{
		"m1": {Category: models.EmailKeep, Confidence: 0.9},
		"m2": {Category: models.EmailKeep, Confidence: 0.9},
	}
```

In `lib/email/classifier_test.go`, replace the enum `want` list in `TestClassificationSchema` (lines 36-39) with:

```go
	for _, want := range []string{
		string(models.EmailArchive), string(models.EmailReply), string(models.EmailKeep),
	} {
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./lib/email/ -run 'TestDecideAction|TestClassificationSchema' -v`
Expected: FAIL/compile error (old behavior still maps low-confidence archive to `ActionRead`; enum still has read/thinking).

- [ ] **Step 3: Narrow `decideAction`**

In `lib/email/triager.go`, replace the doc comment and function (lines 52-81) with:

```go
// decideAction maps a classification to concrete labels/actions. A low-
// confidence archive is downgraded to keep (left untouched) so art never
// auto-archives mail it is unsure about. Art/Triaged is always applied.
func decideAction(cat models.EmailCategory, confidence, threshold float64) decision {
	d := decision{AddLabels: []string{gmail.LabelTriaged}}
	switch cat {
	case models.EmailArchive:
		if confidence >= threshold {
			d.Action = models.ActionArchived
			d.RemoveInbox = true
			d.AddLabels = append(d.AddLabels, gmail.LabelArchived)
		} else {
			d.Action = models.ActionKeep
		}
	case models.EmailReply:
		d.Action = models.ActionReply
		d.AddLabels = append(d.AddLabels, gmail.LabelReply)
		d.MakeDraft = true
	default:
		d.Action = models.ActionKeep
	}
	return d
}
```

- [ ] **Step 4: Narrow the classifier schema**

In `lib/email/classifier.go`, replace the `Enum` slice in `classificationSchema` (lines 116-122) with:

```go
				Enum: []string{
					string(models.EmailArchive),
					string(models.EmailReply),
					string(models.EmailKeep),
				},
```

- [ ] **Step 5: Drop the labels**

In `lib/gmail/labels.go`, replace the const block and `ArtLabels` (lines 11-23) with:

```go
const (
	LabelTriaged  = "Art/Triaged"
	LabelArchived = "Art/Archived"
	LabelReply    = "Art/Reply"

	// InboxLabel is Gmail's system INBOX label; removing it archives a message.
	InboxLabel = "INBOX"
)

// ArtLabels is the full set of labels art manages, in creation order.
var ArtLabels = []string{LabelTriaged, LabelArchived, LabelReply}
```

- [ ] **Step 6: Drop the model constants and narrow validation + constraint**

In `lib/models/models.go`, delete the `EmailRead`, `EmailThinking`, `ActionRead`, and `ActionThinking` const declarations (lines 75-78 and 86-89), keeping `EmailArchive`, `EmailReply`, `EmailKeep`, `ActionArchived`, `ActionReply`, `ActionKeep`, `ActionNone`.

Replace the `EmailCategory.Valid()` switch (lines 109-114) with:

```go
func (c EmailCategory) Valid() bool {
	switch c {
	case EmailArchive, EmailReply, EmailKeep:
		return true
	}
	return false
```

Change the `category` field tag (line 253) from:

```go
	Category   EmailCategory `gorm:"type:varchar(16);not null;check:category IN ('archive','reply','read','thinking','keep')" json:"category"`
```

to:

```go
	Category   EmailCategory `gorm:"type:varchar(16);not null;check:category IN ('archive','reply','keep')" json:"category"`
```

- [ ] **Step 7: Update the API category error message**

In `lib/api/handlers/triage.go`, change the `EmailsList` category validation message (line ~67) from `"category must be one of archive, reply, read, thinking, keep"` to:

```go
			writeError(w, r, http.StatusBadRequest, "category must be one of archive, reply, keep")
```

- [ ] **Step 8: Rewrite the prompt**

Replace the category list and safety rules in `lib/email/prompt.md` so only archive/reply/keep remain. Replace lines 6-39 with:

```markdown
Classify each message into exactly one category:

- `archive`: bulk mail Nat almost certainly doesn't need to see — newsletters,
  marketing, social and app notifications, automated receipts, system alerts.
  Art will remove it from the inbox (it stays searchable in All Mail).
- `reply`: a real person is waiting on a response from Nat. Provide a concise,
  ready-to-send `draft_reply` in Nat's voice (direct, friendly, lowercase-ok,
  no flowery filler). Leave `draft_reply` empty for every other category.
- `keep`: anything that should stay in Nat's inbox — mail worth his eyes, mail
  that needs thought or a decision before acting, anything personal or
  important, and anything you are unsure about. Left untouched in the inbox.

Also produce:
- `summary`: one or two plain sentences capturing what the email is and what,
  if anything, it asks of Nat.
- `reason`: a brief justification for the category you chose.
- `confidence`: 0.0–1.0, how sure you are of the category.

Safety rules — follow these strictly:

- Only choose `archive` for clearly automated or bulk mail, and only with high
  confidence. When in doubt between `archive` and anything else, do NOT archive
  — prefer `keep`.
- Be especially conservative with the work account (nat@laurel.ai): default to
  `keep` unless the message is obviously bulk/automated.
- Until you are given examples of Nat's past corrections, lean toward leaving
  mail in the inbox rather than archiving it.
- Never invent facts in a draft reply. If you lack the information to answer,
  classify as `keep` instead of guessing.
```

(The prompt is the one file where the existing wrapped style is kept — match the surrounding document.)

- [ ] **Step 9: Build and run the package tests**

Run: `go build ./... && go test ./lib/email/ ./lib/gmail/ ./lib/models/ -v`
Expected: PASS. (`go build` confirms no lingering references to the removed constants.)

- [ ] **Step 10: Commit**

```bash
git add lib/email/prompt.md lib/email/classifier.go lib/email/triager.go lib/gmail/labels.go lib/models/models.go lib/api/handlers/triage.go lib/email/triager_test.go lib/email/classifier_test.go
git commit -m "feat: simplify triage taxonomy to archive/reply/keep

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 4: Migrate retired categories in the database

**Files:**
- Modify: `lib/db/conn.go` (run an idempotent migration after AutoMigrate)
- Test: `lib/db/conn_test.go` (new)

**Interfaces:**
- Produces: `migrateEmailCategories(db *gorm.DB) error` — remaps `read`/`thinking` rows to `keep` and narrows the `category` CHECK constraint to `('archive','reply','keep')`. Idempotent; safe on a fresh DB.

- [ ] **Step 1: Write the failing test**

Create `lib/db/conn_test.go`:

```go
package db

import (
	"testing"

	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/testdb"
)

func TestMigrateEmailCategories(t *testing.T) {
	db := testdb.Open(t)

	// Simulate a pre-migration database by widening the constraint so the
	// retired values can be inserted.
	db.Exec(`ALTER TABLE email_messages DROP CONSTRAINT IF EXISTS chk_email_messages_category`)
	if err := db.Exec(`ALTER TABLE email_messages ADD CONSTRAINT chk_email_messages_category CHECK (category IN ('archive','reply','read','thinking','keep'))`).Error; err != nil {
		t.Fatalf("widen constraint: %v", err)
	}

	legacy := models.EmailMessage{
		RunID:          "00000000-0000-0000-0000-000000000001",
		AccountKind:    models.AccountPersonal,
		GmailMessageID: "leg1",
		Category:       models.EmailCategory("read"),
		Action:         models.EmailAction("read"),
	}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	if err := migrateEmailCategories(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var got models.EmailMessage
	if err := db.First(&got, "gmail_message_id = ?", "leg1").Error; err != nil {
		t.Fatal(err)
	}
	if got.Category != models.EmailKeep || got.Action != models.ActionKeep {
		t.Errorf("legacy row = %q/%q, want keep/keep", got.Category, got.Action)
	}

	// The narrowed constraint now rejects a retired value.
	if err := db.Exec(`UPDATE email_messages SET category='thinking' WHERE gmail_message_id='leg1'`).Error; err == nil {
		t.Error("narrowed constraint should reject category='thinking'")
	}

	// Idempotent: a second run is a no-op.
	if err := migrateEmailCategories(db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./lib/db/ -run TestMigrateEmailCategories -v`
Expected: COMPILE FAIL — `migrateEmailCategories` undefined.

- [ ] **Step 3: Implement the migration**

In `lib/db/conn.go`, add `"strings"` to the imports, call the migration after AutoMigrate, and add the function. The file becomes:

```go
// Package db opens and migrates the Postgres database used by the art server.
package db

import (
	"fmt"
	"strings"

	"github.com/icco/art/lib/models"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"moul.io/zapgorm2"
)

// Open connects to dsn, wires zap logging, and runs AutoMigrate.
func Open(dsn string, log *zap.Logger) (*gorm.DB, error) {
	// ErrRecordNotFound is expected control flow (e.g. a calendar's first
	// sync), not an error worth a stack trace.
	gormLog := zapgorm2.New(log)
	gormLog.IgnoreRecordNotFoundError = true
	cfg := &gorm.Config{Logger: gormLog}
	db, err := gorm.Open(postgres.Open(dsn), cfg)
	if err != nil {
		return nil, fmt.Errorf("gorm open: %w", err)
	}
	if err := db.AutoMigrate(models.All()...); err != nil {
		return nil, fmt.Errorf("auto-migrate: %w", err)
	}
	if err := migrateEmailCategories(db); err != nil {
		return nil, fmt.Errorf("migrate email categories: %w", err)
	}
	return db, nil
}

// migrateEmailCategories remaps the retired 'read'/'thinking' categories to
// 'keep' and narrows the category CHECK constraint to the current taxonomy.
// AutoMigrate creates a missing constraint but never alters an existing one,
// so this swap is explicit. Idempotent: a no-op once applied, safe on a fresh
// database. GORM names the field check constraint chk_<table>_<column>.
func migrateEmailCategories(db *gorm.DB) error {
	if err := db.Exec(
		`UPDATE email_messages SET category = 'keep', action = 'keep'
		 WHERE category IN ('read', 'thinking')`).Error; err != nil {
		return err
	}

	const name = "chk_email_messages_category"
	m := db.Migrator()
	if m.HasConstraint(&models.EmailMessage{}, name) {
		var def string
		if err := db.Raw(
			`SELECT pg_get_constraintdef(oid) FROM pg_constraint
			 WHERE conname = ? AND conrelid = 'email_messages'::regclass`, name).
			Scan(&def).Error; err != nil {
			return err
		}
		if !strings.Contains(def, "thinking") {
			return nil // already narrowed
		}
		if err := m.DropConstraint(&models.EmailMessage{}, name); err != nil {
			return err
		}
	}
	return db.Exec(
		`ALTER TABLE email_messages ADD CONSTRAINT chk_email_messages_category
		 CHECK (category IN ('archive', 'reply', 'keep'))`).Error
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./lib/db/ -run TestMigrateEmailCategories -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add lib/db/conn.go lib/db/conn_test.go
git commit -m "feat: migrate retired email categories to keep

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 5: Reverse a decision (gmail + email core)

**Files:**
- Modify: `lib/gmail/actions.go` (`DeleteDraft`)
- Modify: `lib/email/reconcile.go` (add `reversalMiscategorized` const + `buildCorrections` case)
- Create: `lib/email/reverse.go`
- Test: `lib/email/reverse_test.go` (new)

**Interfaces:**
- Consumes: `gmail.Client.ModifyLabels`, `gmail.Client.DeleteDraft`, `gmail.Client.EnsureLabels`; `gmail.LabelArchived`, `gmail.LabelReply`, `gmail.InboxLabel`; reversal consts from `reconcile.go`.
- Produces:
  - `gmail.Client.DeleteDraft(ctx context.Context, draftID string) error`
  - `email.Runner.Reverse(ctx context.Context, emailID string) (models.EmailMessage, error)` — loads the row, builds the account's client, undoes the action, records the reversal. Returns `gorm.ErrRecordNotFound` if the id is unknown; idempotent if already reversed.

- [ ] **Step 1: Add `DeleteDraft` to the gmail client**

In `lib/gmail/actions.go`, add after `GetDraft` (after line 61):

```go
// DeleteDraft removes a reply draft. A missing draft (already sent or deleted)
// is treated as success so reversal stays idempotent.
func (c *Client) DeleteDraft(ctx context.Context, draftID string) error {
	err := c.Service.Users.Drafts.Delete(User, draftID).Context(ctx).Do()
	if isNotFound(err) {
		return nil
	}
	return err
}
```

- [ ] **Step 2: Add the `miscategorized` reversal kind and correction**

In `lib/email/reconcile.go`, replace the reversal-kind const block (lines 14-17) with:

```go
// Reversal kinds recorded on EmailMessage.ReversalKind.
const (
	reversalUnarchived     = "unarchived"
	reversalDraftDeleted   = "draft_deleted"
	reversalMiscategorized = "miscategorized"
)
```

In `buildCorrections`, add a case to the `switch r.ReversalKind` (after the `reversalDraftDeleted` case, before the closing `}` of the switch):

```go
		case reversalMiscategorized:
			fmt.Fprintf(&b, "- You categorized mail from %q (subject %q) as %s; Nat marked that decision wrong — reconsider similar mail.\n", r.FromAddr, r.Subject, r.Category)
```

- [ ] **Step 3: Write the failing reversal tests**

Create `lib/email/reverse_test.go`:

```go
package email

import (
	"context"
	"slices"
	"testing"

	"github.com/icco/art/lib/gmail"
	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/testdb"
)

type fakeReverseGmail struct {
	modifyCalls []modifyCall
	deleted     []string
}

func (f *fakeReverseGmail) EnsureLabels(context.Context) (map[string]string, error) {
	return map[string]string{
		gmail.LabelTriaged:  "L_TRIAGED",
		gmail.LabelArchived: "L_ARCHIVED",
		gmail.LabelReply:    "L_REPLY",
	}, nil
}

func (f *fakeReverseGmail) ModifyLabels(_ context.Context, msgID string, add, remove []string) error {
	f.modifyCalls = append(f.modifyCalls, modifyCall{msgID, add, remove})
	return nil
}

func (f *fakeReverseGmail) DeleteDraft(_ context.Context, draftID string) error {
	f.deleted = append(f.deleted, draftID)
	return nil
}

func TestReverseDecisionArchived(t *testing.T) {
	gm := &fakeReverseGmail{}
	row := &models.EmailMessage{GmailMessageID: "m1", Action: models.ActionArchived, Applied: true}
	kind, err := reverseDecision(context.Background(), gm, row)
	if err != nil {
		t.Fatal(err)
	}
	if kind != reversalUnarchived {
		t.Errorf("kind = %q, want unarchived", kind)
	}
	if len(gm.modifyCalls) != 1 {
		t.Fatalf("modify calls = %d, want 1", len(gm.modifyCalls))
	}
	c := gm.modifyCalls[0]
	if !slices.Contains(c.add, gmail.InboxLabel) {
		t.Errorf("expected INBOX re-added, add=%v", c.add)
	}
	if !slices.Contains(c.remove, "L_ARCHIVED") {
		t.Errorf("expected Art/Archived removed, remove=%v", c.remove)
	}
}

func TestReverseDecisionReply(t *testing.T) {
	gm := &fakeReverseGmail{}
	row := &models.EmailMessage{GmailMessageID: "m2", Action: models.ActionReply, Applied: true, DraftID: "d2"}
	kind, err := reverseDecision(context.Background(), gm, row)
	if err != nil {
		t.Fatal(err)
	}
	if kind != reversalDraftDeleted {
		t.Errorf("kind = %q, want draft_deleted", kind)
	}
	if !slices.Contains(gm.deleted, "d2") {
		t.Errorf("expected draft d2 deleted, got %v", gm.deleted)
	}
	if len(gm.modifyCalls) != 1 || !slices.Contains(gm.modifyCalls[0].remove, "L_REPLY") {
		t.Errorf("expected Art/Reply removed, calls=%v", gm.modifyCalls)
	}
}

func TestReverseDecisionKeepIsNoGmail(t *testing.T) {
	gm := &fakeReverseGmail{}
	row := &models.EmailMessage{GmailMessageID: "m3", Action: models.ActionKeep, Applied: true}
	kind, err := reverseDecision(context.Background(), gm, row)
	if err != nil {
		t.Fatal(err)
	}
	if kind != reversalMiscategorized {
		t.Errorf("kind = %q, want miscategorized", kind)
	}
	if len(gm.modifyCalls) != 0 || len(gm.deleted) != 0 {
		t.Errorf("keep reversal must not touch Gmail: modify=%v delete=%v", gm.modifyCalls, gm.deleted)
	}
}

func TestReverseAndRecordWritesRow(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	row := &models.EmailMessage{
		RunID:          "00000000-0000-0000-0000-000000000001",
		AccountKind:    models.AccountPersonal,
		GmailMessageID: "m1",
		Category:       models.EmailArchive,
		Action:         models.ActionArchived,
		Applied:        true,
		Archived:       true,
	}
	if err := db.Create(row).Error; err != nil {
		t.Fatal(err)
	}

	out, err := reverseAndRecord(ctx, db, &fakeReverseGmail{}, row)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Reversed || out.ReversalKind != reversalUnarchived {
		t.Errorf("returned row = reversed:%v kind:%q", out.Reversed, out.ReversalKind)
	}

	var got models.EmailMessage
	if err := db.First(&got, "id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !got.Reversed || got.ReversalKind != reversalUnarchived || got.ReconciledAt == nil {
		t.Errorf("persisted row = reversed:%v kind:%q reconciledAt:%v", got.Reversed, got.ReversalKind, got.ReconciledAt)
	}
}

func TestRunnerReverseNotFound(t *testing.T) {
	db := testdb.Open(t)
	r := &Runner{DB: db}
	_, err := r.Reverse(context.Background(), "00000000-0000-0000-0000-0000000000ff")
	if err == nil {
		t.Fatal("expected error for unknown id")
	}
}

func TestRunnerReverseIdempotent(t *testing.T) {
	db := testdb.Open(t)
	row := &models.EmailMessage{
		RunID:          "00000000-0000-0000-0000-000000000001",
		AccountKind:    models.AccountPersonal,
		GmailMessageID: "m9",
		Category:       models.EmailArchive,
		Action:         models.ActionArchived,
		Applied:        true,
		Reversed:       true,
		ReversalKind:   reversalUnarchived,
	}
	if err := db.Create(row).Error; err != nil {
		t.Fatal(err)
	}
	// OAuth is nil; an already-reversed row must return before building a client.
	r := &Runner{DB: db}
	out, err := r.Reverse(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("idempotent reverse: %v", err)
	}
	if !out.Reversed {
		t.Error("expected reversed row returned unchanged")
	}
}
```

- [ ] **Step 4: Run the tests to verify they fail**

Run: `go test ./lib/email/ -run 'Reverse' -v`
Expected: COMPILE FAIL — `reverseDecision`, `reverseAndRecord`, `Runner.Reverse` undefined.

- [ ] **Step 5: Implement the reversal core**

Create `lib/email/reverse.go`:

```go
package email

import (
	"context"
	"time"

	"github.com/icco/art/lib/gmail"
	"github.com/icco/art/lib/models"
	"gorm.io/gorm"
)

// reversalGmailer is the subset of *gmail.Client a manual reversal needs.
type reversalGmailer interface {
	EnsureLabels(ctx context.Context) (map[string]string, error)
	ModifyLabels(ctx context.Context, msgID string, add, remove []string) error
	DeleteDraft(ctx context.Context, draftID string) error
}

// reverseDecision undoes the Gmail side-effect of row's action and returns the
// reversal_kind to record. Art/Triaged is intentionally kept so the message is
// not re-triaged into the same mistake. Dry-run rows (not applied) record the
// correction without touching Gmail.
func reverseDecision(ctx context.Context, gm reversalGmailer, row *models.EmailMessage) (string, error) {
	if !row.Applied {
		return reversalMiscategorized, nil
	}
	switch row.Action {
	case models.ActionArchived:
		labels, err := gm.EnsureLabels(ctx)
		if err != nil {
			return "", err
		}
		var remove []string
		if id := labels[gmail.LabelArchived]; id != "" {
			remove = append(remove, id)
		}
		if err := gm.ModifyLabels(ctx, row.GmailMessageID, []string{gmail.InboxLabel}, remove); err != nil {
			return "", err
		}
		return reversalUnarchived, nil
	case models.ActionReply:
		labels, err := gm.EnsureLabels(ctx)
		if err != nil {
			return "", err
		}
		if id := labels[gmail.LabelReply]; id != "" {
			if err := gm.ModifyLabels(ctx, row.GmailMessageID, nil, []string{id}); err != nil {
				return "", err
			}
		}
		if row.DraftID != "" {
			if err := gm.DeleteDraft(ctx, row.DraftID); err != nil {
				return "", err
			}
		}
		return reversalDraftDeleted, nil
	default:
		return reversalMiscategorized, nil
	}
}

// reverseAndRecord undoes the Gmail action and stamps the reversal columns so
// buildCorrections feeds the correction back to the classifier.
func reverseAndRecord(ctx context.Context, db *gorm.DB, gm reversalGmailer, row *models.EmailMessage) (models.EmailMessage, error) {
	kind, err := reverseDecision(ctx, gm, row)
	if err != nil {
		return models.EmailMessage{}, err
	}
	now := time.Now()
	if err := db.WithContext(ctx).Model(&models.EmailMessage{}).
		Where("id = ?", row.ID).
		Updates(map[string]any{"reversed": true, "reversal_kind": kind, "reconciled_at": &now}).Error; err != nil {
		return models.EmailMessage{}, err
	}
	row.Reversed = true
	row.ReversalKind = kind
	row.ReconciledAt = &now
	return *row, nil
}

// Reverse marks a triaged decision wrong: it undoes the Gmail side-effect and
// records the reversal. Idempotent — an already-reversed row is returned as-is.
// Returns gorm.ErrRecordNotFound when emailID is unknown.
func (r *Runner) Reverse(ctx context.Context, emailID string) (models.EmailMessage, error) {
	var row models.EmailMessage
	if err := r.DB.WithContext(ctx).First(&row, "id = ?", emailID).Error; err != nil {
		return models.EmailMessage{}, err
	}
	if row.Reversed {
		return row, nil
	}
	gm, err := gmail.NewClient(ctx, r.OAuth, row.AccountKind)
	if err != nil {
		return models.EmailMessage{}, err
	}
	return reverseAndRecord(ctx, r.DB, gm, &row)
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go build ./... && go test ./lib/email/ ./lib/gmail/ -run 'Reverse|Reconcile|BuildCorrections' -v`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add lib/gmail/actions.go lib/email/reconcile.go lib/email/reverse.go lib/email/reverse_test.go
git commit -m "feat: add manual decision reversal to email triage

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Task 6: Reverse endpoint + TUI action

**Files:**
- Modify: `lib/api/handlers/handlers.go` (extend `TriageService`)
- Modify: `lib/api/handlers/triage.go` (`EmailReverse` handler)
- Modify: `lib/api/router.go` (route)
- Test: `lib/api/handlers/handlers_test.go`
- Modify: `cli/tui/api_client.go` (`ReverseEmail`)
- Modify: `cli/tui/commands.go` (`reverseEmail` cmd)
- Modify: `cli/tui/keys.go` (`Reject` binding)
- Modify: `cli/tui/digest.go` (confirm + reverse flow)

**Interfaces:**
- Consumes: `email.Runner.Reverse` (Task 5).
- Produces: `POST /emails/{id}/reverse` returning the updated `EmailMessage`; `TUI Client.ReverseEmail(ctx, id) (Email, error)`.

- [ ] **Step 1: Extend the service interface and write the failing handler test**

In `lib/api/handlers/handlers.go`, add `"github.com/icco/art/lib/models"` to the imports and extend `TriageService`:

```go
		// TriageService executes an email-triage pass across all linked accounts.
		TriageService interface {
			RunAll(ctx context.Context) error
			Reverse(ctx context.Context, id string) (models.EmailMessage, error)
		}
```

In `lib/api/handlers/handlers_test.go`, add `"context"` and `"gorm.io/gorm"` to the imports, then add a stub and a test:

```go
type stubTriage struct {
	msg models.EmailMessage
	err error
}

func (s stubTriage) RunAll(context.Context) error { return nil }
func (s stubTriage) Reverse(context.Context, string) (models.EmailMessage, error) {
	return s.msg, s.err
}

func TestEmailReverse(t *testing.T) {
	db := testdb.Open(t)

	h := &handlers.Handlers{DB: db, Triage: stubTriage{
		msg: models.EmailMessage{Action: models.ActionArchived, Reversed: true, ReversalKind: "unarchived"},
	}}
	r := chi.NewRouter()
	r.Post("/emails/{id}/reverse", h.EmailReverse)

	w := do(t, r, "POST", "/emails/abc/reverse", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("reverse: %d %s", w.Code, w.Body)
	}
	var got models.EmailMessage
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if !got.Reversed {
		t.Error("expected reversed row in response")
	}

	h2 := &handlers.Handlers{DB: db, Triage: stubTriage{err: gorm.ErrRecordNotFound}}
	r2 := chi.NewRouter()
	r2.Post("/emails/{id}/reverse", h2.EmailReverse)
	if w := do(t, r2, "POST", "/emails/missing/reverse", nil); w.Code != http.StatusNotFound {
		t.Fatalf("unknown id: got %d, want 404", w.Code)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./lib/api/handlers/ -run TestEmailReverse -v`
Expected: COMPILE FAIL — `EmailReverse` undefined.

- [ ] **Step 3: Implement the handler**

In `lib/api/handlers/triage.go`, add `"errors"`, `"github.com/go-chi/chi/v5"`, and `"gorm.io/gorm"` to the imports, then add:

```go
// EmailReverse marks a triaged decision wrong: it undoes the Gmail action and
// records the reversal so the classifier learns. Returns the updated row.
func (h *Handlers) EmailReverse(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	msg, err := h.Triage.Reverse(r.Context(), id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			writeError(w, r, http.StatusNotFound, "email not found")
			return
		}
		writeServerError(w, r, "email reverse", err)
		return
	}
	writeJSON(w, r, http.StatusOK, msg)
}
```

- [ ] **Step 4: Add the route**

In `lib/api/router.go`, after the `/emails` GET (line 71), add:

```go
		r.Post("/emails/{id}/reverse", d.H.EmailReverse)
```

- [ ] **Step 5: Run the handler test to verify it passes**

Run: `go build ./... && go test ./lib/api/... -run TestEmailReverse -v`
Expected: PASS.

- [ ] **Step 6: Add the TUI API client method**

In `cli/tui/api_client.go`, after `ListEmails` (line ~289), add:

```go
// ReverseEmail marks a triaged decision bad and undoes it server-side.
func (c *Client) ReverseEmail(ctx context.Context, id string) (Email, error) {
	var out Email
	return out, c.do(ctx, "POST", "/emails/"+id+"/reverse", nil, &out)
}
```

- [ ] **Step 7: Add the TUI command**

In `cli/tui/commands.go`, after `loadEmails` (line ~76), add:

```go
func reverseEmail(c *Client, id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := bg()
		defer cancel()
		if _, err := c.ReverseEmail(ctx, id); err != nil {
			return errMsg{err}
		}
		return statusMsg("decision reversed")
	}
}
```

- [ ] **Step 8: Add the key binding**

In `cli/tui/keys.go`: add a field to `keyMap` (after `Triage key.Binding`, before `Back`):

```go
	Reject key.Binding
```

In `defaultKeyMap`, add (after the `Triage:` line):

```go
		Reject: key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "mark bad")),
```

In `FullHelp`, add `k.Reject` to the second row so it surfaces in help:

```go
		{k.PrevWeek, k.NextWeek, k.Add, k.Edit, k.Delete, k.Reject},
```

- [ ] **Step 9: Rewrite the digest page with a confirm + reverse flow**

Replace the entire contents of `cli/tui/digest.go` with:

```go
package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
)

type emailItem struct{ e Email }

func (i emailItem) FilterValue() string { return i.e.Subject }
func (i emailItem) Title() string {
	tag := i.e.Action
	switch {
	case i.e.Reversed:
		tag = "↶" + tag
	case !i.e.Applied:
		tag = "~" + tag // proposed only (dry run)
	}
	return fmt.Sprintf("%-9s %s", tag, i.e.Subject)
}

func (i emailItem) Description() string {
	from := truncate(i.e.From, 32)
	if i.e.Summary != "" {
		return from + " · " + i.e.Summary
	}
	return from
}

// confirmData holds the confirm value on the heap so huh's *bool binding
// survives the value-receiver page being copied each Update.
type confirmData struct{ ok bool }

// digestPage lists triaged email and what art proposed/did with each. Triage
// itself is launched with the global `t` key; `x` marks a decision bad and
// undoes it after a confirm.
type digestPage struct {
	client        *Client
	width, height int
	list          list.Model
	keys          keyMap

	form      *huh.Form
	cf        *confirmData
	reverseID string
}

func newDigestPage(c *Client, isDark bool) digestPage {
	d := list.NewDefaultDelegate()
	d.Styles = list.NewDefaultItemStyles(isDark)
	l := list.New(nil, d, 0, 0)
	l.Title = "Email digest"
	l.SetShowHelp(false)
	return digestPage{client: c, list: l, keys: defaultKeyMap()}
}

func (p digestPage) Title() string   { return "digest" }
func (p digestPage) FullInput() bool { return p.form != nil || p.list.SettingFilter() }

func (p digestPage) Init() tea.Cmd { return loadEmails(p.client) }

func (p digestPage) Update(msg tea.Msg) (Page, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		p.width, p.height = m.Width, m.Height
		p.list.SetSize(m.Width, m.Height)
		if p.form != nil {
			p.form = p.form.WithWidth(m.Width).WithHeight(m.Height)
		}
		return p, nil
	case emailsMsg:
		return p, p.list.SetItems(emailItems(m.emails))
	case tea.KeyPressMsg:
		if p.form != nil {
			return p.updateForm(m)
		}
		return p.handleKey(m)
	}
	if p.form != nil {
		return p.updateForm(msg)
	}
	return p, nil
}

func (p digestPage) handleKey(m tea.KeyPressMsg) (Page, tea.Cmd) {
	if p.list.SettingFilter() {
		var cmd tea.Cmd
		p.list, cmd = p.list.Update(m)
		return p, cmd
	}
	if key.Matches(m, p.keys.Reject) {
		if it, ok := p.list.SelectedItem().(emailItem); ok {
			p.cf = &confirmData{}
			p.reverseID = it.e.ID
			p.form = newConfirmForm(p.cf, it.e.Subject, p.width, p.height)
			return p, p.form.Init()
		}
	}
	var cmd tea.Cmd
	p.list, cmd = p.list.Update(m)
	return p, cmd
}

func (p digestPage) updateForm(msg tea.Msg) (Page, tea.Cmd) {
	form, cmd := p.form.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		p.form = f
	}
	switch p.form.State {
	case huh.StateCompleted:
		confirmed, id := p.cf.ok, p.reverseID
		p.form, p.cf, p.reverseID = nil, nil, ""
		if confirmed {
			return p, tea.Sequence(reverseEmail(p.client, id), loadEmails(p.client))
		}
		return p, nil
	case huh.StateAborted:
		p.form, p.cf, p.reverseID = nil, nil, ""
		return p, nil
	}
	return p, cmd
}

func (p digestPage) View() string {
	if p.form != nil {
		return p.form.View()
	}
	if len(p.list.Items()) == 0 {
		return strings.TrimRight(p.list.View(), "\n") + "\n\n" + faintStyle.Render("No triaged mail. Press t to run triage.")
	}
	return p.list.View()
}

func emailItems(emails []Email) []list.Item {
	items := make([]list.Item, len(emails))
	for i, e := range emails {
		items[i] = emailItem{e}
	}
	return items
}

func newConfirmForm(cf *confirmData, subject string, w, h int) *huh.Form {
	form := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().
			Title("Mark this decision bad and undo it?").
			Description(truncate(subject, 60)).
			Value(&cf.ok),
	))
	if w > 0 {
		form = form.WithWidth(w).WithHeight(h)
	}
	return form
}
```

- [ ] **Step 10: Build and run the TUI tests**

Run: `go build ./... && go test ./cli/tui/... -v`
Expected: PASS (existing `triage_test.go`/`app_test.go` still green; the digest page compiles with the new form flow).

- [ ] **Step 11: Commit**

```bash
git add lib/api/handlers/handlers.go lib/api/handlers/triage.go lib/api/handlers/handlers_test.go lib/api/router.go cli/tui/api_client.go cli/tui/commands.go cli/tui/keys.go cli/tui/digest.go
git commit -m "feat: mark a triaged email bad and undo it from the tui

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

### Final verification

- [ ] **Step 1: Full build, vet, lint, tests**

Run:

```bash
go build ./...
go vet ./...
golangci-lint run
TEST_DATABASE_URL=$TEST_DATABASE_URL go test ./... -cover
```

Expected: build/vet/lint clean; all packages PASS; coverage ≥ 50%.

- [ ] **Step 2: Push and open the PR**

```bash
git push -u origin email-triage-refinements
gh pr create --title "feat: email triage refinements" --body "$(cat <<'EOF'
- widen triage window to 7d, raise per-run cap to 1000 (both accounts)
- log and count reconcile reversal-check failures in the run summary
- simplify taxonomy to archive/reply/keep; migrate retired read/thinking rows to keep
- add a TUI `x` action to mark a decision bad and undo it (un-archive / delete draft), feeding the classifier corrections

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

(Only push/PR once Nat asks — see the no-push-without-asking rule.)
