// Package cron runs art's periodic sync and planner work.
package cron

import (
	"context"
	"sync"
	"time"

	"github.com/icco/art/lib/agent"
	"github.com/icco/art/lib/calendar"
	gutillog "github.com/icco/gutil/logging"
)

// A hung Google/Vertex call must not block the next hourly tick.
const runOnceTimeout = 30 * time.Minute

// Scheduler ticks the calendar sync and planner on a fixed interval.
type Scheduler struct {
	Sync     *calendar.Runner
	Planner  *agent.Planner
	Interval time.Duration

	tick *time.Ticker
	stop chan struct{}
	wg   sync.WaitGroup
}

// New returns a Scheduler ready to be Start()ed. A non-positive interval
// falls back to hourly.
func New(sync *calendar.Runner, planner *agent.Planner, interval time.Duration) *Scheduler {
	if interval <= 0 {
		interval = time.Hour
	}
	return &Scheduler{Sync: sync, Planner: planner, Interval: interval, stop: make(chan struct{})}
}

// Start runs sync + planner once, then on every interval tick until ctx is
// cancelled.
func (s *Scheduler) Start(ctx context.Context) {
	s.tick = time.NewTicker(s.Interval)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.runOnce(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stop:
				return
			case <-s.tick.C:
				s.runOnce(ctx)
			}
		}
	}()
}

// Stop halts the ticker and waits for any in-flight tick to return.
func (s *Scheduler) Stop() {
	if s.tick != nil {
		s.tick.Stop()
	}
	close(s.stop)
	s.wg.Wait()
}

func (s *Scheduler) runOnce(ctx context.Context) {
	log := gutillog.FromContext(ctx)
	tickCtx, cancel := context.WithTimeout(ctx, runOnceTimeout)
	defer cancel()
	if errs, err := s.Sync.RunAll(tickCtx); err != nil {
		log.Errorw("sync failed", "err", err)
	} else if len(errs) > 0 {
		log.Warnw("sync had per-account errors", "errors", errs)
	}
	// Reconcile between sync and plan so a block freed up by an owner edit
	// (deleted/moved event, conflicting meeting) is rebooked in this tick.
	// ReconcileAndRun holds one lock across both steps so a manual /replan
	// can't interleave.
	sum, err := s.Planner.ReconcileAndRun(tickCtx)
	if err != nil {
		log.Errorw("planner failed", "err", err)
	}
	if sum != (agent.ReconcileSummary{}) {
		log.Infow("reconciled drift",
			"happened", sum.Happened, "moved", sum.Moved,
			"skipped_deleted", sum.SkippedDeleted, "skipped_conflict", sum.SkippedConflict)
	}
}
