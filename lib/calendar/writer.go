package calendar

import (
	"context"
	"fmt"
	"time"

	"github.com/icco/art/lib/models"
	"google.golang.org/api/calendar/v3"
)

// FocusBlock describes an Art-created focus block to write to Google Calendar.
type FocusBlock struct {
	CalendarID  string
	Start       time.Time
	End         time.Time
	Summary     string
	Description string
	Source      models.SourceKind
	SourceID    string
}

// CreateFocus writes a focusTime event with the art_managed extended property,
// then returns the Google event so the caller can record the event ID.
func (c *Client) CreateFocus(ctx context.Context, fb FocusBlock) (*calendar.Event, error) {
	if !fb.Source.Valid() {
		return nil, fmt.Errorf("calendar: invalid source kind %q", fb.Source)
	}
	if !fb.End.After(fb.Start) {
		return nil, fmt.Errorf("calendar: end must be after start")
	}

	ev := &calendar.Event{
		Summary:     fb.Summary,
		Description: fb.Description,
		EventType:   "focusTime",
		Start:       &calendar.EventDateTime{DateTime: fb.Start.UTC().Format(time.RFC3339)},
		End:         &calendar.EventDateTime{DateTime: fb.End.UTC().Format(time.RFC3339)},
		ExtendedProperties: &calendar.EventExtendedProperties{
			Private: map[string]string{
				ArtManagedKey:    "true",
				"art_source":     string(fb.Source),
				"art_source_id":  fb.SourceID,
				"art_created_at": time.Now().UTC().Format(time.RFC3339),
			},
		},
		FocusTimeProperties: &calendar.EventFocusTimeProperties{
			AutoDeclineMode: "declineNone",
		},
	}
	return c.Service.Events.Insert(fb.CalendarID, ev).Context(ctx).Do()
}

// DeleteManaged refuses to delete events lacking the art_managed=true property.
func (c *Client) DeleteManaged(ctx context.Context, calendarID, eventID string) error {
	ev, err := c.Service.Events.Get(calendarID, eventID).Context(ctx).Do()
	if err != nil {
		return err
	}
	if ev.ExtendedProperties == nil ||
		ev.ExtendedProperties.Private == nil ||
		ev.ExtendedProperties.Private[ArtManagedKey] != "true" {
		return fmt.Errorf("calendar: refusing to delete non-Art event %q", eventID)
	}
	return c.Service.Events.Delete(calendarID, eventID).Context(ctx).Do()
}
