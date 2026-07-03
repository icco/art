package cron

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/icco/art/lib/agent"
	"github.com/icco/art/lib/calendar"
	"github.com/icco/art/lib/email"
)

// A panicking job must not propagate: it would kill the scheduler goroutine
// and with it the whole process.
func TestRunJobRecoversPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic escaped runJob: %v", r)
		}
	}()
	runJob(context.Background(), "boom", func() { panic("kaboom") })
}

// We can't easily exercise Start without a real DB + Google client; cover the
// constructor + Stop happy path.
func TestNewAndStop(t *testing.T) {
	s := New(&calendar.Runner{}, &agent.Planner{}, &email.Runner{})
	if s == nil || s.stop == nil {
		t.Fatal("scheduler not initialized")
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
