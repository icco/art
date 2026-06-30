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
