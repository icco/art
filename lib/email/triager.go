package email

import (
	"context"
	"fmt"
	"slices"

	"github.com/icco/art/lib/gmail"
	"github.com/icco/art/lib/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Gmailer is the subset of gmail.Client the triager uses. Defining it here lets
// tests substitute a fake without touching the Google API.
type Gmailer interface {
	EnsureLabels(ctx context.Context) (map[string]string, error)
	FetchMessageIDs(ctx context.Context, query string, max int) ([]string, error)
	GetMessage(ctx context.Context, id string) (*gmail.Message, error)
	ModifyLabels(ctx context.Context, msgID string, add, remove []string) error
	CreateDraft(ctx context.Context, in gmail.DraftInput) (string, error)
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

// decision is the outcome of decideAction: which labels to add (by name),
// whether to archive, and whether to draft a reply. It is pure data so the
// policy can be unit-tested without any Gmail I/O.
type decision struct {
	Action      models.EmailAction
	AddLabels   []string
	RemoveInbox bool
	MakeDraft   bool
}

// decideAction maps a classification to concrete labels/actions. A low-
// confidence archive is downgraded to read (label only) so art never
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
			d.Action = models.ActionRead
			d.AddLabels = append(d.AddLabels, gmail.LabelRead)
		}
	case models.EmailReply:
		d.Action = models.ActionReply
		d.AddLabels = append(d.AddLabels, gmail.LabelReply)
		d.MakeDraft = true
	case models.EmailRead:
		d.Action = models.ActionRead
		d.AddLabels = append(d.AddLabels, gmail.LabelRead)
	case models.EmailThinking:
		d.Action = models.ActionThinking
		d.AddLabels = append(d.AddLabels, gmail.LabelThinking)
	case models.EmailKeep:
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

	triagedID := labels[gmail.LabelTriaged]
	processed := 0
	for _, id := range ids {
		msg, err := gm.GetMessage(ctx, id)
		if err != nil {
			return processed, fmt.Errorf("get message %s: %w", id, err)
		}
		// Defensive: the label search can lag, so skip anything already tagged.
		if triagedID != "" && slices.Contains(msg.LabelIDs, triagedID) {
			continue
		}

		cls, err := t.Classifier.Classify(ctx, msg)
		if err != nil {
			return processed, fmt.Errorf("classify %s: %w", id, err)
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
			DraftReply:     cls.DraftReply,
			Reason:         cls.Reason,
			Confidence:     cls.Confidence,
			Action:         d.Action,
		}

		if !t.DryRun {
			if err := t.apply(ctx, gm, labels, msg, d, &row); err != nil {
				return processed, fmt.Errorf("apply %s: %w", id, err)
			}
			row.Applied = true
		}

		if err := t.upsert(ctx, &row); err != nil {
			return processed, fmt.Errorf("persist %s: %w", id, err)
		}
		summary[string(cls.Category)]++
		processed++
	}
	return processed, nil
}

// apply executes the decision against Gmail and records what was done on row.
func (t *Triager) apply(ctx context.Context, gm Gmailer, labels map[string]string, msg *gmail.Message, d decision, row *models.EmailMessage) error {
	if d.MakeDraft {
		draftID, err := gm.CreateDraft(ctx, gmail.DraftInput{
			ThreadID:  msg.ThreadID,
			To:        msg.From,
			Subject:   msg.Subject,
			Body:      row.DraftReply,
			InReplyTo: msg.MessageIDHeader,
		})
		if err != nil {
			return err
		}
		row.DraftID = draftID
	}

	add := make([]string, 0, len(d.AddLabels))
	for _, name := range d.AddLabels {
		if id := labels[name]; id != "" {
			add = append(add, id)
		}
	}
	var remove []string
	if d.RemoveInbox {
		remove = []string{gmail.InboxLabel}
		row.Archived = true
	}
	return gm.ModifyLabels(ctx, msg.ID, add, remove)
}

// upsert writes the audit row idempotently. The fetch query re-surfaces any
// message not yet labeled Art/Triaged (dry-run rows, or rows whose Modify
// failed), so a plain Create would violate the (account, message) unique index.
func (t *Triager) upsert(ctx context.Context, row *models.EmailMessage) error {
	return t.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "account_kind"}, {Name: "gmail_message_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"run_id", "thread_id", "from_addr", "to_addr", "subject", "snippet",
			"received_at", "category", "summary", "draft_reply", "reason",
			"confidence", "action", "applied", "draft_id", "archived", "updated_at",
		}),
	}).Create(row).Error
}
