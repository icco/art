package calendar

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/icco/art/lib/models"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/googleapi"
)

// FocusBlock describes an art-managed focus event to write to Google Calendar.
type FocusBlock struct {
	CalendarID  string
	EventID     string // optional caller-supplied ID for idempotent inserts
	Start       time.Time
	End         time.Time
	Summary     string
	Description string
	Source      models.SourceKind
	SourceID    string
}

// CreateFocus inserts an art-managed event for the given FocusBlock.
func (c *Client) CreateFocus(ctx context.Context, fb FocusBlock) (*calendar.Event, error) {
	if !fb.Source.Valid() {
		return nil, fmt.Errorf("calendar: invalid source kind %q", fb.Source)
	}
	if !fb.End.After(fb.Start) {
		return nil, fmt.Errorf("calendar: end must be after start")
	}

	ev := &calendar.Event{
		Id:          fb.EventID,
		Summary:     fb.Summary,
		Description: fb.Description,
		EventType:   "focusTime",
		Start:       &calendar.EventDateTime{DateTime: fb.Start.UTC().Format(time.RFC3339)},
		End:         &calendar.EventDateTime{DateTime: fb.End.UTC().Format(time.RFC3339)},
		ExtendedProperties: &calendar.EventExtendedProperties{
			Private: map[string]string{
				ArtManagedKey:    ArtManagedTrue,
				"art_source":     string(fb.Source),
				"art_source_id":  fb.SourceID,
				"art_created_at": time.Now().UTC().Format(time.RFC3339),
			},
		},
		FocusTimeProperties: &calendar.EventFocusTimeProperties{
			AutoDeclineMode: "declineNone",
		},
	}
	created, err := c.Service.Events.Insert(fb.CalendarID, ev).Context(ctx).Do()
	if err != nil {
		// 409 on a caller-supplied ID means a previous attempt already
		// landed; resolve to the existing event so retries are idempotent.
		var gerr *googleapi.Error
		if fb.EventID != "" && errors.As(err, &gerr) && gerr.Code == http.StatusConflict {
			return c.Service.Events.Get(fb.CalendarID, fb.EventID).Context(ctx).Do()
		}
		return nil, err
	}
	return created, nil
}

// DeleteManaged refuses to delete events not tagged art_managed=true.
// Safety invariant: Art never touches human-created events.
func (c *Client) DeleteManaged(ctx context.Context, calendarID, eventID string) error {
	ev, err := c.Service.Events.Get(calendarID, eventID).Context(ctx).Do()
	if err != nil {
		return err
	}
	if ev.ExtendedProperties == nil ||
		ev.ExtendedProperties.Private == nil ||
		ev.ExtendedProperties.Private[ArtManagedKey] != ArtManagedTrue {
		return fmt.Errorf("calendar: refusing to delete non-Art event %q", eventID)
	}
	return c.Service.Events.Delete(calendarID, eventID).Context(ctx).Do()
}
