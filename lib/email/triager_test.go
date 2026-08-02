package email

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/icco/art/lib/cost"
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
		mailingList bool
		wantAction  models.EmailAction
		wantArchive bool
		wantLabel   string
	}{
		{"archive high confidence", models.EmailArchive, 0.95, false, models.ActionArchived, true, gmail.LabelArchived},
		{"archive low confidence downgrades to keep", models.EmailArchive, 0.5, false, models.ActionKeep, false, ""},
		{"mailinglist is never archived", models.EmailArchive, 1.0, true, models.ActionKeep, false, ""},
		{"reply labels only", models.EmailReply, 0.9, false, models.ActionReply, false, gmail.LabelReply},
		{"mailinglist reply still gets flagged", models.EmailReply, 0.9, true, models.ActionReply, false, gmail.LabelReply},
		{"keep is inert", models.EmailKeep, 0.9, false, models.ActionKeep, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := decideAction(c.cat, c.conf, threshold, c.mailingList)
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

func TestMailingListLabelIDs(t *testing.T) {
	got := mailingListLabelIDs(map[string]string{
		"mailinglist":             "L1",
		"mailinglist/golang-nuts": "L2",
		"mailinglistings":         "L3",
		"art/triaged":             "L4",
	})
	slices.Sort(got)
	want := []string{"L1", "L2"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v want %v — the label and its sublabels, nothing else", got, want)
	}
	if ids := mailingListLabelIDs(map[string]string{"art/triaged": "L4"}); len(ids) != 0 {
		t.Errorf("an account without the label must yield none, got %v", ids)
	}
}

// --- fakes ---

type fakeGmail struct {
	ids            []string
	msgs           map[string]*gmail.Message
	modifyCalls    []modifyCall
	lastQuery      string
	hasMailingList bool
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

func (f *fakeGmail) LabelIDsByName(context.Context) (map[string]string, error) {
	all := map[string]string{
		"art/triaged": "L_TRIAGED", "art/archived": "L_ARCHIVED", "art/reply": "L_REPLY",
	}
	if f.hasMailingList {
		all[gmail.LabelMailingList] = "L_MAILINGLIST"
		all[gmail.LabelMailingList+"/golang-nuts"] = "L_ML_GOLANG"
		all[gmail.LabelMailingList+"ings"] = "L_NOT_ML"
	}
	return all, nil
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

type budgetSpentClassifier struct{ calls int }

func (f *budgetSpentClassifier) Classify(context.Context, *gmail.Message) (Classification, error) {
	f.calls++
	return Classification{}, &cost.ErrBudgetExhausted{SpentUSD: 2.5, BudgetUSD: 2.0}
}

func TestRunAccountStopsOnBudgetExhausted(t *testing.T) {
	db := testdb.Open(t)
	gm := &fakeGmail{
		ids: []string{"m1", "m2", "m3"},
		msgs: map[string]*gmail.Message{
			"m1": {ID: "m1"}, "m2": {ID: "m2"}, "m3": {ID: "m3"},
		},
	}
	cl := &budgetSpentClassifier{}
	tr := &Triager{DB: db, Classifier: cl, BackfillDays: 14, MaxPerRun: 50, ConfidenceThreshold: 0.8}

	summary := map[string]int{}
	runID := uuid.NewString()
	processed, err := tr.RunAccount(context.Background(), runID, models.AccountPersonal, gm, summary)
	if err != nil {
		t.Fatalf("a spent budget is not a run failure: %v", err)
	}
	if processed != 0 {
		t.Errorf("processed = %d, want 0", processed)
	}
	if cl.calls != 1 {
		t.Errorf("classifier called %d times, want 1 — the loop must stop, not retry per message", cl.calls)
	}
	if summary["budget_stopped"] != 1 {
		t.Errorf("budget_stopped = %d, want 1", summary["budget_stopped"])
	}
	if len(gm.modifyCalls) != 0 {
		t.Errorf("no mail should be touched after the budget is spent, got %v", gm.modifyCalls)
	}
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

func TestRunAccountNeverArchivesMailingList(t *testing.T) {
	byID := map[string]Classification{
		"m1": {Category: models.EmailArchive, Confidence: 0.99, Summary: "newsletter"},
		"m2": {Category: models.EmailArchive, Confidence: 0.99, Summary: "junk"},
	}
	tr, gm := newTriager(t, false, byID)
	gm.hasMailingList = true
	// m1 is filed under a sublabel; m2's "mailinglistings" is a lookalike.
	gm.msgs["m1"].LabelIDs = []string{"L_ML_GOLANG"}
	gm.msgs["m2"].LabelIDs = []string{"L_NOT_ML"}

	counts := map[string]int{}
	if _, err := tr.RunAccount(context.Background(), uuid.NewString(), models.AccountPersonal, gm, counts); err != nil {
		t.Fatal(err)
	}

	for _, c := range gm.modifyCalls {
		if c.msgID == "m1" && (slices.Contains(c.remove, gmail.InboxLabel) || slices.Contains(c.add, "L_ARCHIVED")) {
			t.Errorf("mailinglist message archived: add=%v remove=%v", c.add, c.remove)
		}
		if !slices.Contains(c.add, "L_TRIAGED") {
			t.Errorf("modify %s missing Art/Triaged label, add=%v", c.msgID, c.add)
		}
	}
	if counts["mailinglist_kept"] != 1 {
		t.Errorf("mailinglist_kept = %d, want 1", counts["mailinglist_kept"])
	}

	var rows []models.EmailMessage
	if err := tr.DB.Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		// Only the action is downgraded, so the audit keeps the model's verdict.
		if r.Category != models.EmailArchive {
			t.Errorf("row %s category = %q, want archive", r.GmailMessageID, r.Category)
		}
		wantArchived := r.GmailMessageID == "m2"
		if r.Archived != wantArchived {
			t.Errorf("row %s archived = %v, want %v", r.GmailMessageID, r.Archived, wantArchived)
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
