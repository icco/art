package email

import (
	"context"
	"time"

	"github.com/icco/art/lib/gmail"
	"github.com/icco/art/lib/models"
	"gorm.io/gorm"
)

// setArchivedAndRecord moves a triaged message between the inbox and the archive
// by changing Gmail labels only — never writing mail — and records the result.
// The learning signal is derived from art's original Category versus the new
// state: archiving mail art wanted kept (or restoring mail art archived) is a
// correction; returning a message to the state art chose clears any correction.
// A no-op when the message is already in the requested state.
func setArchivedAndRecord(ctx context.Context, db *gorm.DB, gm reversalGmailer, row *models.EmailMessage, archived bool) (models.EmailMessage, error) {
	if row.Archived == archived {
		return *row, nil
	}

	labels, err := gm.EnsureLabels(ctx)
	if err != nil {
		return models.EmailMessage{}, err
	}
	archivedID := labels[gmail.LabelArchived]

	var add, remove []string
	action := models.ActionKeep
	if archived {
		if archivedID != "" {
			add = append(add, archivedID)
		}
		remove = append(remove, gmail.InboxLabel)
		action = models.ActionArchived
	} else {
		add = append(add, gmail.InboxLabel)
		if archivedID != "" {
			remove = append(remove, archivedID)
		}
	}
	if err := gm.ModifyLabels(ctx, row.GmailMessageID, add, remove); err != nil {
		return models.EmailMessage{}, err
	}

	reversed := archived != (row.Category == models.EmailArchive)
	kind := ""
	if reversed {
		kind = reversalUnarchived
		if archived {
			kind = reversalManualArchived
		}
	}

	now := time.Now()
	updates := map[string]any{
		"archived":      archived,
		"action":        action,
		"applied":       true,
		"reversed":      reversed,
		"reversal_kind": kind,
		"reconciled_at": &now,
	}
	if err := db.WithContext(ctx).Model(&models.EmailMessage{}).
		Where("id = ?", row.ID).
		Updates(updates).Error; err != nil {
		return models.EmailMessage{}, err
	}
	row.Archived = archived
	row.Action = action
	row.Applied = true
	row.Reversed = reversed
	row.ReversalKind = kind
	row.ReconciledAt = &now
	return *row, nil
}

// SetArchived toggles a triaged message between the inbox and the archive and
// records the change for learning. Idempotent — a message already in the
// requested state is returned without touching Gmail. Returns
// gorm.ErrRecordNotFound when emailID is unknown.
func (r *Runner) SetArchived(ctx context.Context, emailID string, archived bool) (models.EmailMessage, error) {
	var row models.EmailMessage
	if err := r.DB.WithContext(ctx).First(&row, "id = ?", emailID).Error; err != nil {
		return models.EmailMessage{}, err
	}
	if row.Archived == archived {
		return row, nil
	}
	gm, err := gmail.NewClient(ctx, r.OAuth, row.AccountKind)
	if err != nil {
		return models.EmailMessage{}, err
	}
	return setArchivedAndRecord(ctx, r.DB, gm, &row, archived)
}
