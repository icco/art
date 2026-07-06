package agent

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/icco/art/lib/calendar"
	"github.com/icco/art/lib/config"
	"github.com/icco/art/lib/models"
	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/genai"
	"gorm.io/gorm"
)

//go:embed prompt.md
var systemInstruction string

// llmCycle is the per-run state shared between the agent's tools and the
// surrounding Planner code. Tools close over a *llmCycle so they can read
// the DB, write to the agent's calendar, and append per-item errors / counts
// to the summary that gets persisted on the agent_runs row.
//
// ctx is the parent context; agent.ToolContext does not carry one.
// mu guards summary and clients: ADK runs tool calls in parallel goroutines.
type llmCycle struct {
	p       *Planner
	ctx     context.Context
	mu      sync.Mutex
	summary map[string]any
	clients map[models.AccountKind]*calendar.Client
}

func (p *Planner) llmPlan(ctx context.Context, summary map[string]any) error {
	cycle := &llmCycle{p: p, ctx: ctx, summary: summary, clients: map[models.AccountKind]*calendar.Client{}}

	model, err := gemini.NewModel(ctx, config.VertexModel, &genai.ClientConfig{
		Project:  p.Cfg.Vertex.ProjectID,
		Location: p.Cfg.Vertex.Location,
		Backend:  genai.BackendVertexAI,
	})
	if err != nil {
		return fmt.Errorf("gemini model: %w", err)
	}

	tools, err := cycle.tools()
	if err != nil {
		return fmt.Errorf("build tools: %w", err)
	}

	llm, err := llmagent.New(llmagent.Config{
		Name:        "art_planner",
		Description: "Books focus blocks on Google Calendar for projects and habits.",
		Model:       model,
		Instruction: cycle.instruction(),
		Tools:       tools,
	})
	if err != nil {
		return fmt.Errorf("llmagent.New: %w", err)
	}

	r, err := runner.New(runner.Config{
		AppName:           "art",
		Agent:             llm,
		SessionService:    session.InMemoryService(),
		AutoCreateSession: true,
	})
	if err != nil {
		return fmt.Errorf("runner.New: %w", err)
	}

	userID := "owner"
	sessionID := uuid.NewString()
	msg := &genai.Content{
		Role:  "user",
		Parts: []*genai.Part{{Text: "Plan focus blocks for the current week now."}},
	}

	var lastErr error
	for ev, iterErr := range r.Run(ctx, userID, sessionID, msg, adkagent.RunConfig{}) {
		if iterErr != nil {
			lastErr = iterErr
			continue
		}
		if ev != nil && ev.ErrorMessage != "" {
			cycle.addErr("agent: " + ev.ErrorMessage)
		}
	}
	return lastErr
}

func (c *llmCycle) instruction() string {
	now := time.Now().In(c.p.Cfg.Timezone)
	from := PlanningStart(time.Now(), c.p.Cfg.Timezone)
	_, weekEnd := WeekWindow(time.Now(), c.p.Cfg.Timezone)
	return fmt.Sprintf("%s\n\nNow: %s\nPlan window: [%s, %s) in %s.",
		systemInstruction,
		now.Format(time.RFC3339),
		from.In(c.p.Cfg.Timezone).Format(time.RFC3339),
		weekEnd.In(c.p.Cfg.Timezone).Format(time.RFC3339),
		c.p.Cfg.Timezone.String(),
	)
}

// ---- tool args / results ----

type listStateArgs struct{}

type projectInfo struct {
	ID             string  `json:"id"`
	Name           string  `json:"name"`
	Kind           string  `json:"kind"`
	HoursRemaining float64 `json:"hours_remaining"`
	Deadline       string  `json:"deadline,omitempty"`
}

type habitInfo struct {
	ID                string `json:"id"`
	Name              string `json:"name"`
	Kind              string `json:"kind"`
	BlockMinutes      int    `json:"block_minutes"`
	CadenceType       string `json:"cadence_type"`
	CadenceCount      int    `json:"cadence_count"`
	ScheduledThisWeek int    `json:"scheduled_this_week"`
}

type workingHourInfo struct {
	SlotKind    string `json:"slot_kind"`
	DayOfWeek   int    `json:"day_of_week"`
	StartMinute int    `json:"start_minute"`
	EndMinute   int    `json:"end_minute"`
}

type listStateResult struct {
	Projects     []projectInfo     `json:"projects"`
	Habits       []habitInfo       `json:"habits"`
	WorkingHours []workingHourInfo `json:"working_hours"`
}

type findFreeSlotsArgs struct {
	AccountKind string `json:"account_kind" jsonschema:"personal or work"`
	SlotKind    string `json:"slot_kind"    jsonschema:"personal or work"`
	DurationMin int    `json:"duration_min"`
	MaxResults  int    `json:"max_results"`
}

type freeSlot struct {
	StartISO string `json:"start"`
	EndISO   string `json:"end"`
}

type findFreeSlotsResult struct {
	Slots []freeSlot `json:"slots"`
}

type commitFocusBlockArgs struct {
	Source   string `json:"source"    jsonschema:"project or habit"`
	SourceID string `json:"source_id"`
	StartISO string `json:"start"     jsonschema:"RFC3339 start time in UTC"`
	EndISO   string `json:"end"       jsonschema:"RFC3339 end time in UTC"`
}

type commitFocusBlockResult struct {
	SessionID     string `json:"session_id"`
	GoogleEventID string `json:"google_event_id"`
}

// ---- tool implementations ----

func (c *llmCycle) tools() ([]tool.Tool, error) {
	listState, err := functiontool.New[listStateArgs, listStateResult](
		functiontool.Config{
			Name:        "list_state",
			Description: "List active projects, active habits, and working-hour windows.",
		},
		c.listState,
	)
	if err != nil {
		return nil, err
	}
	findSlots, err := functiontool.New[findFreeSlotsArgs, findFreeSlotsResult](
		functiontool.Config{
			Name:        "find_free_slots",
			Description: "Return candidate free time slots that respect working hours and avoid existing events on the chosen account. The window is implicitly [planning_start, week_end).",
		},
		c.findFreeSlots,
	)
	if err != nil {
		return nil, err
	}
	commit, err := functiontool.New[commitFocusBlockArgs, commitFocusBlockResult](
		functiontool.Config{
			Name:        "commit_focus_block",
			Description: "Create a focusTime event on the right calendar (work or personal based on source kind), tagged art_managed=true, and record a sessions row.",
		},
		c.commitFocusBlock,
	)
	if err != nil {
		return nil, err
	}
	return []tool.Tool{listState, findSlots, commit}, nil
}

func (c *llmCycle) listState(_ adkagent.ToolContext, _ listStateArgs) (listStateResult, error) {
	ctx := c.ctx
	var out listStateResult

	var projects []models.Project
	if err := c.p.DB.WithContext(ctx).
		Where("status = ?", models.ProjectActive).
		Order("COALESCE(deadline, now() + interval '365 days') ASC").
		Find(&projects).Error; err != nil {
		return out, err
	}
	scheduled, err := projectScheduledHours(ctx, c.p.DB)
	if err != nil {
		return out, err
	}
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
		out.Projects = append(out.Projects, info)
	}

	weekStart, weekEnd := WeekWindow(time.Now(), c.p.Cfg.Timezone)

	var habits []models.Habit
	if err := c.p.DB.WithContext(ctx).Where("active = ?", true).Find(&habits).Error; err != nil {
		return out, err
	}
	for _, h := range habits {
		var cad models.Cadence
		if err := json.Unmarshal([]byte(h.Cadence), &cad); err != nil {
			c.addErr(fmt.Sprintf("habit %s: bad cadence: %v", h.Name, err))
			continue
		}
		var n int64
		if err := c.p.DB.WithContext(ctx).Model(&models.Session{}).
			Where("source = ? AND source_id = ? AND scheduled_start >= ? AND scheduled_start < ? AND status <> ?",
				models.SourceHabit, h.ID, weekStart, weekEnd, models.SessionSkipped).
			Count(&n).Error; err != nil {
			c.addErr(fmt.Sprintf("habit %s: session count: %v", h.Name, err))
			continue
		}
		out.Habits = append(out.Habits, habitInfo{
			ID:                h.ID,
			Name:              h.Name,
			Kind:              string(h.Kind),
			BlockMinutes:      h.BlockDurationMinutes,
			CadenceType:       cad.Type,
			CadenceCount:      cad.Count,
			ScheduledThisWeek: int(n),
		})
	}

	var hours []models.WorkingHour
	if err := c.p.DB.WithContext(ctx).Order("slot_kind, day_of_week, start_minute").Find(&hours).Error; err != nil {
		return out, err
	}
	for _, h := range hours {
		out.WorkingHours = append(out.WorkingHours, workingHourInfo{
			SlotKind:    string(h.SlotKind),
			DayOfWeek:   h.DayOfWeek,
			StartMinute: h.StartMinute,
			EndMinute:   h.EndMinute,
		})
	}
	return out, nil
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

func (c *llmCycle) findFreeSlots(_ adkagent.ToolContext, args findFreeSlotsArgs) (findFreeSlotsResult, error) {
	ctx := c.ctx
	if !models.AccountKind(args.AccountKind).Valid() {
		return findFreeSlotsResult{}, fmt.Errorf("account_kind must be 'personal' or 'work'")
	}
	if !models.SlotKind(args.SlotKind).Valid() {
		return findFreeSlotsResult{}, fmt.Errorf("slot_kind must be 'personal' or 'work'")
	}
	maxResults := args.MaxResults
	if maxResults <= 0 {
		maxResults = 5
	}
	from := PlanningStart(time.Now(), c.p.Cfg.Timezone)
	_, weekEnd := WeekWindow(time.Now(), c.p.Cfg.Timezone)
	slots, err := FindFreeSlots(ctx, c.p.DB, c.p.Cfg.Timezone,
		models.AccountKind(args.AccountKind), models.SlotKind(args.SlotKind),
		args.DurationMin, from, weekEnd, maxResults)
	if err != nil {
		return findFreeSlotsResult{}, err
	}
	var out findFreeSlotsResult
	for _, s := range slots {
		out.Slots = append(out.Slots, freeSlot{
			StartISO: s.Start.UTC().Format(time.RFC3339),
			EndISO:   s.End.UTC().Format(time.RFC3339),
		})
	}
	return out, nil
}

func (c *llmCycle) commitFocusBlock(_ adkagent.ToolContext, args commitFocusBlockArgs) (commitFocusBlockResult, error) {
	ctx := c.ctx
	source := models.SourceKind(args.Source)
	if !source.Valid() {
		return commitFocusBlockResult{}, fmt.Errorf("source must be 'project' or 'habit'")
	}
	start, err := time.Parse(time.RFC3339, args.StartISO)
	if err != nil {
		return commitFocusBlockResult{}, fmt.Errorf("start: %w", err)
	}
	end, err := time.Parse(time.RFC3339, args.EndISO)
	if err != nil {
		return commitFocusBlockResult{}, fmt.Errorf("end: %w", err)
	}

	// Enforce the same invariants as the deterministic planner. The LLM
	// should respect these via the prompt, but tools are the source of truth.
	if d := end.Sub(start); d < 30*time.Minute || d > 90*time.Minute {
		return commitFocusBlockResult{}, fmt.Errorf("block must be 30-90 minutes, got %s", d)
	}
	planFrom := PlanningStart(time.Now(), c.p.Cfg.Timezone)
	_, weekEnd := WeekWindow(time.Now(), c.p.Cfg.Timezone)
	if start.Before(planFrom) {
		return commitFocusBlockResult{}, fmt.Errorf("start %s is before planning start %s", start, planFrom)
	}
	if end.After(weekEnd) {
		return commitFocusBlockResult{}, fmt.Errorf("end %s is past the current week", end)
	}

	name, kind, err := c.resolveSource(ctx, source, args.SourceID)
	if err != nil {
		return commitFocusBlockResult{}, err
	}

	var hours []models.WorkingHour
	if err := c.p.DB.WithContext(ctx).Where("slot_kind = ?", kind).Find(&hours).Error; err != nil {
		return commitFocusBlockResult{}, err
	}
	if !withinWorkingHours(start, end, hours, c.p.Cfg.Timezone) {
		return commitFocusBlockResult{}, fmt.Errorf("block %s-%s is outside working hours", args.StartISO, args.EndISO)
	}

	acct := accountForKind(kind)
	busy, err := loadBusy(ctx, c.p.DB, acct, start, end)
	if err != nil {
		return commitFocusBlockResult{}, err
	}
	if overlapsAny(start, end, busy) {
		return commitFocusBlockResult{}, fmt.Errorf("block %s-%s overlaps an existing event or planned session", args.StartISO, args.EndISO)
	}

	// A habit gets at most one block per calendar day (config tz): its weekly
	// cadence must spread across days rather than stack onto one.
	if source == models.SourceHabit {
		dayStart := startOfDay(start, c.p.Cfg.Timezone)
		var n int64
		if err := c.p.DB.WithContext(ctx).Model(&models.Session{}).
			Where("source = ? AND source_id = ? AND status <> ? AND scheduled_start >= ? AND scheduled_start < ?",
				models.SourceHabit, args.SourceID, models.SessionSkipped, dayStart, dayStart.AddDate(0, 0, 1)).
			Count(&n).Error; err != nil {
			return commitFocusBlockResult{}, err
		}
		if n > 0 {
			return commitFocusBlockResult{}, fmt.Errorf("habit already has a block on %s; at most one per day", dayStart.Format("2006-01-02"))
		}
	}

	client, err := c.clientFor(ctx, acct)
	if err != nil {
		return commitFocusBlockResult{}, fmt.Errorf("account %s not linked: %w", acct, err)
	}

	calID := client.Account.PrimaryCalendarID
	ev, err := client.CreateFocus(ctx, calendar.FocusBlock{
		CalendarID:  calID,
		EventID:     focusEventID(source, args.SourceID, start, end),
		Start:       start,
		End:         end,
		Summary:     focusTitle(source, name),
		Description: focusDescription(source, args.SourceID),
		Source:      source,
		SourceID:    args.SourceID,
	})
	if err != nil {
		return commitFocusBlockResult{}, err
	}

	sess := models.Session{
		Source:         source,
		SourceID:       args.SourceID,
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
				return commitFocusBlockResult{SessionID: existing.ID, GoogleEventID: ev.Id}, nil
			}
		}
		return commitFocusBlockResult{}, err
	}

	c.recordScheduled(source)
	return commitFocusBlockResult{SessionID: sess.ID, GoogleEventID: ev.Id}, nil
}

func (c *llmCycle) recordScheduled(source models.SourceKind) {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch source {
	case models.SourceProject:
		c.summary["projects_scheduled"] = intVal(c.summary["projects_scheduled"]) + 1
	case models.SourceHabit:
		c.summary["habits_scheduled"] = intVal(c.summary["habits_scheduled"]) + 1
	}
}

func (c *llmCycle) addErr(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	appendErr(c.summary, s)
}

func (c *llmCycle) resolveSource(ctx context.Context, source models.SourceKind, id string) (string, models.SlotKind, error) {
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

func (c *llmCycle) clientFor(ctx context.Context, acct models.AccountKind) (*calendar.Client, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
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
