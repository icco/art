package agent

import (
	"context"
	"testing"
	"time"

	"github.com/icco/art/lib/models"
)

func TestClassifySession(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	future := now.Add(2 * time.Hour)
	past := now.Add(-3 * time.Hour)

	sess := func(start time.Time) models.Session {
		return models.Session{ScheduledStart: start, ScheduledEnd: start.Add(time.Hour), Status: models.SessionPlanned}
	}
	evAt := func(start time.Time) *models.Event {
		return &models.Event{StartTime: start, EndTime: start.Add(time.Hour)}
	}

	cases := []struct {
		name     string
		sess     models.Session
		ev       *models.Event
		conflict bool
		synced   bool
		want     reconcileAction
	}{
		{"intact future block", sess(future), evAt(future), false, true, reconcileNone},
		{"deleted after sync", sess(future), nil, false, true, reconcileSkipDeleted},
		{"missing but never synced", sess(future), nil, false, false, reconcileNone},
		{"moved upstream", sess(future), evAt(future.Add(time.Hour)), false, true, reconcileMove},
		{"conflict on future block", sess(future), evAt(future), true, true, reconcileSkipConflict},
		{"conflict on past block is left alone", sess(past), evAt(past), true, true, reconcileHappened},
		{"block ended intact", sess(past), evAt(past), false, true, reconcileHappened},
		{"in-progress block untouched", sess(now.Add(-30 * time.Minute)), evAt(now.Add(-30 * time.Minute)), false, true, reconcileNone},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifySession(tc.sess, tc.ev, tc.conflict, tc.synced, now); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func seedReconcileSession(t *testing.T, p *Planner, taskID string, start time.Time, withEvent bool) models.Session {
	t.Helper()
	evID := "ev-" + taskID
	sess := models.Session{
		Source: models.SourceTask, SourceID: taskID,
		AccountKind: models.AccountPersonal, CalendarID: "primary",
		GoogleEventID:  &evID,
		ScheduledStart: start, ScheduledEnd: start.Add(2 * time.Hour),
		Status: models.SessionPlanned,
	}
	if err := p.DB.Create(&sess).Error; err != nil {
		t.Fatal(err)
	}
	if withEvent {
		if err := p.DB.Create(&models.Event{
			AccountKind: models.AccountPersonal, CalendarID: "primary", GoogleEventID: evID,
			StartTime: start, EndTime: start.Add(2 * time.Hour),
			IsArtManaged: true, Status: "confirmed", EventType: "focusTime",
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Mark the calendar as synced after the session was created.
	syncedAt := time.Now().Add(time.Minute)
	if err := p.DB.Create(&models.SyncState{
		AccountKind: models.AccountPersonal, CalendarID: "primary", LastSyncedAt: &syncedAt,
	}).Error; err != nil {
		t.Fatal(err)
	}
	return sess
}

func TestReconcileDeletedEventReopensTask(t *testing.T) {
	p, _ := deterministicPlanner(t)
	task := models.Task{Title: "pack", Kind: models.SlotPersonal, DurationMinutes: 120, Status: models.TaskScheduled}
	if err := p.DB.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	sess := seedReconcileSession(t, p, task.ID, time.Now().Add(24*time.Hour), false)

	sum, err := p.reconcileWith(context.Background(), func(context.Context, models.AccountKind, string, string) error {
		t.Fatal("nothing to delete for an already-deleted event")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if sum.SkippedDeleted != 1 {
		t.Fatalf("summary: %+v", sum)
	}
	var gotSess models.Session
	if err := p.DB.First(&gotSess, "id = ?", sess.ID).Error; err != nil {
		t.Fatal(err)
	}
	if gotSess.Status != models.SessionSkipped {
		t.Fatalf("session status: %q, want skipped", gotSess.Status)
	}
	var gotTask models.Task
	if err := p.DB.First(&gotTask, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if gotTask.Status != models.TaskPending {
		t.Fatalf("task status: %q, want pending (coverage lost)", gotTask.Status)
	}
}

func TestReconcileMovedEventUpdatesSession(t *testing.T) {
	p, _ := deterministicPlanner(t)
	task := models.Task{Title: "pack", Kind: models.SlotPersonal, DurationMinutes: 120, Status: models.TaskScheduled}
	if err := p.DB.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(24 * time.Hour).Truncate(time.Hour)
	sess := seedReconcileSession(t, p, task.ID, start, true)

	newStart := start.Add(3 * time.Hour)
	if err := p.DB.Model(&models.Event{}).
		Where("google_event_id = ?", *sess.GoogleEventID).
		Updates(map[string]any{"start_time": newStart, "end_time": newStart.Add(2 * time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}

	sum, err := p.reconcileWith(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Moved != 1 {
		t.Fatalf("summary: %+v", sum)
	}
	var got models.Session
	if err := p.DB.First(&got, "id = ?", sess.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != models.SessionMoved || !got.ScheduledStart.Equal(newStart) {
		t.Fatalf("session after move: status=%q start=%v want moved/%v", got.Status, got.ScheduledStart, newStart)
	}
	// Task stays scheduled: coverage unchanged, just elsewhere.
	var gotTask models.Task
	_ = p.DB.First(&gotTask, "id = ?", task.ID).Error
	if gotTask.Status != models.TaskScheduled {
		t.Fatalf("task status: %q, want scheduled", gotTask.Status)
	}
}

func TestReconcileConflictDeletesAndReopens(t *testing.T) {
	p, _ := deterministicPlanner(t)
	task := models.Task{Title: "pack", Kind: models.SlotPersonal, DurationMinutes: 120, Status: models.TaskScheduled}
	if err := p.DB.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(24 * time.Hour).Truncate(time.Hour)
	sess := seedReconcileSession(t, p, task.ID, start, true)

	// A human meeting lands on top (on the other account, even).
	if err := p.DB.Create(&models.Event{
		AccountKind: models.AccountWork, CalendarID: "primary", GoogleEventID: "meeting",
		StartTime: start.Add(30 * time.Minute), EndTime: start.Add(90 * time.Minute),
		Status: "confirmed",
	}).Error; err != nil {
		t.Fatal(err)
	}

	var deleted []string
	sum, err := p.reconcileWith(context.Background(), func(_ context.Context, _ models.AccountKind, _ string, eventID string) error {
		deleted = append(deleted, eventID)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if sum.SkippedConflict != 1 {
		t.Fatalf("summary: %+v", sum)
	}
	if len(deleted) != 1 || deleted[0] != *sess.GoogleEventID {
		t.Fatalf("deleted: %v, want our event", deleted)
	}
	var gotTask models.Task
	_ = p.DB.First(&gotTask, "id = ?", task.ID).Error
	if gotTask.Status != models.TaskPending {
		t.Fatalf("task status: %q, want pending", gotTask.Status)
	}
}

func TestReconcileMarksHappened(t *testing.T) {
	p, _ := deterministicPlanner(t)
	task := models.Task{Title: "pack", Kind: models.SlotPersonal, DurationMinutes: 120, Status: models.TaskScheduled}
	if err := p.DB.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-26 * time.Hour).Truncate(time.Hour)
	sess := seedReconcileSession(t, p, task.ID, start, true)

	sum, err := p.reconcileWith(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Happened != 1 {
		t.Fatalf("summary: %+v", sum)
	}
	var got models.Session
	if err := p.DB.First(&got, "id = ?", sess.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != models.SessionHappened || got.ActualStart == nil || got.ActualEnd == nil {
		t.Fatalf("session: status=%q actual=%v/%v", got.Status, got.ActualStart, got.ActualEnd)
	}
}
