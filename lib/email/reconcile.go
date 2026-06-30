package email

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/icco/art/lib/models"
	"gorm.io/gorm"
)

// Reversal kinds recorded on EmailMessage.ReversalKind by a manual Reverse.
const (
	reversalUnarchived     = "unarchived"
	reversalReplyDismissed = "reply_dismissed"
	reversalMiscategorized = "miscategorized"
	// reversalManualArchived records that Nat archived mail art had left in the
	// inbox — the opposite correction to reversalUnarchived.
	reversalManualArchived = "manual_archived"
)

// buildCorrections renders recently-reversed decisions into a prompt block the
// classifier appends to its system instruction, so art learns from the
// corrections Nat makes via the Reverse endpoint. Bounded to the most recent
// `limit` reversals. Art only ever reads mail in the inbox, so there is no
// autonomous reconcile pass that inspects archived mail or drafts — corrections
// come solely from Nat explicitly reversing a decision.
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
			fmt.Fprintf(&b, "- You archived an email from %q (subject %q); Nat moved it back to the inbox. Do not archive similar mail — prefer 'keep'.\n", r.FromAddr, r.Subject)
		case reversalManualArchived:
			fmt.Fprintf(&b, "- You left an email from %q (subject %q) in the inbox; Nat archived it — prefer 'archive' for similar mail.\n", r.FromAddr, r.Subject)
		case reversalReplyDismissed:
			fmt.Fprintf(&b, "- You flagged mail from %q (subject %q) as needing a reply; Nat disagreed. Be more cautious labeling similar mail 'reply'.\n", r.FromAddr, r.Subject)
		case reversalMiscategorized:
			fmt.Fprintf(&b, "- You categorized mail from %q (subject %q) as %s; Nat marked that decision wrong — reconsider similar mail.\n", r.FromAddr, r.Subject, r.Category)
		}
	}
	return b.String(), nil
}
