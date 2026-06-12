package agent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/icco/art/lib/models"
	"gorm.io/gorm"
)

// reconcileAction is what reconciliation decided for one session.
type reconcileAction int

const (
	reconcileNone reconcileAction = iota
	// reconcileSkipDeleted: the Google event vanished after a sync — the
	// owner deleted it. Mark the session skipped; need recomputation will
	// rebook the source.
	reconcileSkipDeleted
	// reconcileMove: the event's times no longer match the session — the
	// owner dragged it. Adopt the new times.
	reconcileMove
	// reconcileSkipConflict: a human event now overlaps a future block.
	// Delete our event and mark the session skipped so it rebooks.
	reconcileSkipConflict
	// reconcileHappened: the block's end passed with the event intact.
	reconcileHappened
)

// deleteEventFunc removes one Google Calendar event. Injected so tests can
// observe deletions without Google.
type deleteEventFunc func(ctx context.Context, account models.AccountKind, calendarID, eventID string) error

// ReconcileSummary counts what one reconciliation pass changed.
type ReconcileSummary struct {
	Happened        int `json:"happened"`
	Moved           int `json:"moved"`
	SkippedDeleted  int `json:"skipped_deleted"`
	SkippedConflict int `json:"skipped_conflict"`
}

// classifySession is the pure decision core: given a session, its mirrored
// Google event (nil if absent), whether a human event overlaps it, and
// whether its calendar has synced since the session was created, decide
// what to do. Owner edits in Google Calendar are treated as signal.
func classifySession(sess models.Session, ev *models.Event, conflict, calendarSynced bool, now time.Time) reconcileAction {
	if ev == nil {
		// Absence is only meaningful once the calendar has synced since the
		// session was created; before that the event just hasn't landed yet.
		if !calendarSynced {
			return reconcileNone
		}
		return reconcileSkipDeleted
	}
	if !ev.StartTime.Equal(sess.ScheduledStart) || !ev.EndTime.Equal(sess.ScheduledEnd) {
		return reconcileMove
	}
	if !sess.ScheduledEnd.After(now) {
		return reconcileHappened
	}
	// Only future blocks get rescheduled around conflicts; an in-progress
	// block is left for the owner to handle.
	if conflict && sess.ScheduledStart.After(now) {
		return reconcileSkipConflict
	}
	return reconcileNone
}

// Reconcile drifts the database toward calendar reality for every open
// session: deleted events skip + reopen their source, moved events update
// the session, conflicting events reschedule the block, finished blocks
// become happened. Run after sync and before planning so a freed-up task is
// rebooked in the same tick.
func (p *Planner) Reconcile(ctx context.Context) (ReconcileSummary, error) {
	bw := newBlockWriter(p)
	return p.reconcileWith(ctx, func(ctx context.Context, account models.AccountKind, calendarID, eventID string) error {
		client, err := bw.clientFor(ctx, account)
		if err != nil {
			return err
		}
		return client.DeleteManaged(ctx, calendarID, eventID)
	})
}

func (p *Planner) reconcileWith(ctx context.Context, deleteEvent deleteEventFunc) (ReconcileSummary, error) {
	var sum ReconcileSummary
	now := time.Now()

	var sessions []models.Session
	if err := p.DB.WithContext(ctx).
		Where("status IN ? AND google_event_id IS NOT NULL",
			[]models.SessionStatus{models.SessionPlanned, models.SessionMoved}).
		Find(&sessions).Error; err != nil {
		return sum, err
	}

	for _, sess := range sessions {
		ev, err := p.mirroredEvent(ctx, sess)
		if err != nil {
			return sum, err
		}
		conflict, err := p.hasConflict(ctx, sess)
		if err != nil {
			return sum, err
		}
		synced, err := p.calendarSyncedSince(ctx, sess)
		if err != nil {
			return sum, err
		}

		switch classifySession(sess, ev, conflict, synced, now) {
		case reconcileSkipDeleted:
			if err := p.skipAndReopen(ctx, sess); err != nil {
				return sum, err
			}
			sum.SkippedDeleted++
		case reconcileMove:
			if err := p.DB.WithContext(ctx).Model(&models.Session{}).Where("id = ?", sess.ID).
				Updates(map[string]any{
					"scheduled_start": ev.StartTime,
					"scheduled_end":   ev.EndTime,
					"status":          models.SessionMoved,
				}).Error; err != nil {
				return sum, err
			}
			sum.Moved++
		case reconcileSkipConflict:
			if err := deleteEvent(ctx, sess.AccountKind, sess.CalendarID, *sess.GoogleEventID); err != nil {
				return sum, fmt.Errorf("delete conflicted event %s: %w", *sess.GoogleEventID, err)
			}
			if err := p.skipAndReopen(ctx, sess); err != nil {
				return sum, err
			}
			sum.SkippedConflict++
		case reconcileHappened:
			if err := p.DB.WithContext(ctx).Model(&models.Session{}).Where("id = ?", sess.ID).
				Updates(map[string]any{
					"status":       models.SessionHappened,
					"actual_start": sess.ScheduledStart,
					"actual_end":   sess.ScheduledEnd,
				}).Error; err != nil {
				return sum, err
			}
			sum.Happened++
		}
	}
	return sum, nil
}

// mirroredEvent returns the events-table row for the session's Google
// event, or nil if sync no longer sees it (cancelled events are
// hard-deleted from the table).
func (p *Planner) mirroredEvent(ctx context.Context, sess models.Session) (*models.Event, error) {
	var ev models.Event
	err := p.DB.WithContext(ctx).
		Where("account_kind = ? AND calendar_id = ? AND google_event_id = ?",
			sess.AccountKind, sess.CalendarID, *sess.GoogleEventID).
		First(&ev).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ev, nil
}

// hasConflict reports whether any human (non-art) busy event on any account
// overlaps the session, using the same busy semantics as slot finding.
func (p *Planner) hasConflict(ctx context.Context, sess models.Session) (bool, error) {
	var events []models.Event
	if err := p.DB.WithContext(ctx).
		Where("is_art_managed = false AND status <> 'cancelled' AND end_time > ? AND start_time < ?",
			sess.ScheduledStart, sess.ScheduledEnd).
		Find(&events).Error; err != nil {
		return false, err
	}
	for _, e := range events {
		if e.AllDay && !isAbsenceEvent(e) {
			continue
		}
		return true, nil
	}
	return false, nil
}

// calendarSyncedSince reports whether the session's calendar completed a
// sync after the session was created — the guard that distinguishes "owner
// deleted the event" from "sync hasn't seen it yet".
func (p *Planner) calendarSyncedSince(ctx context.Context, sess models.Session) (bool, error) {
	var st models.SyncState
	err := p.DB.WithContext(ctx).
		Where("account_kind = ? AND calendar_id = ?", sess.AccountKind, sess.CalendarID).
		First(&st).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return st.LastSyncedAt != nil && st.LastSyncedAt.After(sess.CreatedAt), nil
}

// skipAndReopen marks the session skipped and, for tasks whose coverage
// dropped below their duration, resets the task to pending so the next
// planner pass rebooks it. Done tasks stay done — the owner's word wins.
func (p *Planner) skipAndReopen(ctx context.Context, sess models.Session) error {
	if err := p.DB.WithContext(ctx).Model(&models.Session{}).Where("id = ?", sess.ID).
		Update("status", models.SessionSkipped).Error; err != nil {
		return err
	}
	if sess.Source != models.SourceTask {
		return nil // projects and habits self-heal via need computation
	}
	var task models.Task
	if err := p.DB.WithContext(ctx).First(&task, "id = ?", sess.SourceID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil // task deleted; nothing to reopen
		}
		return err
	}
	if task.Status != models.TaskScheduled {
		return nil
	}
	covered, err := sessionMinutes(ctx, p.DB, models.SourceTask, task.ID)
	if err != nil {
		return err
	}
	if covered < task.DurationMinutes {
		return p.DB.WithContext(ctx).Model(&task).Update("status", models.TaskPending).Error
	}
	return nil
}
