package calendar

import (
	"context"
	"errors"

	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/oauth"
	"gorm.io/gorm"
)

// Runner runs Sync.Run across every persisted account. Implements
// handlers.SyncService.
type Runner struct {
	DB    *gorm.DB
	OAuth *oauth.Flow
}

// RunAll iterates known account kinds; missing/unlinked accounts are skipped
// silently. Per-account errors are returned in the map; a non-nil error means
// something failed before we got to that point.
func (r *Runner) RunAll(ctx context.Context) (map[string]string, error) {
	results := map[string]string{}
	for _, kind := range []models.AccountKind{models.AccountPersonal, models.AccountWork} {
		client, err := NewClient(ctx, r.OAuth, kind)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			results[string(kind)] = err.Error()
			continue
		}
		syncer := &Syncer{Client: client, DB: r.DB}
		if err := syncer.Run(ctx); err != nil {
			results[string(kind)] = err.Error()
		}
	}
	return results, nil
}
