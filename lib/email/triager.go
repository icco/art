package email

import (
	"context"
	"fmt"
	"slices"

	"github.com/icco/art/lib/gmail"
	"github.com/icco/art/lib/models"
	gutillog "github.com/icco/gutil/logging"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Gmailer is the subset of gmail.Client the triager uses. Defining it here lets
// tests substitute a fake without touching the Google API. It deliberately
// exposes only label/archive mutations — there is no draft or send method, so
// the triager cannot write mail even by mistake.
type Gmailer interface {
	EnsureLabels(ctx context.Context) (map[string]string, error)
	FetchMessageIDs(ctx context.Context, query string, limit int) ([]string, error)
	GetMessage(ctx context.Context, id string) (*gmail.Message, error)
	ModifyLabels(ctx context.Context, msgID string, add, remove []string) error
}

// messageClassifier is the classifier behaviour the triager depends on; *Classifier
// satisfies it, and tests substitute a fake to avoid calling Gemini.
type messageClassifier interface {
	Classify(ctx context.Context, m *gmail.Message) (Classification, error)
}

// Triager classifies and acts on one account's inbox, recording an audit row
// per message.
type Triager struct {
	DB                  *gorm.DB
	Classifier          messageClassifier
	BackfillDays        int
	MaxPerRun           int
	ConfidenceThreshold float64
	DryRun              bool
}

// decision is the outcome of decideAction: which labels to add (by name) and
// whether to archive. Labeling and archiving are the only actions art takes; it
// is pure data so the policy can be unit-tested without any Gmail I/O.
type decision struct {
	Action      models.EmailAction
	AddLabels   []string
	RemoveInbox bool
}

// decideAction maps a classification to concrete labels/actions. A low-
// confidence archive is downgraded to keep (left untouched) so art never
// auto-archives mail it is unsure about. A reply is only flagged with the
// Art/Reply label for Nat to act on — art never drafts the response.
// Art/Triaged is always applied.
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
	default:
		d.Action = models.ActionKeep
	}
	return d
}

// RunAccount triages one account's inbox. It returns the number of messages
// processed and accumulates per-category counts into summary.
func (t *Triager) RunAccount(ctx context.Context, runID string, kind models.AccountKind, gm Gmailer, summary map[string]int) (int, error) {
	labels, err := gm.EnsureLabels(ctx)
	if err != nil {
		return 0, fmt.Errorf("ensure labels: %w", err)
	}

	query := fmt.Sprintf("in:inbox -label:%q newer_than:%dd", gmail.LabelTriaged, t.BackfillDays)
	ids, err := gm.FetchMessageIDs(ctx, query, t.MaxPerRun)
	if err != nil {
		return 0, fmt.Errorf("list inbox: %w", err)
	}

	log := gutillog.FromContext(ctx)
	triagedID := labels[gmail.LabelTriaged]
	processed := 0
	for _, id := range ids {
		msg, err := gm.GetMessage(ctx, id)
		if err != nil {
			// Per-message failures must not wedge the queue: one bad message
			// would otherwise abort the account and be retried every run until
			// it ages out of the backfill window.
			log.Warnw("triage: get message failed", "account", kind, "id", id, "err", err)
			summary["errors"]++
			continue
		}
		// Defensive: the label search can lag, so skip anything already tagged.
		if triagedID != "" && slices.Contains(msg.LabelIDs, triagedID) {
			continue
		}

		cls, err := t.Classifier.Classify(ctx, msg)
		if err != nil {
			// Gemini safety-blocks, rate limits, and invalid output are
			// non-fatal: leave the message untagged so a later run retries it,
			// and move on to the rest.
			log.Warnw("triage: classify failed", "account", kind, "id", id, "err", err)
			summary["errors"]++
			continue
		}
		d := decideAction(cls.Category, cls.Confidence, t.ConfidenceThreshold)

		row := models.EmailMessage{
			RunID:          runID,
			AccountKind:    kind,
			GmailMessageID: msg.ID,
			ThreadID:       msg.ThreadID,
			FromAddr:       msg.From,
			ToAddr:         msg.To,
			Subject:        msg.Subject,
			Snippet:        msg.Snippet,
			ReceivedAt:     msg.ReceivedAt,
			Category:       cls.Category,
			Summary:        cls.Summary,
			Reason:         cls.Reason,
			Confidence:     cls.Confidence,
			Action:         d.Action,
		}

		var applyErr error
		if !t.DryRun {
			applyErr = t.apply(ctx, gm, labels, msg, d, &row)
			row.Applied = applyErr == nil
		}

		// Always record the audit row, even on a partial apply failure.
		if err := t.upsert(ctx, &row); err != nil {
			return processed, fmt.Errorf("persist %s: %w", id, err)
		}
		if applyErr != nil {
			log.Warnw("triage: apply failed", "account", kind, "id", id, "err", applyErr)
			summary["errors"]++
			continue
		}
		summary[string(cls.Category)]++
		processed++
	}
	return processed, nil
}

// apply executes the decision against Gmail and records what was done on row.
// The only mutation is a label change (which, when it removes INBOX, archives
// the message); art never drafts or sends mail.
func (t *Triager) apply(ctx context.Context, gm Gmailer, labels map[string]string, msg *gmail.Message, d decision, row *models.EmailMessage) error {
	add := make([]string, 0, len(d.AddLabels))
	for _, name := range d.AddLabels {
		if id := labels[name]; id != "" {
			add = append(add, id)
		}
	}
	var remove []string
	if d.RemoveInbox {
		remove = []string{gmail.InboxLabel}
	}
	if err := gm.ModifyLabels(ctx, msg.ID, add, remove); err != nil {
		return err
	}
	row.Archived = d.RemoveInbox
	return nil
}

// upsert writes the audit row idempotently. The fetch query re-surfaces any
// message not yet labeled Art/Triaged (dry-run rows, or rows whose Modify
// failed), so a plain Create would violate the (account, message) unique index.
func (t *Triager) upsert(ctx context.Context, row *models.EmailMessage) error {
	return t.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "account_kind"}, {Name: "gmail_message_id"}},
		// Reversal state resets too: a re-triage is a fresh decision, and
		// stale reversed=true made Reverse a permanent no-op.
		DoUpdates: clause.AssignmentColumns([]string{
			"run_id", "thread_id", "from_addr", "to_addr", "subject", "snippet",
			"received_at", "category", "summary", "reason",
			"confidence", "action", "applied", "archived", "updated_at",
			"reversed", "reversal_kind", "reconciled_at",
		}),
	}).Create(row).Error
}
