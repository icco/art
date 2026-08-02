package calendar

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/testdb"
	calapi "google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

func newFakeService(t *testing.T, handler http.Handler) *calapi.Service {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	svc, err := calapi.NewService(context.Background(),
		option.WithEndpoint(srv.URL), option.WithoutAuthentication())
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("encode fake response: %v", err)
	}
}

func eventsJSON(items []*calapi.Event) *calapi.Events {
	return &calapi.Events{Items: items, NextSyncToken: "tok"}
}

func timedEvent(id string, start time.Time) *calapi.Event {
	return &calapi.Event{
		Id:     id,
		Status: "confirmed",
		Start:  &calapi.EventDateTime{DateTime: start.Format(time.RFC3339)},
		End:    &calapi.EventDateTime{DateTime: start.Add(time.Hour).Format(time.RFC3339)},
	}
}

// A failing primary calendar surfaces an error naming it.
func TestRunReportsFailingPrimary(t *testing.T) {
	db := testdb.Open(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/calendars/bad/events", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error": {"code": 403}}`, http.StatusForbidden)
	})
	s := &Syncer{Client: &Client{Account: models.Account{Kind: models.AccountWork, PrimaryCalendarID: "bad"}, Service: newFakeService(t, mux)}, DB: db}

	err := s.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "bad") {
		t.Fatalf("want error naming the failing calendar, got %v", err)
	}
}

// Only the primary is mirrored. Subscribed calendars (a coworker PTO import, a
// partner's calendar) are not the owner's busy time, so they must not be read.
func TestRunSyncsOnlyPrimaryCalendar(t *testing.T) {
	db := testdb.Open(t)
	touched := map[string]bool{}
	mux := http.NewServeMux()
	mux.HandleFunc("/users/me/calendarList", func(w http.ResponseWriter, _ *http.Request) {
		touched["calendarList"] = true
		writeJSON(t, w, &calapi.CalendarList{Items: []*calapi.CalendarListEntry{{Id: "mine"}, {Id: "someone-else"}}})
	})
	for _, id := range []string{"mine", "someone-else"} {
		mux.HandleFunc("/calendars/"+id+"/events", func(w http.ResponseWriter, _ *http.Request) {
			touched[id] = true
			writeJSON(t, w, eventsJSON([]*calapi.Event{timedEvent("ev-"+id, time.Now())}))
		})
	}
	s := &Syncer{Client: &Client{Account: models.Account{Kind: models.AccountWork, PrimaryCalendarID: "mine"}, Service: newFakeService(t, mux)}, DB: db}

	if err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !touched["mine"] {
		t.Error("primary calendar was not synced")
	}
	if touched["someone-else"] {
		t.Error("a subscribed calendar was synced; only the primary should be")
	}
	if touched["calendarList"] {
		t.Error("calendarList should not be enumerated at all")
	}
	var ids []string
	db.Model(&models.Event{}).Pluck("google_event_id", &ids)
	if len(ids) != 1 || ids[0] != "ev-mine" {
		t.Fatalf("only primary events should be mirrored, got %v", ids)
	}
}

// PruneForeignCalendars clears rows left behind by a calendar no longer synced.
func TestPruneForeignCalendars(t *testing.T) {
	db := testdb.Open(t)
	if err := db.Create(&models.Account{
		Kind: models.AccountPersonal, Email: "me@example.com",
		RefreshTokenEncrypted: []byte("x"), PrimaryCalendarID: "mine",
	}).Error; err != nil {
		t.Fatal(err)
	}
	for _, cal := range []string{"mine", "coworker-pto"} {
		if err := db.Create(&models.Event{
			AccountKind: models.AccountPersonal, CalendarID: cal, GoogleEventID: "ev-" + cal,
			StartTime: time.Now(), EndTime: time.Now().Add(time.Hour), Status: "confirmed",
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	if err := PruneForeignCalendars(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var ids []string
	db.Model(&models.Event{}).Pluck("google_event_id", &ids)
	if len(ids) != 1 || ids[0] != "ev-mine" {
		t.Fatalf("foreign calendar rows should be pruned, got %v", ids)
	}
}

// A full resync must prune local rows Google no longer returns.
func TestFullResyncPrunesStaleRows(t *testing.T) {
	db := testdb.Open(t)
	ghost := models.Event{
		AccountKind: models.AccountWork, CalendarID: "c1", GoogleEventID: "ghost",
		StartTime: time.Now(), EndTime: time.Now().Add(time.Hour), Status: "confirmed",
	}
	if err := db.Create(&ghost).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&ghost).Update("updated_at", time.Now().Add(-time.Hour)).Error; err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/calendars/c1/events", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, eventsJSON([]*calapi.Event{timedEvent("real", time.Now())}))
	})
	s := &Syncer{Client: &Client{Account: models.Account{Kind: models.AccountWork, PrimaryCalendarID: "c1"}, Service: newFakeService(t, mux)}, DB: db}

	if err := s.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	var ids []string
	db.Model(&models.Event{}).Order("google_event_id").Pluck("google_event_id", &ids)
	if len(ids) != 1 || ids[0] != "real" {
		t.Fatalf("full resync should prune ghost rows, got %v", ids)
	}
}

// Retrying a commit whose insert already landed must not duplicate the event.
func TestCreateFocusIdempotent(t *testing.T) {
	existing := &calapi.Event{Id: "abc123", Summary: "Focus: X", Status: "confirmed"}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /calendars/c1/events", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error": {"code": 409, "message": "duplicate"}}`, http.StatusConflict)
	})
	mux.HandleFunc("GET /calendars/c1/events/abc123", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, existing)
	})
	c := &Client{Account: models.Account{Kind: models.AccountWork}, Service: newFakeService(t, mux)}

	ev, err := c.CreateFocus(context.Background(), FocusBlock{
		CalendarID: "c1", EventID: "abc123",
		Start: time.Now(), End: time.Now().Add(time.Hour),
		Summary: "Focus: X", Source: models.SourceProject, SourceID: "p1",
	})
	if err != nil {
		t.Fatalf("409 on an art-managed id should resolve to the existing event: %v", err)
	}
	if ev.Id != "abc123" {
		t.Fatalf("got event %q, want existing abc123", ev.Id)
	}
}

// A 409 whose existing event is a cancelled tombstone must be revived via
// update, not returned dead: Google keeps deleted event IDs forever, so
// returning the tombstone books a block that never appears on the calendar.
func TestCreateFocusRevivesCancelledTombstone(t *testing.T) {
	var updated *calapi.Event
	mux := http.NewServeMux()
	mux.HandleFunc("POST /calendars/c1/events", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error": {"code": 409, "message": "duplicate"}}`, http.StatusConflict)
	})
	mux.HandleFunc("GET /calendars/c1/events/abc123", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(t, w, &calapi.Event{Id: "abc123", Status: "cancelled"})
	})
	mux.HandleFunc("PUT /calendars/c1/events/abc123", func(w http.ResponseWriter, r *http.Request) {
		var ev calapi.Event
		if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
			t.Errorf("decode update body: %v", err)
		}
		updated = &ev
		ev.Id = "abc123"
		writeJSON(t, w, &ev)
	})
	c := &Client{Account: models.Account{Kind: models.AccountPersonal}, Service: newFakeService(t, mux)}

	ev, err := c.CreateFocus(context.Background(), FocusBlock{
		CalendarID: "c1", EventID: "abc123",
		Start: time.Now(), End: time.Now().Add(time.Hour),
		Summary: "Focus: X", Source: models.SourceProject, SourceID: "p1",
	})
	if err != nil {
		t.Fatalf("CreateFocus: %v", err)
	}
	if updated == nil {
		t.Fatal("cancelled tombstone was not revived via events.update")
	}
	if updated.Status != "confirmed" {
		t.Fatalf("update status = %q, want confirmed", updated.Status)
	}
	if updated.ExtendedProperties == nil || updated.ExtendedProperties.Private[ArtManagedKey] != ArtManagedTrue {
		t.Fatal("revived event lost the art_managed marker")
	}
	if ev.Status != "confirmed" {
		t.Fatalf("returned status = %q, want confirmed", ev.Status)
	}
}

// All-day dates must land on midnight in the configured timezone, not UTC.
func TestEventTimesAllDayInTZ(t *testing.T) {
	tz, _ := time.LoadLocation("America/Los_Angeles")
	ev := &calapi.Event{
		Start: &calapi.EventDateTime{Date: "2026-05-25"},
		End:   &calapi.EventDateTime{Date: "2026-05-26"},
	}
	s, e, allDay := eventTimes(ev, tz)
	if !allDay {
		t.Fatal("expected all-day")
	}
	if want := time.Date(2026, 5, 25, 0, 0, 0, 0, tz); !s.Equal(want) {
		t.Fatalf("start = %v, want %v", s, want)
	}
	if want := time.Date(2026, 5, 26, 0, 0, 0, 0, tz); !e.Equal(want) {
		t.Fatalf("end = %v, want %v", e, want)
	}
}
