package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/icco/art/lib/models"
	"gorm.io/gorm"
)

// PlanItem is one schedulable unit of need: a task, a project's remaining
// hours, or one week's worth of a habit. NeedMinutes is already net of
// existing sessions, so re-running the planner with unchanged state places
// nothing (idempotency lives in need computation, not here).
type PlanItem struct {
	Source   models.SourceKind
	SourceID string
	Name     string
	Kind     models.SlotKind

	NeedMinutes int
	MinBlock    int // minutes; smallest allowed chunk
	MaxBlock    int // minutes; largest allowed chunk

	Deadline  *time.Time // ordering, and a hard bound when MustComplete
	NotBefore time.Time  // zero → window start
	NotAfter  time.Time  // zero → window end

	Splittable   bool // may be broken into multiple chunks
	MustComplete bool // all-or-nothing: place every minute or place none (tasks)
	OnePerDay    bool // spread chunks across days (projects, habits)

	Created time.Time // ordering tiebreak
}

// Placement is one block the planner decided to book.
type Placement struct {
	Item       PlanItem
	Start, End time.Time
}

// UnschedulableItem is a MustComplete item that could not be fully placed
// before its bound. NearestSlots are deadline-ignoring suggestions.
type UnschedulableItem struct {
	Item         PlanItem
	NearestSlots []Slot
}

// PlanResult is what place decided: blocks to book and items that don't fit.
type PlanResult struct {
	Placements    []Placement
	Unschedulable []UnschedulableItem
}

// chunkStep is the granularity of chunk sizes when splitting.
const chunkStep = 15

// place is the pure scheduling core: no DB, no Google. Items are placed
// deadline-first into free space; each placement becomes busy time for
// everything after it.
func place(
	items []PlanItem,
	hours []models.WorkingHour,
	busy []busyRange,
	tz *time.Location,
	windowStart, windowEnd time.Time,
) PlanResult {
	sortPlanItems(items)
	// Local copy: placements become busy for subsequent items.
	busy = append([]busyRange(nil), busy...)

	var res PlanResult
	for _, item := range items {
		if item.NeedMinutes <= 0 {
			continue
		}
		from := windowStart
		if item.NotBefore.After(from) {
			from = item.NotBefore
		}
		bound := windowEnd
		if !item.NotAfter.IsZero() && item.NotAfter.Before(bound) {
			bound = item.NotAfter
		}
		if item.MustComplete && item.Deadline != nil && item.Deadline.Before(bound) {
			bound = *item.Deadline
		}

		kindHours := hoursForKind(hours, item.Kind)
		placed, ok := placeItem(item, kindHours, busy, tz, from, bound)
		if !ok {
			// Refuse-and-report: book nothing, suggest deadline-ignoring options.
			near := findSlots(kindHours, busy, tz, item.NeedMinutes, from, windowEnd, 3)
			res.Unschedulable = append(res.Unschedulable, UnschedulableItem{Item: item, NearestSlots: near})
			continue
		}
		for _, s := range placed {
			busy = append(busy, busyRange{start: s.Start, end: s.End})
			res.Placements = append(res.Placements, Placement{Item: item, Start: s.Start, End: s.End})
		}
	}
	return res
}

// placeItem finds chunks for one item. ok is false only when the item is
// MustComplete and could not be fully covered; partial placements are
// returned (and kept) for best-effort items like projects.
func placeItem(
	item PlanItem,
	hours []models.WorkingHour,
	busy []busyRange,
	tz *time.Location,
	from, bound time.Time,
) ([]Slot, bool) {
	// Prefer a single contiguous block whenever it's allowed to be that big.
	if item.NeedMinutes <= item.MaxBlock || !item.Splittable {
		if s := findSlots(hours, busy, tz, item.NeedMinutes, from, bound, 1); len(s) == 1 {
			return s, true
		}
		if !item.Splittable {
			return nil, !item.MustComplete
		}
	}

	remaining := item.NeedMinutes
	local := append([]busyRange(nil), busy...)
	cursor := from
	var placed []Slot
	for remaining >= item.MinBlock {
		want := min(remaining, item.MaxBlock)
		// Don't strand a remainder smaller than MinBlock.
		if rem := remaining - want; rem > 0 && rem < item.MinBlock {
			want = remaining - item.MinBlock
		}
		slot, size := largestChunk(item, hours, local, tz, want, remaining, cursor, bound)
		if size == 0 {
			break
		}
		placed = append(placed, slot)
		local = append(local, busyRange{start: slot.Start, end: slot.End})
		remaining -= size
		if item.OnePerDay {
			cursor = nextDayStart(slot.Start, tz)
		}
	}
	if remaining > 0 && item.MustComplete {
		return nil, false
	}
	return placed, true
}

// largestChunk tries chunk sizes from want downward (15-minute steps, never
// below MinBlock, never stranding an unplaceable remainder) and returns the
// first slot found.
func largestChunk(
	item PlanItem,
	hours []models.WorkingHour,
	busy []busyRange,
	tz *time.Location,
	want, remaining int,
	from, bound time.Time,
) (Slot, int) {
	for size := want; size >= item.MinBlock; size -= chunkStep {
		if rem := remaining - size; rem > 0 && rem < item.MinBlock {
			continue
		}
		if s := findSlots(hours, busy, tz, size, from, bound, 1); len(s) == 1 {
			return s[0], size
		}
	}
	return Slot{}, 0
}

// sortPlanItems orders by deadline (nil last), then tasks before projects
// before habits, then creation time.
func sortPlanItems(items []PlanItem) {
	rank := map[models.SourceKind]int{models.SourceTask: 0, models.SourceProject: 1, models.SourceHabit: 2}
	sort.SliceStable(items, func(i, j int) bool {
		di, dj := items[i].Deadline, items[j].Deadline
		switch {
		case di != nil && dj == nil:
			return true
		case di == nil && dj != nil:
			return false
		case di != nil && dj != nil && !di.Equal(*dj):
			return di.Before(*dj)
		}
		if ri, rj := rank[items[i].Source], rank[items[j].Source]; ri != rj {
			return ri < rj
		}
		return items[i].Created.Before(items[j].Created)
	})
}

// taskMinBlock is the smallest chunk a split task may produce.
const taskMinBlock = 60

// Project blocks are 30-90 minutes, as established by the LLM prompt.
const (
	projectMinBlock = 30
	projectMaxBlock = 90
)

// deterministicPlan books focus blocks without an LLM: need computation +
// place() + CommitBlock.
func (p *Planner) deterministicPlan(ctx context.Context, summary map[string]any) error {
	return p.deterministicPlanWith(ctx, summary, newBlockWriter(p).CommitBlock)
}

func (p *Planner) deterministicPlanWith(ctx context.Context, summary map[string]any, commit commitFunc) error {
	tz := p.Cfg.Timezone
	windowStart, windowEnd := PlanWindow(time.Now(), tz)

	var hours []models.WorkingHour
	if err := p.DB.WithContext(ctx).Find(&hours).Error; err != nil {
		return fmt.Errorf("working hours: %w", err)
	}
	busy, err := loadBusy(ctx, p.DB, windowStart, windowEnd)
	if err != nil {
		return fmt.Errorf("busy events: %w", err)
	}
	// Future sessions are busy too: blocks booked in an earlier run may not
	// have synced back into the events table yet.
	var sessions []models.Session
	if err := p.DB.WithContext(ctx).
		Where("status IN ? AND scheduled_end > ? AND scheduled_start < ?",
			[]models.SessionStatus{models.SessionPlanned, models.SessionMoved}, windowStart, windowEnd).
		Find(&sessions).Error; err != nil {
		return fmt.Errorf("planned sessions: %w", err)
	}
	for _, s := range sessions {
		busy = append(busy, busyRange{start: s.ScheduledStart, end: s.ScheduledEnd})
	}

	items, err := p.buildPlanItems(ctx, windowStart, windowEnd)
	if err != nil {
		return err
	}
	res := place(items, hours, busy, tz, windowStart, windowEnd)

	taskChunks := map[string]int{}
	for _, pl := range res.Placements {
		if pl.Item.Source == models.SourceTask {
			taskChunks[pl.Item.SourceID]++
		}
	}
	for _, pl := range res.Placements {
		if _, _, err := commit(ctx, pl.Item.Source, pl.Item.SourceID, pl.Start, pl.End); err != nil {
			appendErr(summary, fmt.Sprintf("commit %s %q: %v", pl.Item.Source, pl.Item.Name, err))
			continue
		}
		switch pl.Item.Source {
		case models.SourceProject:
			summary["projects_scheduled"] = intVal(summary["projects_scheduled"]) + 1
		case models.SourceHabit:
			summary["habits_scheduled"] = intVal(summary["habits_scheduled"]) + 1
		case models.SourceTask:
			summary["tasks_scheduled"] = intVal(summary["tasks_scheduled"]) + 1
			taskChunks[pl.Item.SourceID]--
			if taskChunks[pl.Item.SourceID] == 0 {
				if err := p.DB.WithContext(ctx).Model(&models.Task{}).
					Where("id = ?", pl.Item.SourceID).
					Update("status", models.TaskScheduled).Error; err != nil {
					appendErr(summary, "task status: "+err.Error())
				}
			}
		}
	}

	var unsched []map[string]any
	for _, u := range res.Unschedulable {
		if u.Item.Source == models.SourceTask {
			if err := p.DB.WithContext(ctx).Model(&models.Task{}).
				Where("id = ?", u.Item.SourceID).
				Update("status", models.TaskUnschedulable).Error; err != nil {
				appendErr(summary, "task status: "+err.Error())
			}
		}
		var nearest []string
		for _, s := range u.NearestSlots {
			nearest = append(nearest, s.Start.UTC().Format(time.RFC3339))
		}
		unsched = append(unsched, map[string]any{
			"source":       string(u.Item.Source),
			"source_id":    u.Item.SourceID,
			"title":        u.Item.Name,
			"need_minutes": u.Item.NeedMinutes,
			"nearest":      nearest,
		})
	}
	summary["unschedulable"] = unsched
	return nil
}

// buildPlanItems turns tasks, projects, and habits into PlanItems with needs
// already net of existing sessions — the idempotency core: unchanged state
// yields zero need.
func (p *Planner) buildPlanItems(ctx context.Context, windowStart, windowEnd time.Time) ([]PlanItem, error) {
	var items []PlanItem

	var tasks []models.Task
	if err := p.DB.WithContext(ctx).
		Where("status IN ?", []models.TaskStatus{models.TaskPending, models.TaskUnschedulable}).
		Find(&tasks).Error; err != nil {
		return nil, fmt.Errorf("tasks: %w", err)
	}
	for _, t := range tasks {
		covered, err := sessionMinutes(ctx, p.DB, models.SourceTask, t.ID)
		if err != nil {
			return nil, err
		}
		need := t.DurationMinutes - covered
		if need <= 0 {
			// Already fully covered (e.g. a crash between booking and the
			// status update): repair the status instead of double-booking.
			if err := p.DB.WithContext(ctx).Model(&models.Task{}).
				Where("id = ?", t.ID).Update("status", models.TaskScheduled).Error; err != nil {
				return nil, err
			}
			continue
		}
		items = append(items, PlanItem{
			Source: models.SourceTask, SourceID: t.ID, Name: t.Title, Kind: t.Kind,
			NeedMinutes: need, MinBlock: taskMinBlock, MaxBlock: need,
			Deadline:   t.Deadline,
			Splittable: need >= 2*taskMinBlock, MustComplete: true,
			Created: t.CreatedAt,
		})
	}

	var projects []models.Project
	if err := p.DB.WithContext(ctx).
		Where("status = ?", models.ProjectActive).
		Find(&projects).Error; err != nil {
		return nil, fmt.Errorf("projects: %w", err)
	}
	for _, pj := range projects {
		scheduled, err := sessionMinutes(ctx, p.DB, models.SourceProject, pj.ID)
		if err != nil {
			return nil, err
		}
		need := int(pj.TargetHours*60) - scheduled
		if need < projectMinBlock {
			continue
		}
		item := PlanItem{
			Source: models.SourceProject, SourceID: pj.ID, Name: pj.Name, Kind: pj.Kind,
			NeedMinutes: need, MinBlock: projectMinBlock, MaxBlock: projectMaxBlock,
			Deadline:   pj.Deadline,
			Splittable: true, OnePerDay: true,
			Created: pj.CreatedAt,
		}
		if pj.Deadline != nil {
			item.NotAfter = *pj.Deadline
		}
		items = append(items, item)
	}

	var habits []models.Habit
	if err := p.DB.WithContext(ctx).Where("active = ?", true).Find(&habits).Error; err != nil {
		return nil, fmt.Errorf("habits: %w", err)
	}
	tz := p.Cfg.Timezone
	for _, h := range habits {
		var cad models.Cadence
		_ = json.Unmarshal([]byte(h.Cadence), &cad)
		for weekStart := startOfWeek(windowStart, tz); weekStart.Before(windowEnd); weekStart = weekStart.AddDate(0, 0, 7) {
			weekEnd := weekStart.AddDate(0, 0, 7)
			from := maxTime(weekStart, windowStart)
			to := weekEnd
			if windowEnd.Before(to) {
				to = windowEnd
			}
			target := habitTargetCount(cad, from, weekEnd)
			var n int64
			if err := p.DB.WithContext(ctx).Model(&models.Session{}).
				Where("source = ? AND source_id = ? AND scheduled_start >= ? AND scheduled_start < ? AND status <> ?",
					models.SourceHabit, h.ID, weekStart, weekEnd, models.SessionSkipped).
				Count(&n).Error; err != nil {
				return nil, err
			}
			needBlocks := target - int(n)
			if needBlocks <= 0 {
				continue
			}
			items = append(items, PlanItem{
				Source: models.SourceHabit, SourceID: h.ID, Name: h.Name, Kind: h.Kind,
				NeedMinutes: needBlocks * h.BlockDurationMinutes,
				MinBlock:    h.BlockDurationMinutes, MaxBlock: h.BlockDurationMinutes,
				NotBefore: from, NotAfter: to,
				Splittable: true, OnePerDay: cad.Type != "per_day",
				Created: h.CreatedAt,
			})
		}
	}
	return items, nil
}

// sessionMinutes sums non-skipped session minutes for one source. Skipped
// sessions don't count, so a block deleted from the calendar re-opens need.
func sessionMinutes(ctx context.Context, db *gorm.DB, source models.SourceKind, id string) (int, error) {
	var mins float64
	err := db.WithContext(ctx).Model(&models.Session{}).
		Select("COALESCE(SUM(EXTRACT(EPOCH FROM (scheduled_end - scheduled_start)) / 60), 0)").
		Where("source = ? AND source_id = ? AND status <> ?", source, id, models.SessionSkipped).
		Scan(&mins).Error
	return int(mins), err
}

// markTaskScheduledIfCovered flips a task to scheduled once its non-skipped
// sessions cover its duration. Used by the LLM commit path, where blocks
// arrive one tool call at a time.
func markTaskScheduledIfCovered(ctx context.Context, db *gorm.DB, taskID string) error {
	var task models.Task
	if err := db.WithContext(ctx).First(&task, "id = ?", taskID).Error; err != nil {
		return err
	}
	covered, err := sessionMinutes(ctx, db, models.SourceTask, taskID)
	if err != nil {
		return err
	}
	if covered >= task.DurationMinutes && task.Status != models.TaskDone {
		return db.WithContext(ctx).Model(&task).Update("status", models.TaskScheduled).Error
	}
	return nil
}

func hoursForKind(hours []models.WorkingHour, kind models.SlotKind) []models.WorkingHour {
	var out []models.WorkingHour
	for _, h := range hours {
		if h.SlotKind == kind {
			out = append(out, h)
		}
	}
	return out
}

func nextDayStart(t time.Time, tz *time.Location) time.Time {
	local := t.In(tz)
	return time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, tz)
}
