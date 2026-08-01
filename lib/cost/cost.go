// Package cost turns recorded token counts into dollars and enforces a daily
// spend ceiling.
//
// It exists because art once ran up $737 of Vertex AI in a month without any
// signal: the planner recorded tokens_in = tokens_out = 0 on every row, so the
// only place the spend appeared was the Cloud Billing console. Cost that is not
// written down next to the work that caused it is cost nobody notices.
package cost

import (
	"context"
	"fmt"
	"time"

	"github.com/icco/art/lib/models"
	"gorm.io/gorm"
)

// Rate is the list price in USD per million tokens for one model.
type Rate struct {
	InPerMillion  float64
	OutPerMillion float64
}

// rates holds Vertex AI pay-as-you-go list prices. These are a billing
// estimate, not an invoice: they ignore tiering, batch discounts and context
// caching, all of which only make the real charge lower. Erring high is the
// point — the guard should trip early rather than late.
var rates = map[string]Rate{
	"gemini-2.5-flash": {InPerMillion: 0.30, OutPerMillion: 2.50},
	"gemini-2.5-pro":   {InPerMillion: 1.25, OutPerMillion: 10.00},
}

// unknownModelRate prices a model absent from the table. It is the most
// expensive entry, so an unrecognised model over-reports rather than slipping
// past the ceiling unpriced.
var unknownModelRate = Rate{InPerMillion: 1.25, OutPerMillion: 10.00}

// RateFor returns the price for a model, and whether it was known.
func RateFor(model string) (Rate, bool) {
	r, ok := rates[model]
	if !ok {
		return unknownModelRate, false
	}
	return r, true
}

// USD prices one call's token usage.
func USD(model string, tokensIn, tokensOut int) float64 {
	r, _ := RateFor(model)
	return float64(tokensIn)/1e6*r.InPerMillion + float64(tokensOut)/1e6*r.OutPerMillion
}

// SpentToday sums the priced token usage of every agent run started since
// midnight in tz. Rows with an empty model contribute nothing: a deterministic
// planner run has no model and no cost.
func SpentToday(ctx context.Context, db *gorm.DB, tz *time.Location) (float64, error) {
	now := time.Now().In(tz)
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, tz)

	var rows []struct {
		Model     string
		TokensIn  int64
		TokensOut int64
	}
	if err := db.WithContext(ctx).Model(&models.AgentRun{}).
		Select("model, SUM(tokens_in) AS tokens_in, SUM(tokens_out) AS tokens_out").
		Where("started_at >= ? AND model <> ''", midnight).
		Group("model").
		Scan(&rows).Error; err != nil {
		return 0, err
	}

	var total float64
	for _, r := range rows {
		total += USD(r.Model, int(r.TokensIn), int(r.TokensOut))
	}
	return total, nil
}

// ErrBudgetExhausted reports that today's ceiling is already reached, so the
// caller must not start LLM work.
type ErrBudgetExhausted struct {
	SpentUSD  float64
	BudgetUSD float64
}

func (e *ErrBudgetExhausted) Error() string {
	return fmt.Sprintf("daily LLM budget exhausted: $%.4f spent of $%.2f", e.SpentUSD, e.BudgetUSD)
}

// Guard tracks spend against a daily ceiling across one run. Construct it with
// NewGuard, which reads what the day has already cost, then call Allow before
// each model call and Record after it.
//
// A budget of zero or less means unlimited: the ceiling is opt-in so a
// misconfigured setting cannot silently stop triage forever.
type Guard struct {
	budget float64
	spent  float64
}

// NewGuard reads today's spend so far and returns a guard for the remainder.
func NewGuard(ctx context.Context, db *gorm.DB, tz *time.Location, budgetUSD float64) (*Guard, error) {
	spent, err := SpentToday(ctx, db, tz)
	if err != nil {
		return nil, err
	}
	return &Guard{budget: budgetUSD, spent: spent}, nil
}

// Allow reports whether another model call may be made.
func (g *Guard) Allow() error {
	if g.budget <= 0 || g.spent < g.budget {
		return nil
	}
	return &ErrBudgetExhausted{SpentUSD: g.spent, BudgetUSD: g.budget}
}

// Record adds a completed call's cost, so the ceiling applies within a run and
// not only between runs. Without this a single run could overshoot by its whole
// message batch before the next run noticed.
func (g *Guard) Record(model string, tokensIn, tokensOut int) {
	g.spent += USD(model, tokensIn, tokensOut)
}

// SpentUSD is the day's spend including everything recorded on this guard.
func (g *Guard) SpentUSD() float64 { return g.spent }

// BudgetUSD is the ceiling this guard enforces; zero means unlimited.
func (g *Guard) BudgetUSD() float64 { return g.budget }
