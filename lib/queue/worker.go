package queue

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/icco/art/lib/calendar"
	"github.com/icco/art/lib/models"
	gutillog "github.com/icco/gutil/logging"
	"gorm.io/gorm"
)

const (
	pollInterval = 15 * time.Second
	// A hung Google/Vertex call must not block the queue forever.
	jobTimeout = 30 * time.Minute
	// How many times to try the finish write. Losing it strands the row in
	// running and, because the reschedule shares its transaction, stops that
	// kind entirely -- so it is worth more than one attempt.
	finishAttempts = 3
	finishBackoff  = 2 * time.Second
	// A running row older than this belongs to nobody: execution is capped at
	// jobTimeout, so double that is past any legitimate run.
	staleJobAfter = 2 * jobTimeout
)

// The job implementations, satisfied by calendar.Runner, agent.Planner, and
// email.Runner.
type (
	// SyncService runs upstream calendar syncs.
	SyncService interface {
		RunAll(ctx context.Context) (map[string]string, error)
	}
	// PlannerService executes a planner pass.
	PlannerService interface {
		Run(ctx context.Context) error
	}
	// ReconcileService heals sessions against the synced calendar mirror.
	ReconcileService interface {
		Run(ctx context.Context) error
	}
	// TriageService executes an email-triage pass.
	TriageService interface {
		RunAll(ctx context.Context) error
	}
)

// Worker polls the queue and runs jobs one at a time.
type Worker struct {
	Queue     *Queue
	Sync      SyncService
	Reconcile ReconcileService
	Planner   PlannerService
	Triage    TriageService

	poke     chan struct{}
	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// New returns a Worker over db ready to be Start()ed.
func New(db *gorm.DB, sync SyncService, reconcile ReconcileService, planner PlannerService, triage TriageService) *Worker {
	return &Worker{
		Queue:     &Queue{DB: db},
		Sync:      sync,
		Reconcile: reconcile,
		Planner:   planner,
		Triage:    triage,
		poke:      make(chan struct{}, 1),
		stop:      make(chan struct{}),
	}
}

// Start reaps orphans, drops retired kinds, seeds missing schedules, and
// launches the poll loop.
func (w *Worker) Start(ctx context.Context) error {
	if err := w.Queue.Reap(ctx); err != nil {
		return fmt.Errorf("queue reap: %w", err)
	}
	if err := w.Queue.DropRetiredKinds(ctx); err != nil {
		return fmt.Errorf("queue drop retired kinds: %w", err)
	}
	if err := calendar.PruneForeignCalendars(ctx, w.Queue.DB); err != nil {
		return fmt.Errorf("prune foreign calendars: %w", err)
	}
	if err := w.Queue.Seed(ctx); err != nil {
		return fmt.Errorf("queue seed: %w", err)
	}
	w.wg.Go(func() {
		tick := time.NewTicker(pollInterval)
		defer tick.Stop()
		w.drain(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-w.stop:
				return
			case <-w.poke:
			case <-tick.C:
			}
			w.reapStale(ctx)
			w.drain(ctx)
		}
	})
	return nil
}

// Stop halts polling and waits for any in-flight job to return.
func (w *Worker) Stop() {
	w.stopOnce.Do(func() { close(w.stop) })
	w.wg.Wait()
}

// Poke wakes the worker before the next poll tick.
func (w *Worker) Poke() {
	select {
	case w.poke <- struct{}{}:
	default:
	}
}

// Enqueue queues kind to run now and wakes the worker.
func (w *Worker) Enqueue(ctx context.Context, kind models.JobKind) (models.Job, bool, error) {
	job, running, err := w.Queue.Enqueue(ctx, kind)
	if err == nil && !running {
		w.Poke()
	}
	return job, running, err
}

// reapStale returns rows abandoned by a finish that never landed. Boot-time
// Reap cannot help here: the process is still up, so nothing restarts it.
func (w *Worker) reapStale(ctx context.Context) {
	n, err := w.Queue.ReapStale(ctx, staleJobAfter)
	if err != nil {
		gutillog.FromContext(ctx).Errorw("stale job reap failed", "err", err)
		return
	}
	if n > 0 {
		gutillog.FromContext(ctx).Warnw("requeued stale running job(s)", "count", n, "stale_after", staleJobAfter)
	}
}

// drain claims and runs due jobs until the queue is empty or ctx ends.
func (w *Worker) drain(ctx context.Context) {
	log := gutillog.FromContext(ctx)
	for ctx.Err() == nil {
		select {
		case <-w.stop:
			return
		default:
		}
		job, err := w.Queue.Claim(ctx)
		if errors.Is(err, ErrNoJob) {
			return
		}
		if err != nil {
			log.Errorw("job claim failed", "err", err)
			return
		}
		w.run(ctx, job)
	}
}

// run executes one claimed job; the finish write survives ctx cancellation
// so shutdown records the retry instead of leaving the row running.
func (w *Worker) run(ctx context.Context, job models.Job) {
	log := gutillog.FromContext(ctx)
	log.Infow("job started", "job", job.ID, "kind", job.Kind, "attempt", job.Attempts)
	start := time.Now()
	jobCtx, cancel := context.WithTimeout(ctx, jobTimeout)
	warning, err := w.execute(jobCtx, job.Kind)
	cancel()
	jobDuration.WithLabelValues(string(job.Kind)).Observe(time.Since(start).Seconds())

	// Generous relative to a single write: finishAttempts tries plus backoff.
	finishCtx, cancelFinish := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancelFinish()
	status, finishErr := w.finish(finishCtx, job, err, warning)
	if finishErr != nil {
		// The row stays running and no successor is scheduled. ReapStale is
		// what gets this kind moving again, hence the pointer to it here.
		log.Errorw("job finish failed; row left running until it is reaped as stale",
			"job", job.ID, "kind", job.Kind, "stale_after", staleJobAfter, "err", finishErr)
		return
	}
	jobsProcessed.WithLabelValues(string(job.Kind), string(status)).Inc()
	switch {
	case err != nil && status == models.JobPending:
		log.Warnw("job failed, retry scheduled", "job", job.ID, "kind", job.Kind, "attempt", job.Attempts, "err", err)
	case err != nil:
		log.Errorw("job failed", "job", job.ID, "kind", job.Kind, "attempts", job.Attempts, "err", err)
	case warning != "":
		log.Warnw("job succeeded with warnings", "job", job.ID, "kind", job.Kind, "warnings", warning)
	default:
		log.Infow("job succeeded", "job", job.ID, "kind", job.Kind)
	}
}

// finish writes the outcome, retrying transient failures. A lost finish is not
// a lost job on its own -- it also loses the successor, which is scheduled in
// the same transaction -- so it gets more than one shot before we give up.
func (w *Worker) finish(
	ctx context.Context,
	job models.Job,
	runErr error,
	warning string,
) (models.JobStatus, error) {
	var status models.JobStatus
	var err error
	for attempt := 1; attempt <= finishAttempts; attempt++ {
		status, err = w.Queue.Finish(ctx, job, runErr, warning)
		if err == nil {
			return status, nil
		}
		if attempt == finishAttempts || ctx.Err() != nil {
			break
		}
		select {
		case <-ctx.Done():
			return status, err
		case <-time.After(finishBackoff):
		}
	}
	return status, err
}

// execute dispatches to the job implementation, converting panics to errors.
func (w *Worker) execute(ctx context.Context, kind models.JobKind) (warning string, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: %v\n%s", errPanic, r, debug.Stack())
		}
	}()
	switch kind {
	case models.JobSync:
		// Reconcile runs as the sync tail, always against a fresh mirror.
		accountErrs, runErr := w.Sync.RunAll(ctx)
		if runErr != nil {
			return "", runErr
		}
		if err := w.Reconcile.Run(ctx); err != nil {
			return "", err
		}
		return formatAccountErrors(accountErrs), nil
	case models.JobPlanner:
		return "", w.Planner.Run(ctx)
	case models.JobTriage:
		return "", w.Triage.RunAll(ctx)
	}
	return "", fmt.Errorf("%w %q", errUnknownJobKind, kind)
}

// formatAccountErrors flattens sync's per-account errors, sorted for
// stable output.
func formatAccountErrors(errs map[string]string) string {
	if len(errs) == 0 {
		return ""
	}
	keys := make([]string, 0, len(errs))
	for k := range errs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+": "+errs[k])
	}
	return strings.Join(parts, "; ")
}
