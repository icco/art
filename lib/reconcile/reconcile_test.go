package reconcile

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/testdb"
	"gorm.io/gorm"
)

type fakeCal struct {
	calls []string
	err   error
}

func (f *fakeCal) DeleteManaged(_ context.Context, account models.AccountKind, calendarID, eventID string) error {
	f.calls = append(f.calls, string(account)+" "+calendarID+" "+eventID)
	return f.err
}

// fixedNow anchors the pass; sessions sit comfortably inside the sync window.
var fixedNow = time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)

func newRunner(t *testing.T, cal CalendarService) (*Runner, *gorm.DB) {
	t.Helper()
	db := testdb.Open(t)
	// A fresh sync so the freshness guard passes.
	synced := fixedNow.Add(-time.Minute)
	if err := db.Create(&models.SyncState{AccountKind: models.AccountPersonal, CalendarID: "primary", LastSyncedAt: &synced}).Error; err != nil {
		t.Fatal(err)
	}
	return &Runner{DB: db, Cal: cal, TZ: time.UTC, Now: func() time.Time { return fixedNow }}, db
}

func seedSession(t *testing.T, db *gorm.DB, evID string, start time.Time) models.Session {
	t.Helper()
	s := models.Session{
		Source: models.SourceProject, SourceID: "00000000-0000-0000-0000-000000000001",
		AccountKind: models.AccountPersonal, CalendarID: "primary", GoogleEventID: &evID,
		ScheduledStart: start, ScheduledEnd: start.Add(time.Hour), Status: models.SessionPlanned,
	}
	if err := db.Create(&s).Error; err != nil {
		t.Fatal(err)
	}
	return s
}

func seedEvent(t *testing.T, db *gorm.DB, evID string, start time.Time, artManaged bool) {
	t.Helper()
	e := models.Event{
		AccountKind: models.AccountPersonal, CalendarID: "primary", GoogleEventID: evID,
		StartTime: start, EndTime: start.Add(time.Hour), Status: "confirmed",
		EventType: "default", IsArtManaged: artManaged,
	}
	if err := db.Create(&e).Error; err != nil {
		t.Fatal(err)
	}
}

func latestRun(t *testing.T, db *gorm.DB) models.AgentRun {
	t.Helper()
	var run models.AgentRun
	if err := db.Where("kind = ?", models.AgentRunReconcile).Order("started_at DESC").First(&run).Error; err != nil {
		t.Fatal(err)
	}
	return run
}

func TestReconcileHealsMovedSession(t *testing.T) {
	r, db := newRunner(t, &fakeCal{})
	planned := fixedNow.Add(24 * time.Hour)
	s := seedSession(t, db, "ev-move", planned)
	moved := planned.Add(3 * time.Hour)
	seedEvent(t, db, "ev-move", moved, true)

	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	var got models.Session
	if err := db.First(&got, "id = ?", s.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !got.ScheduledStart.Equal(moved) {
		t.Fatalf("scheduled_start = %v, want healed %v", got.ScheduledStart, moved)
	}
	if got.PlannedStart == nil || !got.PlannedStart.Equal(planned) {
		t.Fatalf("planned_start = %v, want original %v", got.PlannedStart, planned)
	}
	run := latestRun(t, db)
	if run.Status != models.AgentRunSucceeded {
		t.Fatalf("run status = %v", run.Status)
	}
}

func TestReconcileDeletesOrphanSession(t *testing.T) {
	r, db := newRunner(t, &fakeCal{})
	s := seedSession(t, db, "ev-gone", fixedNow.Add(24*time.Hour))
	// No matching event row -> deleted upstream.

	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Model(&models.Session{}).Where("id = ?", s.ID).Count(&n)
	if n != 0 {
		t.Fatalf("orphan session should be deleted, %d remain", n)
	}
}

func TestReconcileRetractsOnHumanConflict(t *testing.T) {
	cal := &fakeCal{}
	r, db := newRunner(t, cal)
	start := fixedNow.Add(24 * time.Hour)
	s := seedSession(t, db, "ev-art", start)
	seedEvent(t, db, "ev-art", start, true)    // the session's own art event
	seedEvent(t, db, "ev-human", start, false) // overlapping human event

	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(cal.calls) != 1 || cal.calls[0] != "personal primary ev-art" {
		t.Fatalf("DeleteManaged calls = %v, want one for ev-art", cal.calls)
	}
	var n int64
	db.Model(&models.Session{}).Where("id = ?", s.ID).Count(&n)
	if n != 0 {
		t.Fatalf("conflicting session should be retracted, %d remain", n)
	}
}

func TestReconcileIgnoresOwnEventNoConflict(t *testing.T) {
	cal := &fakeCal{}
	r, db := newRunner(t, cal)
	start := fixedNow.Add(24 * time.Hour)
	s := seedSession(t, db, "ev-art", start)
	seedEvent(t, db, "ev-art", start, true) // only its own art event overlaps

	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(cal.calls) != 0 {
		t.Fatalf("no conflict expected, got DeleteManaged %v", cal.calls)
	}
	var n int64
	db.Model(&models.Session{}).Where("id = ?", s.ID).Count(&n)
	if n != 1 {
		t.Fatalf("session should survive, %d remain", n)
	}
}

func TestReconcileSkipsWhenSyncStale(t *testing.T) {
	cal := &fakeCal{}
	db := testdb.Open(t)
	stale := fixedNow.Add(-2 * time.Hour)
	if err := db.Create(&models.SyncState{AccountKind: models.AccountPersonal, CalendarID: "primary", LastSyncedAt: &stale}).Error; err != nil {
		t.Fatal(err)
	}
	r := &Runner{DB: db, Cal: cal, TZ: time.UTC, Now: func() time.Time { return fixedNow }}
	s := seedSession(t, db, "ev-gone", fixedNow.Add(24*time.Hour)) // would be deleted if fresh

	if err := r.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Model(&models.Session{}).Where("id = ?", s.ID).Count(&n)
	if n != 1 {
		t.Fatalf("stale pass must not delete, %d remain", n)
	}
	run := latestRun(t, db)
	var got map[string]any
	if err := json.Unmarshal(run.Summary, &got); err != nil {
		t.Fatalf("summary unmarshal: %v", err)
	}
	if got["skipped_stale"] != true {
		t.Fatalf("summary = %s, want skipped_stale true", run.Summary)
	}
}
