package email

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/testdb"
	"gorm.io/gorm"
)

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

func seedRow(t *testing.T, db *gorm.DB, msgID string, action models.EmailAction, applied bool, draftID string, cat models.EmailCategory) {
	t.Helper()
	row := models.EmailMessage{
		RunID:          "00000000-0000-0000-0000-000000000001",
		AccountKind:    models.AccountPersonal,
		GmailMessageID: msgID,
		Subject:        "Subj " + msgID,
		FromAddr:       "sender@example.com",
		Category:       cat,
		Action:         action,
		Applied:        applied,
		DraftID:        draftID,
		Archived:       action == models.ActionArchived,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed %s: %v", msgID, err)
	}
}

func TestReconcileDetectsReversals(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()

	seedRow(t, db, "g1", models.ActionArchived, true, "", models.EmailArchive)  // un-archived
	seedRow(t, db, "g2", models.ActionArchived, true, "", models.EmailArchive)  // still archived
	seedRow(t, db, "g3", models.ActionReply, true, "d3", models.EmailReply)     // draft deleted
	seedRow(t, db, "g4", models.ActionReply, true, "d4", models.EmailReply)     // draft kept
	seedRow(t, db, "g5", models.ActionArchived, false, "", models.EmailArchive) // dry-run: ignored

	gm := &fakeReconcileGmail{
		inInbox:     map[string]bool{"g1": true, "g2": false, "g5": true},
		draftExists: map[string]bool{"d3": false, "d4": true},
	}
	if _, err := Reconcile(ctx, db, models.AccountPersonal, gm, 14, 50); err != nil {
		t.Fatal(err)
	}

	get := func(id string) models.EmailMessage {
		var r models.EmailMessage
		if err := db.Where("gmail_message_id = ?", id).First(&r).Error; err != nil {
			t.Fatalf("load %s: %v", id, err)
		}
		return r
	}

	if r := get("g1"); !r.Reversed || r.ReversalKind != reversalUnarchived {
		t.Errorf("g1 = reversed:%v kind:%q, want unarchived", r.Reversed, r.ReversalKind)
	}
	if r := get("g3"); !r.Reversed || r.ReversalKind != reversalDraftDeleted {
		t.Errorf("g3 = reversed:%v kind:%q, want draft_deleted", r.Reversed, r.ReversalKind)
	}
	if r := get("g2"); r.Reversed {
		t.Error("g2 (still archived) should not be reversed")
	}
	if r := get("g4"); r.Reversed {
		t.Error("g4 (draft kept) should not be reversed")
	}
	if r := get("g5"); r.Reversed || r.ReconciledAt != nil {
		t.Error("g5 (dry-run, applied=false) must be excluded from reconcile")
	}

	corr, err := buildCorrections(ctx, db, 14, 15)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(corr, "moved it back") || !strings.Contains(corr, "discarded") {
		t.Errorf("corrections missing expected guidance:\n%s", corr)
	}
}

func TestBuildCorrectionsEmpty(t *testing.T) {
	db := testdb.Open(t)
	got, err := buildCorrections(context.Background(), db, 14, 15)
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("no reversals should yield empty corrections, got %q", got)
	}
}

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
