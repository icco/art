package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/icco/art/lib/config"
	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/oauth"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Planner schedules focus blocks inside the rolling 14-day window and never
// inside the in-progress hour. The deterministic planner is the default;
// the optional ADK/Gemini planner wraps the same primitives as tools and
// falls back to deterministic on failure.
type Planner struct {
	Cfg   *config.Config
	DB    *gorm.DB
	OAuth *oauth.Flow

	// mu serializes runs: a manual /replan concurrent with the cron tick
	// must not double-book the same free slots.
	mu sync.Mutex
}

// ReconcileAndRun reconciles calendar drift and then plans, holding the
// run lock across both so a manual /replan and the cron tick can't
// interleave (e.g. both deleting the same conflicted event).
func (p *Planner) ReconcileAndRun(ctx context.Context) (ReconcileSummary, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	sum, err := p.reconcileLocked(ctx)
	if err != nil {
		return sum, err
	}
	return sum, p.runLocked(ctx)
}

// Run executes a single planner pass and records the result as an AgentRun row.
func (p *Planner) Run(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.runLocked(ctx)
}

func (p *Planner) runLocked(ctx context.Context) error {
	model := "deterministic"
	if p.Cfg.LLMEnabled() {
		model = p.Cfg.Vertex.Model + "+sweep"
	}
	run := models.AgentRun{StartedAt: time.Now(), Status: models.AgentRunRunning, Model: model}
	if err := p.DB.WithContext(ctx).Create(&run).Error; err != nil {
		return err
	}

	summary := map[string]any{
		"projects_scheduled": 0,
		"habits_scheduled":   0,
		"tasks_scheduled":    0,
		"errors":             []string{},
	}
	// The planners cooperate: the LLM places blocks with judgment first,
	// then the deterministic pass sweeps the same needs — anything the LLM
	// missed (or a run that died mid-way) gets placed mechanically, and the
	// sweep is what marks unfittable tasks unschedulable. Need computation
	// is net of existing sessions, so the sweep no-ops where the LLM
	// already did the job. An LLM failure is recorded but never blocks the
	// sweep.
	if p.Cfg.LLMEnabled() {
		if err := p.llmPlan(ctx, summary); err != nil {
			appendErr(summary, "llm planner: "+err.Error()+" (deterministic sweep still runs)")
		}
	}
	return p.finish(ctx, run.ID, summary, p.deterministicPlan(ctx, summary))
}

func (p *Planner) finish(ctx context.Context, id string, summary map[string]any, runErr error) error {
	status := models.AgentRunSucceeded
	errStr := ""
	if runErr != nil {
		status = models.AgentRunFailed
		errStr = runErr.Error()
	}
	body, _ := json.Marshal(summary)
	t := time.Now()
	if err := p.DB.WithContext(ctx).Model(&models.AgentRun{}).Where("id = ?", id).Updates(map[string]any{
		"ended_at": &t,
		"status":   string(status),
		"summary":  datatypes.JSON(body),
		"error":    errStr,
	}).Error; err != nil {
		return err
	}
	return runErr
}

func accountForKind(k models.SlotKind) models.AccountKind {
	if k == models.SlotWork {
		return models.AccountWork
	}
	return models.AccountPersonal
}

func focusTitle(src models.SourceKind, name string) string {
	prefix := "Focus"
	if src == models.SourceHabit {
		prefix = "Habit"
	}
	return prefix + ": " + name
}

func focusDescription(src models.SourceKind, id string) string {
	return fmt.Sprintf("Scheduled by Art.\nSource: %s\nID: %s\n", src, id)
}

func startOfWeek(t time.Time, tz *time.Location) time.Time {
	local := t.In(tz)
	wd := int(local.Weekday())
	if wd == 0 {
		wd = 7
	}
	monday := local.AddDate(0, 0, -(wd - 1))
	return time.Date(monday.Year(), monday.Month(), monday.Day(), 0, 0, 0, 0, tz)
}

func habitTargetCount(c models.Cadence, from, weekEnd time.Time) int {
	switch c.Type {
	case "per_week":
		return c.Count
	case "per_day":
		// Count remaining days, a partial day counting as a whole one: a
		// full week is exactly 7, Wednesday-noon-to-Monday is 5.
		days := int(math.Ceil(weekEnd.Sub(from).Hours() / 24))
		if days < 0 {
			days = 0
		}
		return c.Count * days
	default:
		return c.Count
	}
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func appendErr(summary map[string]any, s string) {
	errs, _ := summary["errors"].([]string)
	summary["errors"] = append(errs, s)
}
