package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/icco/art/lib/calendar"
	"github.com/icco/art/lib/config"
	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/oauth"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

const PlanningWindow = 7 * 24 * time.Hour

// Planner schedules focus blocks deterministically; ADK-driven mode can
// later wrap the same primitives as tools.
type Planner struct {
	Cfg   *config.Config
	DB    *gorm.DB
	OAuth *oauth.Flow
}

func (p *Planner) Run(ctx context.Context) error {
	run := models.AgentRun{StartedAt: time.Now(), Status: models.AgentRunRunning, Model: p.Cfg.Vertex.Model}
	if err := p.DB.WithContext(ctx).Create(&run).Error; err != nil {
		return err
	}

	summary := map[string]any{
		"projects_scheduled": 0,
		"habits_scheduled":   0,
		"errors":             []string{},
	}
	runErr := p.plan(ctx, summary)
	return p.finish(ctx, run.ID, summary, runErr)
}

func (p *Planner) plan(ctx context.Context, summary map[string]any) error {
	now := time.Now().UTC()
	end := now.Add(PlanningWindow)
	if err := p.planProjects(ctx, now, end, summary); err != nil {
		appendErr(summary, "projects: "+err.Error())
	}
	if err := p.planHabits(ctx, now, summary); err != nil {
		appendErr(summary, "habits: "+err.Error())
	}
	return nil
}

func (p *Planner) planProjects(ctx context.Context, from, to time.Time, summary map[string]any) error {
	var projects []models.Project
	if err := p.DB.WithContext(ctx).
		Where("status = ?", models.ProjectActive).
		Order("COALESCE(deadline, now() + interval '365 days') ASC").
		Find(&projects).Error; err != nil {
		return err
	}

	const maxBlockMin = 90
	const minBlockMin = 30
	for _, pj := range projects {
		remaining := pj.TargetHours - pj.ScheduledHours
		if remaining <= 0 {
			continue
		}
		acct := accountForKind(pj.Kind)
		client, err := calendar.NewClient(ctx, p.OAuth, acct)
		if err != nil {
			appendErr(summary, fmt.Sprintf("project %s: account %s not linked", pj.ID, acct))
			continue
		}

		windowEnd := to
		if pj.Deadline != nil && pj.Deadline.Before(windowEnd) {
			windowEnd = *pj.Deadline
		}

		for remaining > 0 {
			blockMin := min(int(remaining*60), maxBlockMin)
			if blockMin < minBlockMin {
				break
			}
			slots, err := FindFreeSlots(ctx, p.DB, p.Cfg.Timezone, acct, pj.Kind, blockMin, from, windowEnd, 1)
			if err != nil {
				appendErr(summary, fmt.Sprintf("project %s: find slots: %v", pj.ID, err))
				break
			}
			if len(slots) == 0 {
				appendErr(summary, fmt.Sprintf("project %s: no free slot for %d minutes before deadline", pj.ID, blockMin))
				break
			}
			if err := p.commit(ctx, client, models.SourceProject, pj.ID, pj.Name, slots[0]); err != nil {
				appendErr(summary, fmt.Sprintf("project %s: commit: %v", pj.ID, err))
				break
			}
			summary["projects_scheduled"] = summary["projects_scheduled"].(int) + 1
			remaining -= float64(blockMin) / 60
		}

		_ = p.DB.WithContext(ctx).Model(&pj).Updates(map[string]any{
			"scheduled_hours": pj.TargetHours - remaining,
		}).Error
	}
	return nil
}

func (p *Planner) planHabits(ctx context.Context, from time.Time, summary map[string]any) error {
	var habits []models.Habit
	if err := p.DB.WithContext(ctx).Where("active = ?", true).Find(&habits).Error; err != nil {
		return err
	}

	weekStart := startOfWeek(from, p.Cfg.Timezone)
	weekEnd := weekStart.Add(7 * 24 * time.Hour)

	for _, h := range habits {
		var cad models.Cadence
		if err := json.Unmarshal([]byte(h.Cadence), &cad); err != nil {
			appendErr(summary, fmt.Sprintf("habit %s: bad cadence json: %v", h.ID, err))
			continue
		}
		target := habitTargetCount(cad, from, weekEnd)
		if target <= 0 {
			continue
		}
		var existing int64
		if err := p.DB.WithContext(ctx).Model(&models.Session{}).
			Where("source = ? AND source_id = ? AND scheduled_start >= ? AND scheduled_start < ? AND status <> ?",
				models.SourceHabit, h.ID, weekStart, weekEnd, models.SessionSkipped).
			Count(&existing).Error; err != nil {
			appendErr(summary, fmt.Sprintf("habit %s: count: %v", h.ID, err))
			continue
		}
		need := target - int(existing)
		if need <= 0 {
			continue
		}

		acct := accountForKind(h.Kind)
		client, err := calendar.NewClient(ctx, p.OAuth, acct)
		if err != nil {
			appendErr(summary, fmt.Sprintf("habit %s: account %s not linked", h.ID, acct))
			continue
		}

		for range need {
			slots, err := FindFreeSlots(ctx, p.DB, p.Cfg.Timezone, acct, h.Kind, h.BlockDurationMinutes, maxTime(from, weekStart), weekEnd, 1)
			if err != nil {
				appendErr(summary, fmt.Sprintf("habit %s: find slots: %v", h.ID, err))
				break
			}
			if len(slots) == 0 {
				appendErr(summary, fmt.Sprintf("habit %s: no free slot for %d minutes", h.ID, h.BlockDurationMinutes))
				break
			}
			if err := p.commit(ctx, client, models.SourceHabit, h.ID, h.Name, slots[0]); err != nil {
				appendErr(summary, fmt.Sprintf("habit %s: commit: %v", h.ID, err))
				break
			}
			summary["habits_scheduled"] = summary["habits_scheduled"].(int) + 1
		}
	}
	return nil
}

func (p *Planner) commit(ctx context.Context, client *calendar.Client, src models.SourceKind, sourceID, name string, slot Slot) error {
	calID := client.Account.PrimaryCalendarID
	if client.Account.ArtCalendarID != nil && *client.Account.ArtCalendarID != "" {
		calID = *client.Account.ArtCalendarID
	}
	ev, err := client.CreateFocus(ctx, calendar.FocusBlock{
		CalendarID:  calID,
		Start:       slot.Start,
		End:         slot.End,
		Summary:     focusTitle(src, name),
		Description: focusDescription(src, sourceID),
		Source:      src,
		SourceID:    sourceID,
	})
	if err != nil {
		return err
	}
	session := models.Session{
		Source:         src,
		SourceID:       sourceID,
		AccountKind:    client.Account.Kind,
		CalendarID:     calID,
		GoogleEventID:  &ev.Id,
		ScheduledStart: slot.Start,
		ScheduledEnd:   slot.End,
		Status:         models.SessionPlanned,
	}
	return p.DB.WithContext(ctx).Create(&session).Error
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
		days := int(weekEnd.Sub(from).Hours()/24) + 1
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
