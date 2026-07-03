package email

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/icco/art/lib/gmail"
	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/testdb"
)

func TestDecideAction(t *testing.T) {
	const threshold = 0.8
	cases := []struct {
		name        string
		cat         models.EmailCategory
		conf        float64
		wantAction  models.EmailAction
		wantArchive bool
		wantLabel   string
	}{
		{"archive high confidence", models.EmailArchive, 0.95, models.ActionArchived, true, gmail.LabelArchived},
		{"archive low confidence downgrades to keep", models.EmailArchive, 0.5, models.ActionKeep, false, ""},
		{"reply labels only", models.EmailReply, 0.9, models.ActionReply, false, gmail.LabelReply},
		{"keep is inert", models.EmailKeep, 0.9, models.ActionKeep, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := decideAction(c.cat, c.conf, threshold)
			if d.Action != c.wantAction {
				t.Errorf("action: got %q want %q", d.Action, c.wantAction)
			}
			if d.RemoveInbox != c.wantArchive {
				t.Errorf("removeInbox: got %v want %v", d.RemoveInbox, c.wantArchive)
			}
			if !slices.Contains(d.AddLabels, gmail.LabelTriaged) {
				t.Errorf("every action must add %q, got %v", gmail.LabelTriaged, d.AddLabels)
			}
			if c.wantLabel != "" && !slices.Contains(d.AddLabels, c.wantLabel) {
				t.Errorf("expected label %q in %v", c.wantLabel, d.AddLabels)
			}
		})
	}
}

// --- fakes ---

type fakeGmail struct {
	ids         []string
	msgs        map[string]*gmail.Message
	modifyCalls []modifyCall
	lastQuery   string
}

type modifyCall struct {
	msgID  string
	add    []string
	remove []string
}

func (f *fakeGmail) EnsureLabels(context.Context) (map[string]string, error) {
	return map[string]string{
		gmail.LabelTriaged:  "L_TRIAGED",
		gmail.LabelArchived: "L_ARCHIVED",
		gmail.LabelReply:    "L_REPLY",
	}, nil
}

func (f *fakeGmail) FetchMessageIDs(_ context.Context, query string, _ int) ([]string, error) {
	f.lastQuery = query
	return f.ids, nil
}

func (f *fakeGmail) GetMessage(_ context.Context, id string) (*gmail.Message, error) {
	return f.msgs[id], nil
}

func (f *fakeGmail) ModifyLabels(_ context.Context, msgID string, add, remove []string) error {
	f.modifyCalls = append(f.modifyCalls, modifyCall{msgID, add, remove})
	return nil
}

type fakeClassifier struct{ byID map[string]Classification }

func (f *fakeClassifier) Classify(_ context.Context, m *gmail.Message) (Classification, error) {
	return f.byID[m.ID], nil
}

func newTriager(t *testing.T, dryRun bool, byID map[string]Classification) (*Triager, *fakeGmail) {
	t.Helper()
	db := testdb.Open(t)
	gm := &fakeGmail{
		ids:  []string{"m1", "m2"},
		msgs: map[string]*gmail.Message{},
	}
	for id := range byID {
		gm.msgs[id] = &gmail.Message{ID: id, ThreadID: "t_" + id, From: "x@example.com", Subject: "Subj " + id}
	}
	tr := &Triager{
		DB:                  db,
		Classifier:          &fakeClassifier{byID: byID},
		BackfillDays:        14,
		MaxPerRun:           50,
		ConfidenceThreshold: 0.8,
		DryRun:              dryRun,
	}
	return tr, gm
}

func TestUpsertClearsReversalState(t *testing.T) {
	db := testdb.Open(t)
	tr := &Triager{DB: db}
	now := time.Now()
	old := models.EmailMessage{
		RunID: "00000000-0000-0000-0000-000000000001", AccountKind: models.AccountPersonal,
		GmailMessageID: "g1", Category: models.EmailKeep, Applied: false,
		Reversed: true, ReversalKind: reversalMiscategorized, ReconciledAt: &now,
	}
	if err := db.Create(&old).Error; err != nil {
		t.Fatal(err)
	}

	fresh := models.EmailMessage{
		RunID: "00000000-0000-0000-0000-000000000002", AccountKind: models.AccountPersonal,
		GmailMessageID: "g1", Category: models.EmailArchive, Applied: true,
	}
	if err := tr.upsert(context.Background(), &fresh); err != nil {
		t.Fatal(err)
	}

	var got models.EmailMessage
	if err := db.First(&got, "gmail_message_id = ?", "g1").Error; err != nil {
		t.Fatal(err)
	}
	if got.Reversed || got.ReversalKind != "" || got.ReconciledAt != nil {
		t.Fatalf("reversal state must reset on re-triage: %+v", got)
	}
}

func TestRunAccountApplies(t *testing.T) {
	byID := map[string]Classification{
		"m1": {Category: models.EmailArchive, Confidence: 0.95, Summary: "junk"},
		"m2": {Category: models.EmailReply, Confidence: 0.9, Summary: "needs reply"},
	}
	tr, gm := newTriager(t, false, byID)
	counts := map[string]int{}

	n, err := tr.RunAccount(context.Background(), uuid.NewString(), models.AccountPersonal, gm, counts)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("processed %d want 2", n)
	}

	// Art only ever reads the inbox — the fetch query must stay inbox-scoped.
	if !strings.Contains(gm.lastQuery, "in:inbox") {
		t.Errorf("fetch query must be inbox-only, got %q", gm.lastQuery)
	}

	// The archived message must have INBOX removed; both must get Art/Triaged;
	// the reply gets Art/Reply but nothing is ever drafted (the Gmailer the
	// triager holds has no way to create a draft).
	var sawArchive, sawReplyLabel bool
	for _, c := range gm.modifyCalls {
		if slices.Contains(c.remove, gmail.InboxLabel) {
			sawArchive = true
		}
		if slices.Contains(c.add, "L_REPLY") {
			sawReplyLabel = true
		}
		if !slices.Contains(c.add, "L_TRIAGED") {
			t.Errorf("modify %s missing Art/Triaged label, add=%v", c.msgID, c.add)
		}
	}
	if !sawArchive {
		t.Error("expected one message to be archived (INBOX removed)")
	}
	if !sawReplyLabel {
		t.Error("expected the reply message to get the Art/Reply label")
	}

	var rows []models.EmailMessage
	if err := tr.DB.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("persisted %d rows want 2", len(rows))
	}
	for _, r := range rows {
		if !r.Applied {
			t.Errorf("row %s not marked applied", r.GmailMessageID)
		}
		if r.Action == models.ActionArchived && !r.Archived {
			t.Errorf("archived row %s missing Archived flag", r.GmailMessageID)
		}
	}
}

func TestRunAccountDryRun(t *testing.T) {
	byID := map[string]Classification{
		"m1": {Category: models.EmailArchive, Confidence: 0.95},
		"m2": {Category: models.EmailReply, Confidence: 0.9},
	}
	tr, gm := newTriager(t, true, byID)

	if _, err := tr.RunAccount(context.Background(), uuid.NewString(), models.AccountPersonal, gm, map[string]int{}); err != nil {
		t.Fatal(err)
	}
	if len(gm.modifyCalls) != 0 {
		t.Errorf("dry run touched Gmail: modify=%d", len(gm.modifyCalls))
	}
	var rows []models.EmailMessage
	if err := tr.DB.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("dry run persisted %d rows want 2", len(rows))
	}
	for _, r := range rows {
		if r.Applied {
			t.Errorf("dry run row %s marked applied", r.GmailMessageID)
		}
	}
}

func TestRunAccountIdempotent(t *testing.T) {
	byID := map[string]Classification{
		"m1": {Category: models.EmailKeep, Confidence: 0.9},
		"m2": {Category: models.EmailKeep, Confidence: 0.9},
	}
	tr, gm := newTriager(t, false, byID)
	ctx := context.Background()

	if _, err := tr.RunAccount(ctx, uuid.NewString(), models.AccountPersonal, gm, map[string]int{}); err != nil {
		t.Fatal(err)
	}
	// Second pass over the same messages must upsert, not duplicate.
	if _, err := tr.RunAccount(ctx, uuid.NewString(), models.AccountPersonal, gm, map[string]int{}); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := tr.DB.Model(&models.EmailMessage{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected 2 rows after re-run, got %d", count)
	}
}
