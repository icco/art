package cost

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/testdb"
)

func closeTo(got, want float64) bool { return math.Abs(got-want) < 1e-9 }

func TestUSDKnownModel(t *testing.T) {
	// 1M in + 1M out on Flash = $0.30 + $2.50.
	if got := usd("gemini-2.5-flash", 1_000_000, 1_000_000); !closeTo(got, 2.80) {
		t.Errorf("flash 1M/1M = %v, want 2.80", got)
	}
	if got := usd("gemini-2.5-pro", 1_000_000, 1_000_000); !closeTo(got, 11.25) {
		t.Errorf("pro 1M/1M = %v, want 11.25", got)
	}
	if got := usd("gemini-2.5-flash", 0, 0); got != 0 {
		t.Errorf("zero tokens = %v, want 0", got)
	}
}

func TestUSDUnknownModelPricesHigh(t *testing.T) {
	r, known := rateFor("gemini-9-ultra")
	if known {
		t.Fatal("expected an unknown model")
	}
	if r != unknownModelRate {
		t.Errorf("unknown model rate = %+v, want %+v", r, unknownModelRate)
	}
	if usd("gemini-9-ultra", 1_000_000, 0) <= 0 {
		t.Error("an unknown model must still cost something")
	}
}

func TestGuardUnlimitedWhenBudgetZero(t *testing.T) {
	g := &Guard{budget: 0, spent: 1000}
	if err := g.Allow(); err != nil {
		t.Errorf("a zero budget means unlimited, got %v", err)
	}
}

func TestGuardBlocksWhenSpent(t *testing.T) {
	g := &Guard{budget: 2.0, spent: 1.99}
	if err := g.Allow(); err != nil {
		t.Fatalf("under budget should be allowed, got %v", err)
	}
	// Record pushes it over, and the ceiling must bite within the run.
	g.Record("gemini-2.5-pro", 1_000_000, 0)
	err := g.Allow()
	var exhausted *ErrBudgetExhausted
	if !errors.As(err, &exhausted) {
		t.Fatalf("want *ErrBudgetExhausted, got %v", err)
	}
	if exhausted.BudgetUSD != 2.0 {
		t.Errorf("budget in error = %v, want 2", exhausted.BudgetUSD)
	}
	if exhausted.Error() == "" {
		t.Error("error message should not be empty")
	}
}

func TestGuardBlocksExactlyAtBudget(t *testing.T) {
	g := &Guard{budget: 2.0, spent: 2.0}
	if err := g.Allow(); err == nil {
		t.Error("spending exactly the budget must stop further calls")
	}
}

func TestSpentTodayCountsOnlyTodaysPricedRuns(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	tz := time.UTC

	mk := func(model string, in, out int, started time.Time) {
		t.Helper()
		if err := db.Create(&models.AgentRun{
			Kind: models.AgentRunTriage, StartedAt: started,
			Status: models.AgentRunSucceeded, Model: model,
			TokensIn: in, TokensOut: out,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	now := time.Now().In(tz)
	midday := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, tz)
	if midday.After(now) {
		midday = now
	}

	mk("gemini-2.5-flash", 1_000_000, 0, midday)                // $0.30 today
	mk("gemini-2.5-flash", 1_000_000, 0, now.AddDate(0, 0, -3)) // older: excluded
	// A deterministic planner run: no model, so no cost and no rate lookup.
	mk("", 0, 0, midday)

	got, err := SpentToday(ctx, db, tz)
	if err != nil {
		t.Fatal(err)
	}
	if !closeTo(got, 0.30) {
		t.Errorf("SpentToday = %v, want 0.30", got)
	}
}

func TestNewGuardReadsTodaysSpend(t *testing.T) {
	db := testdb.Open(t)
	ctx := context.Background()
	tz := time.UTC

	now := time.Now().In(tz)
	if err := db.Create(&models.AgentRun{
		Kind: models.AgentRunTriage, StartedAt: now,
		Status: models.AgentRunSucceeded, Model: "gemini-2.5-pro",
		TokensIn: 1_000_000, TokensOut: 200_000, // $1.25 + $2.00 = $3.25
	}).Error; err != nil {
		t.Fatal(err)
	}

	g, err := NewGuard(ctx, db, tz, 2.0)
	if err != nil {
		t.Fatal(err)
	}
	if !closeTo(g.SpentUSD(), 3.25) {
		t.Errorf("spent = %v, want 3.25", g.SpentUSD())
	}
	// Already over: triage must not start.
	if err := g.Allow(); err == nil {
		t.Error("a day already over budget must block further calls")
	}
}
