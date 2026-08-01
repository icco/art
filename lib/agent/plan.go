package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/icco/art/lib/calendar"
	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/settings"
	"gorm.io/gorm"
)

// slotOversample requests spare candidates per block: some get skipped.
const slotOversample = 4

// cycle is the per-run state: settings snapshot, lazy calendar clients, summary.
type cycle struct {
	p       *Planner
	vals    settings.Values
	summary map[string]any
	clients map[models.AccountKind]*calendar.Client
}

// plan books projects deadline-ascending, then habits one per day.
func (c *cycle) plan(ctx context.Context) error {
	projects, habits, err := c.loadState(ctx)
	if err != nil {
		return err
	}
	for _, pj := range projects {
		c.fillProject(ctx, pj)
	}
	for _, h := range habits {
		c.fillHabit(ctx, h)
	}
	return nil
}

// fillProject books until target hours are met or nothing fits.
func (c *cycle) fillProject(ctx context.Context, pj projectInfo) {
	minHours := float64(c.vals.FocusBlockMinMinutes) / 60
	maxHours := float64(c.vals.FocusBlockMaxMinutes) / 60
	remaining := pj.HoursRemaining
	kind := models.SlotKind(pj.Kind)
	if !kind.Valid() {
		c.addErr(fmt.Sprintf("project %s: invalid kind %q", pj.Name, pj.Kind))
		return
	}
	acct := accountForKind(kind)

	var deadline time.Time
	if pj.Deadline != "" {
		d, err := time.Parse(time.RFC3339, pj.Deadline)
		if err != nil {
			c.addErr(fmt.Sprintf("project %s: bad deadline %q: %v", pj.Name, pj.Deadline, err))
			return
		}
		deadline = d
	}

	for remaining >= minHours {
		minutes := int(math.Round(math.Min(remaining, maxHours) * 60))
		slots, err := c.freeSlots(ctx, acct, kind, minutes, slotOversample)
		if err != nil {
			c.addErr(fmt.Sprintf("project %s: find slots: %v", pj.Name, err))
			return
		}
		booked := false
		for _, s := range slots {
			if !deadline.IsZero() && s.End.After(deadline) {
				continue
			}
			if err := c.commitFocus(ctx, models.SourceProject, pj.ID, s.Start, s.End); err != nil {
				c.addErr(fmt.Sprintf("project %s: %v", pj.Name, err))
				continue
			}
			remaining -= s.End.Sub(s.Start).Hours()
			booked = true
			break
		}
		if !booked {
			return
		}
	}
}

// fillHabit books the habit's shortfall for the window, at most one per day.
func (c *cycle) fillHabit(ctx context.Context, h habitInfo) {
	need := h.TargetInWindow - h.ScheduledInWindow
	if need <= 0 {
		return
	}
	kind := models.SlotKind(h.Kind)
	if !kind.Valid() {
		c.addErr(fmt.Sprintf("habit %s: invalid kind %q", h.Name, h.Kind))
		return
	}
	// commitFocus would reject every slot; say so once, not per candidate.
	if h.BlockMinutes < c.vals.FocusBlockMinMinutes || h.BlockMinutes > c.vals.FocusBlockMaxMinutes {
		c.addErr(fmt.Sprintf("habit %s: block %d min is outside the allowed %d-%d",
			h.Name, h.BlockMinutes, c.vals.FocusBlockMinMinutes, c.vals.FocusBlockMaxMinutes))
		return
	}

	slots, err := c.freeSlots(ctx, accountForKind(kind), kind, h.BlockMinutes, need*slotOversample)
	if err != nil {
		c.addErr(fmt.Sprintf("habit %s: find slots: %v", h.Name, err))
		return
	}
	used := map[string]bool{}
	for _, s := range slots {
		if need == 0 {
			break
		}
		day := s.Start.In(c.p.Cfg.Timezone).Format(time.DateOnly)
		if used[day] {
			continue
		}
		if err := c.commitFocus(ctx, models.SourceHabit, h.ID, s.Start, s.End); err != nil {
			c.addErr(fmt.Sprintf("habit %s: %v", h.Name, err))
			continue
		}
		used[day] = true
		need--
	}
}

func (c *cycle) planWindow() (time.Time, time.Time) {
	return PlanWindow(time.Now(), c.p.Cfg.Timezone, c.vals.PlanHorizon())
}

func (c *cycle) freeSlots(ctx context.Context, acct models.AccountKind, kind models.SlotKind, durationMin, maxSlots int) ([]Slot, error) {
	from, end := c.planWindow()
	return FindFreeSlots(ctx, c.p.DB, c.p.Cfg.Timezone, acct, kind, durationMin, from, end, maxSlots, c.vals.SoftTitles())
}

type projectInfo struct {
	ID             string
	Name           string
	Kind           string
	HoursRemaining float64
	Deadline       string
}

type habitInfo struct {
	ID                string
	Name              string
	Kind              string
	BlockMinutes      int
	TargetInWindow    int
	ScheduledInWindow int
}

// loadState reads active projects (deadline-ascending) and habits with their
// shortfall. Working hours aren't returned: the callers query them directly.
func (c *cycle) loadState(ctx context.Context) ([]projectInfo, []habitInfo, error) {
	var projects []models.Project
	if err := c.p.DB.WithContext(ctx).
		Where("status = ?", models.ProjectActive).
		Order("COALESCE(deadline, now() + interval '365 days') ASC").
		Find(&projects).Error; err != nil {
		return nil, nil, err
	}
	scheduled, err := projectScheduledHours(ctx, c.p.DB)
	if err != nil {
		return nil, nil, err
	}
	var outProjects []projectInfo
	for _, pj := range projects {
		info := projectInfo{
			ID:             pj.ID,
			Name:           pj.Name,
			Kind:           string(pj.Kind),
			HoursRemaining: pj.TargetHours - scheduled[pj.ID],
		}
		if pj.Deadline != nil {
			info.Deadline = pj.Deadline.Format(time.RFC3339)
		}
		outProjects = append(outProjects, info)
	}

	windowStart, windowEnd := c.planWindow()

	var habits []models.Habit
	if err := c.p.DB.WithContext(ctx).Where("active = ?", true).Find(&habits).Error; err != nil {
		return nil, nil, err
	}
	var outHabits []habitInfo
	for _, h := range habits {
		var cad models.Cadence
		if err := json.Unmarshal([]byte(h.Cadence), &cad); err != nil {
			c.addErr(fmt.Sprintf("habit %s: bad cadence: %v", h.Name, err))
			continue
		}
		var n int64
		if err := c.p.DB.WithContext(ctx).Model(&models.Session{}).
			Where("source = ? AND source_id = ? AND scheduled_start >= ? AND scheduled_start < ? AND status <> ?",
				models.SourceHabit, h.ID, windowStart, windowEnd, models.SessionSkipped).
			Count(&n).Error; err != nil {
			c.addErr(fmt.Sprintf("habit %s: session count: %v", h.Name, err))
			continue
		}
		outHabits = append(outHabits, habitInfo{
			ID:                h.ID,
			Name:              h.Name,
			Kind:              string(h.Kind),
			BlockMinutes:      h.BlockDurationMinutes,
			TargetInWindow:    habitTargetCount(cad, windowStart, windowEnd),
			ScheduledInWindow: int(n),
		})
	}
	return outProjects, outHabits, nil
}

// focusEventID derives a stable Google event ID so retries converge on one event.
func focusEventID(source models.SourceKind, sourceID string, start, end time.Time) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s|%s|%d|%d", source, sourceID, start.Unix(), end.Unix()))
	return hex.EncodeToString(sum[:16])
}

// projectScheduledHours sums non-skipped session durations per project.
func projectScheduledHours(ctx context.Context, db *gorm.DB) (map[string]float64, error) {
	var rows []struct {
		SourceID string
		Hours    float64
	}
	if err := db.WithContext(ctx).Model(&models.Session{}).
		Select("source_id, SUM(EXTRACT(EPOCH FROM (scheduled_end - scheduled_start))) / 3600 AS hours").
		Where("source = ? AND status <> ?", models.SourceProject, models.SessionSkipped).
		Group("source_id").
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]float64, len(rows))
	for _, r := range rows {
		out[r.SourceID] = r.Hours
	}
	return out, nil
}

// commitFocus creates the calendar event and session row for one block.
//
// The checks date from when an LLM chose these times and are all kept: a
// rejection now means a real race with a human calendar edit.
func (c *cycle) commitFocus(ctx context.Context, source models.SourceKind, sourceID string, start, end time.Time) error {
	if !source.Valid() {
		return fmt.Errorf("source must be 'project' or 'habit'")
	}

	lo := time.Duration(c.vals.FocusBlockMinMinutes) * time.Minute
	hi := time.Duration(c.vals.FocusBlockMaxMinutes) * time.Minute
	if d := end.Sub(start); d < lo || d > hi {
		return fmt.Errorf("block must be %d-%d minutes, got %s",
			c.vals.FocusBlockMinMinutes, c.vals.FocusBlockMaxMinutes, d)
	}
	planFrom, windowEnd := c.planWindow()
	if start.Before(planFrom) {
		return fmt.Errorf("start %s is before planning start %s", start, planFrom)
	}
	if end.After(windowEnd) {
		return fmt.Errorf("end %s is past the plan window end %s", end, windowEnd)
	}

	name, kind, err := c.resolveSource(ctx, source, sourceID)
	if err != nil {
		return err
	}

	var hours []models.WorkingHour
	if err := c.p.DB.WithContext(ctx).Where("slot_kind = ?", kind).Find(&hours).Error; err != nil {
		return err
	}
	if !withinWorkingHours(start, end, hours, c.p.Cfg.Timezone) {
		return fmt.Errorf("block %s-%s is outside working hours", start.Format(time.RFC3339), end.Format(time.RFC3339))
	}

	acct := accountForKind(kind)
	busy, err := loadBusy(ctx, c.p.DB, acct, start, end, c.vals.SoftTitles())
	if err != nil {
		return err
	}
	// Placeholders are schedulable-over; real events and sessions are not.
	if overlapsHard(start, end, busy) {
		return fmt.Errorf("block %s-%s overlaps an existing event or planned session",
			start.Format(time.RFC3339), end.Format(time.RFC3339))
	}

	// At most one block per habit per calendar day, so cadence spreads out.
	if source == models.SourceHabit {
		dayStart := startOfDay(start, c.p.Cfg.Timezone)
		var n int64
		if err := c.p.DB.WithContext(ctx).Model(&models.Session{}).
			Where("source = ? AND source_id = ? AND status <> ? AND scheduled_start >= ? AND scheduled_start < ?",
				models.SourceHabit, sourceID, models.SessionSkipped, dayStart, dayStart.AddDate(0, 0, 1)).
			Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("habit already has a block on %s; at most one per day", dayStart.Format(time.DateOnly))
		}
	}

	client, err := c.clientFor(ctx, acct)
	if err != nil {
		return fmt.Errorf("account %s not linked: %w", acct, err)
	}

	calID := client.Account.PrimaryCalendarID
	ev, err := client.CreateFocus(ctx, calendar.FocusBlock{
		CalendarID:  calID,
		EventID:     focusEventID(source, sourceID, start, end),
		Start:       start,
		End:         end,
		Summary:     focusTitle(source, name),
		Description: focusDescription(source, sourceID),
		Source:      source,
		SourceID:    sourceID,
	})
	if err != nil {
		return err
	}

	sess := models.Session{
		Source:         source,
		SourceID:       sourceID,
		AccountKind:    client.Account.Kind,
		CalendarID:     calID,
		GoogleEventID:  &ev.Id,
		ScheduledStart: start,
		ScheduledEnd:   end,
		Status:         models.SessionPlanned,
	}
	if err := c.p.DB.WithContext(ctx).Create(&sess).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			var existing models.Session
			if lookupErr := c.p.DB.WithContext(ctx).First(&existing, "google_event_id = ?", ev.Id).Error; lookupErr == nil {
				return nil
			}
		}
		return err
	}

	c.recordScheduled(source)
	return nil
}

func (c *cycle) recordScheduled(source models.SourceKind) {
	switch source {
	case models.SourceProject:
		c.summary["projects_scheduled"] = intVal(c.summary["projects_scheduled"]) + 1
	case models.SourceHabit:
		c.summary["habits_scheduled"] = intVal(c.summary["habits_scheduled"]) + 1
	}
}

func (c *cycle) addErr(s string) {
	appendErr(c.summary, s)
}

func (c *cycle) resolveSource(ctx context.Context, source models.SourceKind, id string) (string, models.SlotKind, error) {
	switch source {
	case models.SourceProject:
		var pj models.Project
		if err := c.p.DB.WithContext(ctx).First(&pj, "id = ?", id).Error; err != nil {
			return "", "", fmt.Errorf("project %s: %w", id, err)
		}
		return pj.Name, pj.Kind, nil
	case models.SourceHabit:
		var h models.Habit
		if err := c.p.DB.WithContext(ctx).First(&h, "id = ?", id).Error; err != nil {
			return "", "", fmt.Errorf("habit %s: %w", id, err)
		}
		return h.Name, h.Kind, nil
	}
	return "", "", errors.New("unknown source kind")
}

func (c *cycle) clientFor(ctx context.Context, acct models.AccountKind) (*calendar.Client, error) {
	if cl, ok := c.clients[acct]; ok {
		return cl, nil
	}
	cl, err := calendar.NewClient(ctx, c.p.OAuth, acct)
	if err != nil {
		return nil, err
	}
	c.clients[acct] = cl
	return cl, nil
}

func intVal(v any) int {
	if n, ok := v.(int); ok {
		return n
	}
	return 0
}
