package email

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/icco/art/lib/models"
	gutillog "github.com/icco/gutil/logging"
	"gorm.io/gorm"
)

// Reversal kinds recorded on EmailMessage.ReversalKind.
const (
	reversalUnarchived     = "unarchived"
	reversalDraftDeleted   = "draft_deleted"
	reversalMiscategorized = "miscategorized"
)

// reconcileGmailer is the subset of gmail.Client the reconcile pass needs.
type reconcileGmailer interface {
	HasInboxLabel(ctx context.Context, msgID string) (bool, error)
	GetDraft(ctx context.Context, draftID string) (bool, error)
}

// Reconcile detects actions Nat reversed since a prior run — mail he
// un-archived, or draft replies he discarded — and records them so they can
// feed the classifier prompt as corrections.
//
// To keep hourly cost bounded it checks at most `cap` rows per call, oldest-
// reconciled first, and stamps ReconciledAt on every row it checks so
// successive runs round-robin across the window.
func Reconcile(ctx context.Context, db *gorm.DB, kind models.AccountKind, gm reconcileGmailer, withinDays, maxRows int) (int, error) {
	cutoff := time.Now().AddDate(0, 0, -withinDays)
	var rows []models.EmailMessage
	if err := db.WithContext(ctx).
		Where("account_kind = ? AND applied AND NOT reversed AND action IN ? AND created_at >= ?",
			kind, []string{string(models.ActionArchived), string(models.ActionReply)}, cutoff).
		Order("reconciled_at ASC NULLS FIRST").
		Limit(maxRows).
		Find(&rows).Error; err != nil {
		return 0, err
	}

	log := gutillog.FromContext(ctx)
	now := time.Now()
	errCount := 0
	for i := range rows {
		row := &rows[i]
		reversalKind, err := detectReversal(ctx, gm, row)
		if err != nil {
			// Transient Gmail error: log it, count it, and leave the row for a
			// later run rather than wedging the pass.
			log.Warnw("reconcile: detect reversal failed", "account", kind, "id", row.GmailMessageID, "err", err)
			errCount++
			continue
		}
		updates := map[string]any{"reconciled_at": &now}
		if reversalKind != "" {
			updates["reversed"] = true
			updates["reversal_kind"] = reversalKind
		}
		if err := db.WithContext(ctx).Model(&models.EmailMessage{}).
			Where("id = ?", row.ID).Updates(updates).Error; err != nil {
			return errCount, err
		}
	}
	return errCount, nil
}

func detectReversal(ctx context.Context, gm reconcileGmailer, row *models.EmailMessage) (string, error) {
	switch row.Action {
	case models.ActionArchived:
		inInbox, err := gm.HasInboxLabel(ctx, row.GmailMessageID)
		if err != nil {
			return "", err
		}
		if inInbox {
			return reversalUnarchived, nil
		}
	case models.ActionReply:
		if row.DraftID == "" {
			return "", nil
		}
		exists, err := gm.GetDraft(ctx, row.DraftID)
		if err != nil {
			return "", err
		}
		if !exists {
			return reversalDraftDeleted, nil
		}
	}
	return "", nil
}

// buildCorrections renders recently-detected reversals into a prompt block the
// classifier appends to its system instruction. Bounded to the most recent
// `max` reversals.
func buildCorrections(ctx context.Context, db *gorm.DB, withinDays, limit int) (string, error) {
	cutoff := time.Now().AddDate(0, 0, -withinDays)
	var rows []models.EmailMessage
	if err := db.WithContext(ctx).
		Where("reversed AND reconciled_at >= ?", cutoff).
		Order("reconciled_at DESC").
		Limit(limit).
		Find(&rows).Error; err != nil {
		return "", err
	}
	if len(rows) == 0 {
		return "", nil
	}

	var b strings.Builder
	b.WriteString("\n\nRecent corrections from Nat — learn from these and do not repeat the mistake:\n")
	for _, r := range rows {
		switch r.ReversalKind {
		case reversalUnarchived:
			fmt.Fprintf(&b, "- You archived an email from %q (subject %q); Nat moved it back to the inbox. Do not archive similar mail — prefer 'read' or 'keep'.\n", r.FromAddr, r.Subject)
		case reversalDraftDeleted:
			fmt.Fprintf(&b, "- You drafted a reply to %q (subject %q); Nat discarded it without sending. Be more cautious drafting replies to similar mail.\n", r.FromAddr, r.Subject)
		case reversalMiscategorized:
			fmt.Fprintf(&b, "- You categorized mail from %q (subject %q) as %s; Nat marked that decision wrong — reconsider similar mail.\n", r.FromAddr, r.Subject, r.Category)
		}
	}
	return b.String(), nil
}
