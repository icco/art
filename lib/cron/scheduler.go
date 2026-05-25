package cron

import (
	"context"
	"sync"
	"time"

	"github.com/icco/art/lib/agent"
	"github.com/icco/art/lib/calendar"
	"github.com/icco/art/lib/logging"
)

// A hung Google/Vertex call must not block the next hourly tick.
const runOnceTimeout = 30 * time.Minute

type Scheduler struct {
	Sync    *calendar.Runner
	Planner *agent.Planner

	tick *time.Ticker
	stop chan struct{}
	wg   sync.WaitGroup
}

func New(sync *calendar.Runner, planner *agent.Planner) *Scheduler {
	return &Scheduler{Sync: sync, Planner: planner, stop: make(chan struct{})}
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

func (s *Scheduler) Stop() {
	if s.tick != nil {
		s.tick.Stop()
	}
	close(s.stop)
	s.wg.Wait()
}

func (s *Scheduler) runOnce(ctx context.Context) {
	log := logging.From(ctx)
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
}
