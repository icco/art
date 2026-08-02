// Package reconcile heals Art's session plan against the synced calendar mirror:
// it updates moved blocks, drops deleted ones, and retracts blocks a human event
// now overlaps.
package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/icco/art/lib/calendar"
	"github.com/icco/art/lib/models"
	gutillog "github.com/icco/gutil/logging"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// freshness bounds how stale the events mirror may be before reconcile refuses
// destructive actions: 3x the 10-minute sync cadence.
const freshness = 30 * time.Minute

// CalendarService retracts an Art-managed calendar event; satisfied by
// *calendar.Manager.
type CalendarService interface {
	DeleteManaged(ctx context.Context, account models.AccountKind, calendarID, eventID string) error
}

// SoftTitler supplies the placeholder-event titles the planner may book over;
// satisfied by *settings.Store.
type SoftTitler interface {
	SoftTitles(ctx context.Context) (models.SoftTitles, error)
}

// Runner heals planned sessions against the events mirror on each pass.
type Runner struct {
	DB  *gorm.DB
	Cal CalendarService
	TZ  *time.Location
	// Settings is read once per pass, so an edit lands on the next sync.
	Settings SoftTitler
	// Now is injectable for tests; nil means time.Now.
	Now func() time.Time
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Run performs one reconcile pass and records it as a reconcile AgentRun.
func (r *Runner) Run(ctx context.Context) error {
	run := models.AgentRun{Kind: models.AgentRunReconcile, StartedAt: r.now(), Status: models.AgentRunRunning}
	if err := r.DB.WithContext(ctx).Create(&run).Error; err != nil {
		return err
	}
	summary := map[string]any{"moved": 0, "deleted": 0, "conflicts": 0, "skipped_stale": false}
	runErr := r.reconcile(ctx, summary)
	return r.finish(ctx, run.ID, summary, runErr)
}

func (r *Runner) reconcile(ctx context.Context, summary map[string]any) error {
	log := gutillog.FromContext(ctx)
	now := r.now()
	if !r.mirrorFresh(ctx, now) {
		summary["skipped_stale"] = true
		log.Warnw("reconcile skipped: sync mirror stale")
		return nil
	}

	soft, err := r.Settings.SoftTitles(ctx)
	if err != nil {
		return err
	}

	windowStart := now.Add(-calendar.HistoryWindow)
	windowEnd := now.Add(calendar.FutureWindow)

	var sessions []models.Session
	if err := r.DB.WithContext(ctx).
		Where("status = ? AND google_event_id IS NOT NULL AND scheduled_start >= ? AND scheduled_start < ?",
			models.SessionPlanned, windowStart, windowEnd).
		Find(&sessions).Error; err != nil {
		return err
	}
	for _, s := range sessions {
		if err := r.reconcileOne(ctx, summary, s, soft); err != nil {
			return err
		}
	}
	return nil
}

// mirrorFresh reports whether some calendar synced within the freshness window.
func (r *Runner) mirrorFresh(ctx context.Context, now time.Time) bool {
	var latest *time.Time
	if err := r.DB.WithContext(ctx).Model(&models.SyncState{}).
		Select("max(last_synced_at)").Scan(&latest).Error; err != nil {
		return false
	}
	return latest != nil && now.Sub(*latest) <= freshness
}

func (r *Runner) reconcileOne(ctx context.Context, summary map[string]any, s models.Session, soft models.SoftTitles) error {
	log := gutillog.FromContext(ctx)

	var ev models.Event
	err := r.DB.WithContext(ctx).
		Where("account_kind = ? AND calendar_id = ? AND google_event_id = ?",
			s.AccountKind, s.CalendarID, *s.GoogleEventID).
		First(&ev).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if delErr := r.DB.WithContext(ctx).Delete(&models.Session{}, "id = ?", s.ID).Error; delErr != nil {
			return delErr
		}
		log.Infow("session drift: deleted", "session", s.ID)
		inc(summary, "deleted")
		return nil
	}
	if err != nil {
		return err
	}

	if !ev.StartTime.Equal(s.ScheduledStart) || !ev.EndTime.Equal(s.ScheduledEnd) {
		updates := map[string]any{"scheduled_start": ev.StartTime, "scheduled_end": ev.EndTime}
		if s.PlannedStart == nil {
			ps, pe := s.ScheduledStart, s.ScheduledEnd
			updates["planned_start"] = &ps
			updates["planned_end"] = &pe
		}
		if upErr := r.DB.WithContext(ctx).Model(&models.Session{}).Where("id = ?", s.ID).Updates(updates).Error; upErr != nil {
			return upErr
		}
		log.Infow("session drift: moved", "session", s.ID, "from", s.ScheduledStart, "to", ev.StartTime)
		inc(summary, "moved")
		s.ScheduledStart, s.ScheduledEnd = ev.StartTime, ev.EndTime
	}

	conflict, err := r.hasHumanConflict(ctx, s, soft)
	if err != nil {
		return err
	}
	if conflict {
		if delErr := r.Cal.DeleteManaged(ctx, s.AccountKind, s.CalendarID, *s.GoogleEventID); delErr != nil {
			return delErr
		}
		if delErr := r.DB.WithContext(ctx).Delete(&models.Session{}, "id = ?", s.ID).Error; delErr != nil {
			return delErr
		}
		log.Infow("session drift: conflict retract", "session", s.ID, "event", *s.GoogleEventID)
		inc(summary, "conflicts")
	}
	return nil
}

// hasHumanConflict reports whether a non-Art busy event overlaps the session,
// using the same busy predicate as the planner's loadBusy. Soft titles are
// excluded — the planner may book over them, so retracting would undo that on
// the next sync. The title test runs in Go, not SQL: btrim() trims spaces where
// strings.TrimSpace trims all Unicode whitespace, and the two disagreeing is
// exactly how a block gets booked and then retracted.
//
// Mirrors of Art's own block are excluded too: a third-party calendar sync
// copies it to the other calendar as "Busy", which is not art-managed and so
// read as a conflict — Art retracted its own reflection every cycle.
func (r *Runner) hasHumanConflict(ctx context.Context, s models.Session, soft models.SoftTitles) (bool, error) {
	var rows []struct {
		Summary            string
		ExtendedProperties string
	}
	if err := r.DB.WithContext(ctx).Model(&models.Event{}).
		Select("summary, coalesce(extended_properties::text, '') AS extended_properties").
		Where(`account_kind = ? AND is_art_managed = false AND status <> 'cancelled'
		       AND (all_day = false OR event_type = 'outOfOffice')
		       AND end_time > ? AND start_time < ?`,
			s.AccountKind, s.ScheduledStart, s.ScheduledEnd).
		Scan(&rows).Error; err != nil {
		return false, err
	}
	for _, row := range rows {
		if soft.Match(row.Summary) || isMirrorOf(row.ExtendedProperties, s.GoogleEventID) {
			continue
		}
		return true, nil
	}
	return false, nil
}

// isMirrorOf reports whether an event's extended properties reference eventID,
// which means some sync tool created it as a copy of that event.
func isMirrorOf(extendedProperties string, eventID *string) bool {
	if eventID == nil || *eventID == "" || extendedProperties == "" {
		return false
	}
	return strings.Contains(extendedProperties, *eventID)
}

func (r *Runner) finish(ctx context.Context, id string, summary map[string]any, runErr error) error {
	status := models.AgentRunSucceeded
	errStr := ""
	if runErr != nil {
		status = models.AgentRunFailed
		errStr = runErr.Error()
	}
	body, _ := json.Marshal(summary)
	t := r.now()
	if err := r.DB.WithContext(context.WithoutCancel(ctx)).Model(&models.AgentRun{}).Where("id = ?", id).Updates(map[string]any{
		"ended_at": &t,
		"status":   string(status),
		"summary":  datatypes.JSON(body),
		"error":    errStr,
	}).Error; err != nil {
		return errors.Join(runErr, err)
	}
	return runErr
}

func inc(summary map[string]any, key string) {
	if v, ok := summary[key].(int); ok {
		summary[key] = v + 1
	}
}
