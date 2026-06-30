package email

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/testdb"
	"gorm.io/gorm"
)

// seedReversed inserts an already-reversed message so buildCorrections can pick
// it up; corrections only ever come from decisions Nat manually reversed.
func seedReversed(t *testing.T, db *gorm.DB, msgID string, cat models.EmailCategory, kind string) {
	t.Helper()
	now := time.Now()
	row := models.EmailMessage{
		RunID:          "00000000-0000-0000-0000-000000000001",
		AccountKind:    models.AccountPersonal,
		GmailMessageID: msgID,
		Subject:        "Subj " + msgID,
		FromAddr:       "sender@example.com",
		Category:       cat,
		Applied:        true,
		Reversed:       true,
		ReversalKind:   kind,
		ReconciledAt:   &now,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed %s: %v", msgID, err)
	}
}

func TestBuildCorrections(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()

	seedReversed(t, db, "g1", models.EmailArchive, reversalUnarchived)
	seedReversed(t, db, "g2", models.EmailReply, reversalReplyDismissed)

	corr, err := buildCorrections(ctx, db, 14, 15)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(corr, "moved it back") {
		t.Errorf("corrections missing un-archive guidance:\n%s", corr)
	}
	if !strings.Contains(corr, "needing a reply") {
		t.Errorf("corrections missing reply guidance:\n%s", corr)
	}
	// The corrections block must never suggest art drafts mail.
	if strings.Contains(strings.ToLower(corr), "drafted") {
		t.Errorf("corrections must not mention drafting:\n%s", corr)
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
