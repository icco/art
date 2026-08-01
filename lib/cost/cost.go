// Package cost prices recorded tokens and enforces a daily ceiling.
//
// art spent $737 in a month unnoticed: the planner recorded 0 tokens, so the
// cost showed up only in the billing console.
package cost

import (
	"context"
	"fmt"
	"time"

	"github.com/icco/art/lib/models"
	"gorm.io/gorm"
)

// rate is the price per million tokens.
type rate struct {
	InPerMillion  float64
	OutPerMillion float64
}

// rates holds Vertex AI list prices. An estimate: ignoring tiering and caching
// errs high, so the guard trips early rather than late.
var rates = map[string]rate{
	"gemini-2.5-flash": {InPerMillion: 0.30, OutPerMillion: 2.50},
	"gemini-2.5-pro":   {InPerMillion: 1.25, OutPerMillion: 10.00},
}

// unknownModelRate prices an unlisted model at the dearest rate, never free.
var unknownModelRate = rate{InPerMillion: 1.25, OutPerMillion: 10.00}

func rateFor(model string) (rate, bool) {
	r, ok := rates[model]
	if !ok {
		return unknownModelRate, false
	}
	return r, true
}

func usd(model string, tokensIn, tokensOut int) float64 {
	r, _ := rateFor(model)
	return float64(tokensIn)/1e6*r.InPerMillion + float64(tokensOut)/1e6*r.OutPerMillion
}

// SpentToday sums priced usage since midnight in tz; an empty model is free.
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
		total += usd(r.Model, int(r.TokensIn), int(r.TokensOut))
	}
	return total, nil
}

// ErrBudgetExhausted means today's ceiling is reached; make no further calls.
type ErrBudgetExhausted struct {
	SpentUSD  float64
	BudgetUSD float64
}

func (e *ErrBudgetExhausted) Error() string {
	return fmt.Sprintf("daily LLM budget exhausted: $%.4f spent of $%.2f", e.SpentUSD, e.BudgetUSD)
}

// Guard caps daily spend: Allow before each call, Record after. A budget of
// zero or less means unlimited, so a bad setting can't stop triage forever.
type Guard struct {
	budget float64
	spent  float64
}

// NewGuard reads today's spend and returns a guard for the rest of the day.
func NewGuard(ctx context.Context, db *gorm.DB, tz *time.Location, budgetUSD float64) (*Guard, error) {
	spent, err := SpentToday(ctx, db, tz)
	if err != nil {
		return nil, err
	}
	return &Guard{budget: budgetUSD, spent: spent}, nil
}

// Allow reports whether another model call is within budget.
func (g *Guard) Allow() error {
	if g.budget <= 0 || g.spent < g.budget {
		return nil
	}
	return &ErrBudgetExhausted{SpentUSD: g.spent, BudgetUSD: g.budget}
}

// Record applies cost within a run, so one run can't overshoot by its batch.
func (g *Guard) Record(model string, tokensIn, tokensOut int) {
	g.spent += usd(model, tokensIn, tokensOut)
}

// SpentUSD is the day's spend so far.
func (g *Guard) SpentUSD() float64 { return g.spent }

// BudgetUSD is the ceiling; zero means unlimited.
func (g *Guard) BudgetUSD() float64 { return g.budget }
