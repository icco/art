// Package queue runs art's background jobs from a Postgres-backed queue.
package queue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.icco.me/art/lib/models"
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

// backoff returns the retry delay after the nth attempt: 1m, 5m, 25m, ….
func backoff(attempt int) time.Duration {
	d := time.Minute
	for i := 1; i < attempt; i++ {
		d *= 5
	}
	return d
}

// cadence is how often a kind repeats.
func cadence(kind models.JobKind) time.Duration {
	switch kind {
	case models.JobTriage:
		return 30 * time.Minute
	default:
		return time.Hour
	}
}

// nextGrid returns the next interval-aligned slot after t, in UTC.
func nextGrid(t time.Time, interval time.Duration) time.Time {
	return t.UTC().Truncate(interval).Add(interval)
}

// onePendingConflict makes inserting a pending job a no-op when one exists.
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
		// A lost insert race means another caller's row exists, but the worker
		// may already have claimed it, so re-read running and pending.
		for range 2 {
			var found bool
			var err error
			job, running, found, err = activeJob(tx, kind)
			if err != nil {
				return err
			}
			if found {
				if running {
					return nil
				}
				job.RunAt = q.now()
				return tx.Model(&job).Update("run_at", job.RunAt).Error
			}
			if testHookBeforeEnqueueInsert != nil {
				testHookBeforeEnqueueInsert()
			}
			job = models.Job{Kind: kind, Status: models.JobPending, RunAt: q.now(), MaxAttempts: maxAttempts}
			res := tx.Clauses(onePendingConflict).Create(&job)
			if res.Error != nil || res.RowsAffected > 0 {
				return res.Error
			}
			if testHookAfterEnqueueConflict != nil {
				testHookAfterEnqueueConflict()
			}
		}
		return fmt.Errorf("enqueue %s: %w", kind, errEnqueueRace)
	})
	return job, running, err
}

// activeJob returns kind's running job, else its pending job.
func activeJob(tx *gorm.DB, kind models.JobKind) (models.Job, bool, bool, error) {
	var job models.Job
	err := tx.Where("kind = ? AND status IN ?", kind, []models.JobStatus{models.JobRunning, models.JobPending}).
		Order("status = 'running' DESC").First(&job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return models.Job{}, false, false, nil
	}
	if err != nil {
		return models.Job{}, false, false, err
	}
	return job, job.Status == models.JobRunning, true, nil
}

// Test hooks for interleaving a concurrent insert and claim inside Enqueue.
var testHookBeforeEnqueueInsert, testHookAfterEnqueueConflict func()

// claimSQL claims the next due job.
//
// The bound is a parameter, not the database's now(): run_at is written from
// Go's clock, and an app clock milliseconds ahead would claim nothing at all.
const claimSQL = `
UPDATE jobs SET status = 'running', started_at = ?, attempts = attempts + 1, updated_at = ?
WHERE id = (
	SELECT id FROM jobs
	WHERE status = 'pending' AND run_at <= ?
	ORDER BY run_at
	LIMIT 1
	FOR UPDATE SKIP LOCKED
)
RETURNING *`

// Claim returns the next due job marked running, or ErrNoJob.
func (q *Queue) Claim(ctx context.Context) (models.Job, error) {
	var job models.Job
	now := q.now()
	res := q.DB.WithContext(ctx).Raw(claimSQL, now, now, now).Scan(&job)
	if res.Error != nil {
		return models.Job{}, res.Error
	}
	if res.RowsAffected == 0 {
		return models.Job{}, ErrNoJob
	}
	return job, nil
}

// Finish records a claimed job's outcome: retryable failures go back to
// pending with backoff, terminal outcomes chain the next run onto the grid,
// and a warning lands in last_error without failing the job.
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
		next := models.Job{Kind: job.Kind, Status: models.JobPending, RunAt: nextGrid(now, cadence(job.Kind)), MaxAttempts: maxAttempts}
		return tx.Clauses(onePendingConflict).Create(&next).Error
	})
	return status, err
}

// Seed ensures every kind has a pending job; missing ones are due immediately.
func (q *Queue) Seed(ctx context.Context) error {
	for _, kind := range models.JobKinds() {
		job := models.Job{Kind: kind, Status: models.JobPending, RunAt: q.now(), MaxAttempts: maxAttempts}
		if err := q.DB.WithContext(ctx).Clauses(onePendingConflict).Create(&job).Error; err != nil {
			return err
		}
	}
	return nil
}

// ReapStale resets running jobs whose started_at predates the cutoff. Unlike
// Reap this is safe to call while the worker is live: a row older than the
// cutoff cannot belong to a job anyone is still going to finish, because
// execution is bounded by the job timeout.
//
// This exists because a failed Finish leaves its row running forever, and the
// next job of that kind is only scheduled inside Finish's transaction -- so one
// failed write silently stops that kind until the process restarts. Triage was
// dead for two days that way.
func (q *Queue) ReapStale(ctx context.Context, olderThan time.Duration) (int64, error) {
	now := q.now()
	res := q.DB.WithContext(ctx).Model(&models.Job{}).
		Where("status = ? AND started_at IS NOT NULL AND started_at < ?",
			models.JobRunning, now.Add(-olderThan)).
		Updates(map[string]any{
			"status":     models.JobPending,
			"run_at":     now,
			"started_at": nil,
		})
	return res.RowsAffected, res.Error
}

// Reap resets running jobs to pending: at boot any running row belongs to a
// dead process. Attempts were counted at claim, so crash loops still exhaust
// MaxAttempts.
func (q *Queue) Reap(ctx context.Context) error {
	return q.DB.WithContext(ctx).Model(&models.Job{}).Where("status = ?", models.JobRunning).Updates(map[string]any{
		"status":     models.JobPending,
		"run_at":     q.now(),
		"started_at": nil,
	}).Error
}

// DropRetiredKinds deletes queued jobs of no-longer-served kinds, so a row from
// a merged-away kind (e.g. the old sync or planner) can't fail and re-chain forever.
func (q *Queue) DropRetiredKinds(ctx context.Context) error {
	kinds := models.JobKinds()
	valid := make([]string, len(kinds))
	for i, k := range kinds {
		valid[i] = string(k)
	}
	return q.DB.WithContext(ctx).Where("kind NOT IN ?", valid).Delete(&models.Job{}).Error
}
