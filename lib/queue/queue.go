// Package queue implements the Postgres-backed job queue and worker that
// replaced the in-memory cron scheduler; see
// docs/superpowers/specs/2026-07-04-db-job-queue-design.md.
package queue

import (
	"context"
	"errors"
	"time"

	"github.com/icco/art/lib/models"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// maxAttempts bounds tries per job: the first run plus three retries.
const maxAttempts = 4

// ErrNoJob is returned by Claim when nothing is due.
var ErrNoJob = errors.New("queue: no job due")

// Queue provides persistent job operations over the jobs table.
type Queue struct {
	DB *gorm.DB
	// Now is injectable for tests; nil means time.Now.
	Now func() time.Time
}

func (q *Queue) now() time.Time {
	if q.Now != nil {
		return q.Now()
	}
	return time.Now()
}

// backoff returns the retry delay after the nth attempt: 1m, 5m, 25m, …
func backoff(attempt int) time.Duration {
	d := time.Minute
	for i := 1; i < attempt; i++ {
		d *= 5
	}
	return d
}

// nextSlot returns the next top-of-hour grid slot after t, in UTC. All kinds
// recur hourly; scheduled runs stay pinned to the clock grid.
func nextSlot(t time.Time) time.Time {
	return t.UTC().Truncate(time.Hour).Add(time.Hour)
}

// onePendingConflict targets the partial unique index so inserting a pending
// job is a no-op when one already exists for that kind.
var onePendingConflict = clause.OnConflict{
	Columns:     []clause.Column{{Name: "kind"}},
	TargetWhere: clause.Where{Exprs: []clause.Expression{gorm.Expr("status = 'pending'")}},
	DoNothing:   true,
}

// Enqueue makes kind runnable now: a running job is reported as-is, an
// existing pending job is pulled forward, otherwise a fresh row is inserted.
func (q *Queue) Enqueue(ctx context.Context, kind models.JobKind) (models.Job, bool, error) {
	var job models.Job
	running := false
	err := q.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Where("kind = ? AND status = ?", kind, models.JobRunning).First(&job).Error
		if err == nil {
			running = true
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		err = tx.Where("kind = ? AND status = ?", kind, models.JobPending).First(&job).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			job = models.Job{Kind: kind, Status: models.JobPending, RunAt: q.now(), MaxAttempts: maxAttempts}
			return tx.Create(&job).Error
		}
		if err != nil {
			return err
		}
		job.RunAt = q.now()
		return tx.Model(&job).Update("run_at", job.RunAt).Error
	})
	return job, running, err
}

// claimSQL atomically claims the next due pending job; kinds sharing a slot
// run in sync → planner → triage order. SKIP LOCKED keeps a second worker or
// replica safe even though today there is only one.
const claimSQL = `
UPDATE jobs SET status = 'running', started_at = now(), attempts = attempts + 1, updated_at = now()
WHERE id = (
	SELECT id FROM jobs
	WHERE status = 'pending' AND run_at <= now()
	ORDER BY run_at, CASE kind WHEN 'sync' THEN 0 WHEN 'planner' THEN 1 ELSE 2 END
	LIMIT 1
	FOR UPDATE SKIP LOCKED
)
RETURNING *`

// Claim returns the next due job marked running, or ErrNoJob.
func (q *Queue) Claim(ctx context.Context) (models.Job, error) {
	var job models.Job
	res := q.DB.WithContext(ctx).Raw(claimSQL).Scan(&job)
	if res.Error != nil {
		return models.Job{}, res.Error
	}
	if res.RowsAffected == 0 {
		return models.Job{}, ErrNoJob
	}
	return job, nil
}

// Finish records a claimed job's outcome and returns the status it ended in.
// Retryable failures go back to pending with backoff; terminal outcomes chain
// the next recurring run onto the hourly grid. A warning (e.g. sync's
// per-account errors) is kept in last_error without failing the job.
func (q *Queue) Finish(ctx context.Context, job models.Job, runErr error, warning string) (models.JobStatus, error) {
	now := q.now()
	if runErr != nil && job.Attempts < job.MaxAttempts {
		err := q.DB.WithContext(ctx).Model(&models.Job{}).Where("id = ?", job.ID).Updates(map[string]any{
			"status":     models.JobPending,
			"run_at":     now.Add(backoff(job.Attempts)),
			"started_at": nil,
			"last_error": runErr.Error(),
		}).Error
		return models.JobPending, err
	}
	status := models.JobSucceeded
	lastError := warning
	if runErr != nil {
		status = models.JobFailed
		lastError = runErr.Error()
	}
	err := q.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.Job{}).Where("id = ?", job.ID).Updates(map[string]any{
			"status":      status,
			"finished_at": now,
			"last_error":  lastError,
		}).Error; err != nil {
			return err
		}
		next := models.Job{Kind: job.Kind, Status: models.JobPending, RunAt: nextSlot(now), MaxAttempts: maxAttempts}
		return tx.Clauses(onePendingConflict).Create(&next).Error
	})
	return status, err
}

// Seed ensures every kind has a pending job; missing ones are due
// immediately, so a fresh deploy runs its first pass at boot.
func (q *Queue) Seed(ctx context.Context) error {
	for _, kind := range models.JobKinds() {
		job := models.Job{Kind: kind, Status: models.JobPending, RunAt: q.now(), MaxAttempts: maxAttempts}
		if err := q.DB.WithContext(ctx).Clauses(onePendingConflict).Create(&job).Error; err != nil {
			return err
		}
	}
	return nil
}

// Reap resets running jobs to pending-now. Called at boot, before the worker
// starts: in a single-process deployment any running row belongs to a dead
// process. Attempts were counted at claim, so crash loops still exhaust
// MaxAttempts.
func (q *Queue) Reap(ctx context.Context) error {
	return q.DB.WithContext(ctx).Model(&models.Job{}).Where("status = ?", models.JobRunning).Updates(map[string]any{
		"status":     models.JobPending,
		"run_at":     q.now(),
		"started_at": nil,
	}).Error
}
