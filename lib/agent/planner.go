package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/icco/art/lib/config"
	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/oauth"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Planner schedules focus blocks over a rolling 30-day window (PlanHorizon)
// and never inside the in-progress hour. ADK orchestration wraps the same
// primitives as tools.
type Planner struct {
	Cfg   *config.Config
	DB    *gorm.DB
	OAuth *oauth.Flow
}

// Run executes a single planner pass and records the result as an AgentRun row.
func (p *Planner) Run(ctx context.Context) error {
	run := models.AgentRun{Kind: models.AgentRunPlanner, StartedAt: time.Now(), Status: models.AgentRunRunning, Model: config.VertexModel}
	if err := p.DB.WithContext(ctx).Create(&run).Error; err != nil {
		return err
	}

	summary := map[string]any{
		"projects_scheduled": 0,
		"habits_scheduled":   0,
		"errors":             []string{},
	}
	runErr := p.llmPlan(ctx, summary)
	return p.finish(ctx, run.ID, summary, runErr)
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
	// Record the outcome even if ctx timed out, so runs never stay "running".
	if err := p.DB.WithContext(context.WithoutCancel(ctx)).Model(&models.AgentRun{}).Where("id = ?", id).Updates(map[string]any{
		"ended_at": &t,
		"status":   string(status),
		"summary":  datatypes.JSON(body),
		"error":    errStr,
	}).Error; err != nil {
		return errors.Join(runErr, err)
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

func startOfDay(t time.Time, tz *time.Location) time.Time {
	local := t.In(tz)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, tz)
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

// habitTargetCount is how many blocks a habit wants over [from, to). A weekly
// cadence is scaled across the window (rounded to nearest), so a 3×/week habit
// targets ~13 blocks over a 30-day plan; a daily cadence gets one per day.
func habitTargetCount(c models.Cadence, from, to time.Time) int {
	switch c.Type {
	case "per_week":
		weeks := max(to.Sub(from).Hours()/(24*7), 0)
		return int(math.Round(float64(c.Count) * weeks))
	case "per_day":
		days := max(int(to.Sub(from).Hours()/24)+1, 0)
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
