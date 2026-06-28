// Package cron runs art's periodic sync and planner work.
package cron

import (
	"context"
	"sync"
	"time"

	"github.com/icco/art/lib/agent"
	"github.com/icco/art/lib/calendar"
	"github.com/icco/art/lib/email"
	gutillog "github.com/icco/gutil/logging"
)

// A hung Google/Vertex call must not block the next hourly tick.
const runOnceTimeout = 30 * time.Minute

// Scheduler ticks the calendar sync, planner, and email triage on a fixed
// interval.
type Scheduler struct {
	Sync    *calendar.Runner
	Planner *agent.Planner
	Triage  *email.Runner

	tick *time.Ticker
	stop chan struct{}
	wg   sync.WaitGroup
}

// New returns a Scheduler ready to be Start()ed.
func New(sync *calendar.Runner, planner *agent.Planner, triage *email.Runner) *Scheduler {
	return &Scheduler{Sync: sync, Planner: planner, Triage: triage, stop: make(chan struct{})}
}

// Start runs sync + planner once, then hourly until ctx is cancelled.
func (s *Scheduler) Start(ctx context.Context) {
	s.tick = time.NewTicker(time.Hour)
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
	if err := s.Planner.Run(tickCtx); err != nil {
		log.Errorw("planner failed", "err", err)
	}
	if s.Triage != nil {
		if err := s.Triage.RunAll(tickCtx); err != nil {
			log.Errorw("triage failed", "err", err)
		}
	}
}
