package email

import (
	"context"
	"slices"
	"testing"

	"github.com/icco/art/lib/gmail"
	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/testdb"
)

func TestSetArchivedFromKeepRecordsCorrection(t *testing.T) {
	db := testdb.Open(t)
	gm := &fakeReverseGmail{}
	row := &models.EmailMessage{
		RunID:          "00000000-0000-0000-0000-000000000001",
		AccountKind:    models.AccountPersonal,
		GmailMessageID: "k1",
		Category:       models.EmailKeep,
		Action:         models.ActionKeep,
		Applied:        true,
		Archived:       false,
	}
	if err := db.Create(row).Error; err != nil {
		t.Fatal(err)
	}

	out, err := setArchivedAndRecord(context.Background(), db, gm, row, true)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Archived || out.Action != models.ActionArchived {
		t.Errorf("row = archived:%v action:%q, want true/archived", out.Archived, out.Action)
	}
	// Archiving a 'keep' email disagrees with art, so it is a learning signal.
	if !out.Reversed || out.ReversalKind != reversalManualArchived {
		t.Errorf("learning = reversed:%v kind:%q, want true/manual_archived", out.Reversed, out.ReversalKind)
	}
	if len(gm.modifyCalls) != 1 {
		t.Fatalf("modify calls = %d, want 1", len(gm.modifyCalls))
	}
	c := gm.modifyCalls[0]
	if !slices.Contains(c.add, "L_ARCHIVED") {
		t.Errorf("expected Art/Archived added, add=%v", c.add)
	}
	if !slices.Contains(c.remove, gmail.InboxLabel) {
		t.Errorf("expected INBOX removed, remove=%v", c.remove)
	}

	var got models.EmailMessage
	if err := db.First(&got, "id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !got.Archived || got.ReversalKind != reversalManualArchived || got.ReconciledAt == nil {
		t.Errorf("persisted = archived:%v kind:%q reconciledAt:%v", got.Archived, got.ReversalKind, got.ReconciledAt)
	}
}

func TestSetArchivedAgreementRecordsNoCorrection(t *testing.T) {
	db := testdb.Open(t)
	gm := &fakeReverseGmail{}
	row := &models.EmailMessage{
		RunID:          "00000000-0000-0000-0000-000000000001",
		AccountKind:    models.AccountPersonal,
		GmailMessageID: "a1",
		Category:       models.EmailArchive,
		Action:         models.ActionNone,
		Applied:        false,
		Archived:       false,
	}
	if err := db.Create(row).Error; err != nil {
		t.Fatal(err)
	}

	out, err := setArchivedAndRecord(context.Background(), db, gm, row, true)
	if err != nil {
		t.Fatal(err)
	}
	// Archiving an 'archive' email agrees with art: apply it, but no correction.
	if out.Reversed || out.ReversalKind != "" {
		t.Errorf("agreement must not record a correction: reversed:%v kind:%q", out.Reversed, out.ReversalKind)
	}
	if !out.Applied || !out.Archived {
		t.Errorf("expected applied+archived, got applied:%v archived:%v", out.Applied, out.Archived)
	}
}

func TestSetArchivedUnarchiveRecordsUnarchived(t *testing.T) {
	db := testdb.Open(t)
	gm := &fakeReverseGmail{}
	row := &models.EmailMessage{
		RunID:          "00000000-0000-0000-0000-000000000001",
		AccountKind:    models.AccountPersonal,
		GmailMessageID: "u1",
		Category:       models.EmailArchive,
		Action:         models.ActionArchived,
		Applied:        true,
		Archived:       true,
	}
	if err := db.Create(row).Error; err != nil {
		t.Fatal(err)
	}

	out, err := setArchivedAndRecord(context.Background(), db, gm, row, false)
	if err != nil {
		t.Fatal(err)
	}
	if out.Archived || out.Action != models.ActionKeep {
		t.Errorf("row = archived:%v action:%q, want false/keep", out.Archived, out.Action)
	}
	if !out.Reversed || out.ReversalKind != reversalUnarchived {
		t.Errorf("learning = reversed:%v kind:%q, want true/unarchived", out.Reversed, out.ReversalKind)
	}
	c := gm.modifyCalls[0]
	if !slices.Contains(c.add, gmail.InboxLabel) {
		t.Errorf("expected INBOX added, add=%v", c.add)
	}
	if !slices.Contains(c.remove, "L_ARCHIVED") {
		t.Errorf("expected Art/Archived removed, remove=%v", c.remove)
	}
}

func TestSetArchivedToggleBackClearsCorrection(t *testing.T) {
	db := testdb.Open(t)
	gm := &fakeReverseGmail{}
	// A 'keep' email that was manually archived (correction recorded).
	row := &models.EmailMessage{
		RunID:          "00000000-0000-0000-0000-000000000001",
		AccountKind:    models.AccountPersonal,
		GmailMessageID: "t1",
		Category:       models.EmailKeep,
		Action:         models.ActionArchived,
		Applied:        true,
		Archived:       true,
		Reversed:       true,
		ReversalKind:   reversalManualArchived,
	}
	if err := db.Create(row).Error; err != nil {
		t.Fatal(err)
	}

	// Toggling back to the inbox returns to art's original 'keep' — agreement,
	// so the earlier correction must be cleared, not a new one recorded.
	out, err := setArchivedAndRecord(context.Background(), db, gm, row, false)
	if err != nil {
		t.Fatal(err)
	}
	if out.Reversed || out.ReversalKind != "" {
		t.Errorf("toggle-back should clear correction: reversed:%v kind:%q", out.Reversed, out.ReversalKind)
	}
	var got models.EmailMessage
	if err := db.First(&got, "id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Reversed || got.ReversalKind != "" {
		t.Errorf("persisted correction not cleared: reversed:%v kind:%q", got.Reversed, got.ReversalKind)
	}
}

func TestSetArchivedNoOp(t *testing.T) {
	db := testdb.Open(t)
	gm := &fakeReverseGmail{}
	row := &models.EmailMessage{
		RunID:          "00000000-0000-0000-0000-000000000001",
		AccountKind:    models.AccountPersonal,
		GmailMessageID: "n1",
		Category:       models.EmailArchive,
		Action:         models.ActionArchived,
		Applied:        true,
		Archived:       true,
	}
	if err := db.Create(row).Error; err != nil {
		t.Fatal(err)
	}
	out, err := setArchivedAndRecord(context.Background(), db, gm, row, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(gm.modifyCalls) != 0 {
		t.Errorf("already-archived no-op must not touch Gmail: %v", gm.modifyCalls)
	}
	if !out.Archived {
		t.Error("expected row returned unchanged")
	}
}

func TestRunnerSetArchivedNotFound(t *testing.T) {
	db := testdb.Open(t)
	r := &Runner{DB: db}
	_, err := r.SetArchived(context.Background(), "00000000-0000-0000-0000-0000000000ff", true)
	if err == nil {
		t.Fatal("expected error for unknown id")
	}
}

func TestRunnerSetArchivedNoOpSkipsClient(t *testing.T) {
	db := testdb.Open(t)
	row := &models.EmailMessage{
		RunID:          "00000000-0000-0000-0000-000000000001",
		AccountKind:    models.AccountPersonal,
		GmailMessageID: "n2",
		Category:       models.EmailArchive,
		Action:         models.ActionArchived,
		Applied:        true,
		Archived:       true,
	}
	if err := db.Create(row).Error; err != nil {
		t.Fatal(err)
	}
	// OAuth is nil; a no-op must return before building a Gmail client.
	r := &Runner{DB: db}
	out, err := r.SetArchived(context.Background(), row.ID, true)
	if err != nil {
		t.Fatalf("no-op set-archived: %v", err)
	}
	if !out.Archived {
		t.Error("expected row returned unchanged")
	}
}
