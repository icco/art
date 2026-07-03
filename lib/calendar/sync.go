package calendar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/icco/art/lib/models"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/googleapi"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// HistoryWindow is how far back a full sync walks; FutureWindow is the
// matching forward bound for upcoming events.
const (
	HistoryWindow = 365 * 24 * time.Hour
	FutureWindow  = 60 * 24 * time.Hour
)

// Syncer pulls events from a single calendar account into the database.
// TZ anchors all-day event dates; nil means UTC.
type Syncer struct {
	Client *Client
	DB     *gorm.DB
	TZ     *time.Location
}

// Run performs an incremental sync, falling back to a bounded full sync.
// A failing calendar doesn't block the rest; errors are joined.
func (s *Syncer) Run(ctx context.Context) error {
	var errs []error
	pageToken := ""
	for {
		call := s.Client.Service.CalendarList.List().Context(ctx)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		list, err := call.Do()
		if err != nil {
			return fmt.Errorf("calendarList: %w", err)
		}
		for _, item := range list.Items {
			if err := s.syncCalendar(ctx, item.Id); err != nil {
				errs = append(errs, fmt.Errorf("sync calendar %q: %w", item.Id, err))
			}
		}
		if list.NextPageToken == "" {
			return errors.Join(errs...)
		}
		pageToken = list.NextPageToken
	}
}

func (s *Syncer) syncCalendar(ctx context.Context, calendarID string) error {
	state, fullSync, err := s.loadSyncState(ctx, calendarID)
	if err != nil {
		return err
	}

	pageToken := ""
	now := time.Now().UTC()
	syncStart := time.Now()
	for {
		call := s.Client.Service.Events.List(calendarID).
			Context(ctx).
			ShowDeleted(true).
			SingleEvents(true).
			MaxResults(2500)
		if fullSync {
			call = call.
				TimeMin(now.Add(-HistoryWindow).Format(time.RFC3339)).
				TimeMax(now.Add(FutureWindow).Format(time.RFC3339))
		} else {
			call = call.SyncToken(*state.LastSyncToken)
		}
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}

		resp, err := call.Do()
		if err != nil {
			var gerr *googleapi.Error
			if errors.As(err, &gerr) && gerr.Code == 410 {
				// 410 = sync token expired; drop state and full-resync.
				if delErr := s.DB.WithContext(ctx).Where(
					"account_kind = ? AND calendar_id = ?",
					string(s.Client.Account.Kind), calendarID,
				).Delete(&models.SyncState{}).Error; delErr != nil {
					return delErr
				}
				return s.syncCalendar(ctx, calendarID)
			}
			return err
		}

		for _, ev := range resp.Items {
			if err := s.upsertEvent(ctx, calendarID, ev); err != nil {
				return err
			}
		}

		if resp.NextPageToken != "" {
			pageToken = resp.NextPageToken
			continue
		}
		if resp.NextSyncToken != "" {
			tok := resp.NextSyncToken
			t := time.Now()
			next := models.SyncState{
				AccountKind:   s.Client.Account.Kind,
				CalendarID:    calendarID,
				LastSyncToken: &tok,
				LastSyncedAt:  &t,
			}
			if err := s.DB.WithContext(ctx).
				Clauses(clause.OnConflict{
					Columns: []clause.Column{{Name: "account_kind"}, {Name: "calendar_id"}},
					DoUpdates: clause.AssignmentColumns([]string{
						"last_sync_token", "last_synced_at",
					}),
				}).
				Create(&next).Error; err != nil {
				return err
			}
		}
		if fullSync {
			// Rows untouched by the full walk no longer exist upstream.
			if err := s.DB.WithContext(ctx).Where(
				"account_kind = ? AND calendar_id = ? AND updated_at < ?",
				string(s.Client.Account.Kind), calendarID, syncStart,
			).Delete(&models.Event{}).Error; err != nil {
				return err
			}
		}
		return nil
	}
}

func (s *Syncer) loadSyncState(ctx context.Context, calendarID string) (models.SyncState, bool, error) {
	var st models.SyncState
	err := s.DB.WithContext(ctx).
		Where("account_kind = ? AND calendar_id = ?", string(s.Client.Account.Kind), calendarID).
		First(&st).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return st, true, nil
	}
	if err != nil {
		return st, false, err
	}
	if st.LastSyncToken == nil || *st.LastSyncToken == "" {
		return st, true, nil
	}
	return st, false, nil
}

func (s *Syncer) upsertEvent(ctx context.Context, calendarID string, ev *calendar.Event) error {
	if ev.Status == "cancelled" {
		return s.DB.WithContext(ctx).Where(
			"account_kind = ? AND calendar_id = ? AND google_event_id = ?",
			string(s.Client.Account.Kind), calendarID, ev.Id,
		).Delete(&models.Event{}).Error
	}

	start, end, allDay := eventTimes(ev, s.TZ)
	if start.IsZero() || end.IsZero() {
		return nil // unparseable; skip
	}

	props := map[string]any{}
	if ev.ExtendedProperties != nil {
		if len(ev.ExtendedProperties.Private) > 0 {
			props["private"] = ev.ExtendedProperties.Private
		}
		if len(ev.ExtendedProperties.Shared) > 0 {
			props["shared"] = ev.ExtendedProperties.Shared
		}
	}
	extJSON, err := json.Marshal(props)
	if err != nil {
		return err
	}

	artManaged := ev.ExtendedProperties != nil &&
		ev.ExtendedProperties.Private != nil &&
		ev.ExtendedProperties.Private[ArtManagedKey] == ArtManagedTrue

	eventType := ev.EventType
	if eventType == "" {
		eventType = "default"
	}

	row := models.Event{
		AccountKind:        s.Client.Account.Kind,
		CalendarID:         calendarID,
		GoogleEventID:      ev.Id,
		Summary:            ev.Summary,
		Description:        ev.Description,
		StartTime:          start,
		EndTime:            end,
		AllDay:             allDay,
		AttendeeCount:      len(ev.Attendees),
		EventType:          eventType,
		IsArtManaged:       artManaged,
		Status:             ev.Status,
		ExtendedProperties: datatypes.JSON(extJSON),
	}
	return s.DB.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "account_kind"}, {Name: "calendar_id"}, {Name: "google_event_id"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"summary", "description", "start_time", "end_time", "all_day",
				"attendee_count", "event_type", "is_art_managed", "status",
				"extended_properties", "updated_at",
			}),
		}).
		Create(&row).Error
}

func eventTimes(ev *calendar.Event, tz *time.Location) (start, end time.Time, allDay bool) {
	if tz == nil {
		tz = time.UTC
	}
	parse := func(s string) (time.Time, bool) {
		if s == "" {
			return time.Time{}, false
		}
		if t, err := time.ParseInLocation("2006-01-02", s, tz); err == nil {
			return t, true
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t, false
		}
		return time.Time{}, false
	}
	if ev.Start != nil {
		if t, ad := parse(firstNonEmpty(ev.Start.DateTime, ev.Start.Date)); !t.IsZero() {
			start = t
			allDay = ad
		}
	}
	if ev.End != nil {
		if t, _ := parse(firstNonEmpty(ev.End.DateTime, ev.End.Date)); !t.IsZero() {
			end = t
		}
	}
	return
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
