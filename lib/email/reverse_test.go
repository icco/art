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
	row := &models.EmailMessage{GmailMessageID: "m2", Action: models.ActionReply, Applied: true}
	kind, err := reverseDecision(context.Background(), gm, row)
	if err != nil {
		t.Fatal(err)
	}
	if kind != reversalReplyDismissed {
		t.Errorf("kind = %q, want reply_dismissed", kind)
	}
	// Reversing a reply only removes the Art/Reply label — there is no draft to
	// delete, because art never created one.
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
	if len(gm.modifyCalls) != 0 {
		t.Errorf("keep reversal must not touch Gmail: modify=%v", gm.modifyCalls)
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
