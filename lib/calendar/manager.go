package calendar

import (
	"context"
	"errors"
	"net/http"

	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/oauth"
	"google.golang.org/api/googleapi"
)

// Manager builds per-account clients on demand for one-off managed-event
// operations that aren't tied to a running sync.
type Manager struct {
	OAuth *oauth.Flow
}

// DeleteManaged removes an Art-managed event from the account's calendar. An
// already-deleted event (Google 404) is treated as success so the operation
// is idempotent under retries and races.
func (m *Manager) DeleteManaged(ctx context.Context, account models.AccountKind, calendarID, eventID string) error {
	cl, err := NewClient(ctx, m.OAuth, account)
	if err != nil {
		return err
	}
	err = cl.DeleteManaged(ctx, calendarID, eventID)
	var gerr *googleapi.Error
	if errors.As(err, &gerr) && gerr.Code == http.StatusNotFound {
		return nil
	}
	return err
}
