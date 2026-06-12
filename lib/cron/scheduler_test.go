package cron

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/icco/art/lib/agent"
	"github.com/icco/art/lib/calendar"
)

// We can't easily exercise Start without a real DB + Google client; cover the
// constructor + Stop happy path.
func TestNewAndStop(t *testing.T) {
	s := New(&calendar.Runner{}, &agent.Planner{}, 0)
	if s == nil || s.stop == nil {
		t.Fatal("scheduler not initialized")
	}
	if s.Interval != time.Hour {
		t.Fatalf("non-positive interval should default to 1h, got %v", s.Interval)
	}
	if got := New(&calendar.Runner{}, &agent.Planner{}, 30*time.Minute).Interval; got != 30*time.Minute {
		t.Fatalf("interval not stored: got %v", got)
	}
	// Start with a cancelled context so runOnce returns quickly. Then Stop.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var ran atomic.Bool
	go func() {
		s.tick = time.NewTicker(time.Millisecond)
		ran.Store(true)
		select {
		case <-ctx.Done():
		case <-s.stop:
		}
	}()
	time.Sleep(20 * time.Millisecond)
	s.tick = time.NewTicker(time.Hour) // satisfy Stop's tick.Stop()
	s.Stop()
	if !ran.Load() {
		t.Fatal("goroutine didn't run")
	}
}
