package agent

import (
	"testing"
	"time"

	"github.com/icco/art/lib/models"
)

// Test fixture: working hours every day 9-17 for both kinds, window
// Mon 2026-06-15 00:00 PT → +14d.
func allHours() []models.WorkingHour {
	var hours []models.WorkingHour
	for d := 0; d < 7; d++ {
		for _, k := range []models.SlotKind{models.SlotWork, models.SlotPersonal} {
			hours = append(hours, models.WorkingHour{SlotKind: k, DayOfWeek: d, StartMinute: 9 * 60, EndMinute: 17 * 60})
		}
	}
	return hours
}

func planWindow(t *testing.T) (time.Time, time.Time, *time.Location) {
	t.Helper()
	tz := mustTZ(t)
	start := time.Date(2026, 6, 15, 0, 0, 0, 0, tz)
	return start, start.Add(14 * 24 * time.Hour), tz
}

func taskItem(id, name string, needMin int, deadline *time.Time) PlanItem {
	return PlanItem{
		Source: models.SourceTask, SourceID: id, Name: name, Kind: models.SlotPersonal,
		NeedMinutes: needMin, MinBlock: 60, MaxBlock: needMin,
		Deadline: deadline, Splittable: needMin >= 120, MustComplete: true,
	}
}

func TestPlaceTaskContiguous(t *testing.T) {
	start, end, tz := planWindow(t)
	res := place([]PlanItem{taskItem("t1", "pack office", 120, nil)}, allHours(), nil, tz, start, end)
	if len(res.Unschedulable) != 0 {
		t.Fatalf("unexpected unschedulable: %+v", res.Unschedulable)
	}
	if len(res.Placements) != 1 {
		t.Fatalf("want 1 contiguous placement, got %d", len(res.Placements))
	}
	p := res.Placements[0]
	if p.End.Sub(p.Start) != 2*time.Hour {
		t.Fatalf("placement length: %v", p.End.Sub(p.Start))
	}
	// Earliest possible: Monday 09:00 PT.
	want := time.Date(2026, 6, 15, 9, 0, 0, 0, tz)
	if !p.Start.Equal(want) {
		t.Fatalf("placement start: got %v want %v", p.Start, want)
	}
}

func TestPlaceTaskSplitsWhenNoContiguousFits(t *testing.T) {
	start, end, tz := planWindow(t)
	// Busy 11:00-17:00 every day for the whole window: only 9-11 free daily.
	var busy []busyRange
	for d := 0; d < 14; d++ {
		dayStart := time.Date(2026, 6, 15+d, 11, 0, 0, 0, tz)
		busy = append(busy, busyRange{start: dayStart, end: dayStart.Add(6 * time.Hour)})
	}
	res := place([]PlanItem{taskItem("t1", "move prep", 240, nil)}, allHours(), busy, tz, start, end)
	if len(res.Unschedulable) != 0 {
		t.Fatalf("unexpected unschedulable: %+v", res.Unschedulable)
	}
	var total time.Duration
	for _, p := range res.Placements {
		if p.End.Sub(p.Start) < time.Hour {
			t.Fatalf("chunk below 1h minimum: %v-%v", p.Start, p.End)
		}
		total += p.End.Sub(p.Start)
	}
	if total != 4*time.Hour {
		t.Fatalf("total scheduled %v, want 4h", total)
	}
	if len(res.Placements) < 2 {
		t.Fatalf("expected a split, got %d placements", len(res.Placements))
	}
}

func TestPlaceTaskRefusesWhenDeadlineTooTight(t *testing.T) {
	start, end, tz := planWindow(t)
	// Deadline Tuesday EOD, but Mon+Tue are fully busy.
	deadline := time.Date(2026, 6, 16, 23, 59, 59, 0, tz)
	var busy []busyRange
	for d := 0; d < 2; d++ {
		dayStart := time.Date(2026, 6, 15+d, 9, 0, 0, 0, tz)
		busy = append(busy, busyRange{start: dayStart, end: dayStart.Add(8 * time.Hour)})
	}
	res := place([]PlanItem{taskItem("t1", "tight", 120, &deadline)}, allHours(), busy, tz, start, end)
	if len(res.Placements) != 0 {
		t.Fatalf("refuse means commit nothing, got %+v", res.Placements)
	}
	if len(res.Unschedulable) != 1 {
		t.Fatalf("want 1 unschedulable, got %d", len(res.Unschedulable))
	}
	// Nearest options ignore the deadline: Wednesday 9:00 is free.
	near := res.Unschedulable[0].NearestSlots
	if len(near) == 0 {
		t.Fatal("expected nearest-slot suggestions")
	}
	wantNear := time.Date(2026, 6, 17, 9, 0, 0, 0, tz)
	if !near[0].Start.Equal(wantNear) {
		t.Fatalf("nearest slot: got %v want %v", near[0].Start, wantNear)
	}
}

func TestPlaceDeadlineOrdering(t *testing.T) {
	start, end, tz := planWindow(t)
	soon := time.Date(2026, 6, 16, 23, 59, 59, 0, tz)
	later := time.Date(2026, 6, 20, 23, 59, 59, 0, tz)
	items := []PlanItem{
		taskItem("nodeadline", "whenever", 60, nil),
		taskItem("later", "later", 60, &later),
		taskItem("soon", "soon", 60, &soon),
	}
	res := place(items, allHours(), nil, tz, start, end)
	if len(res.Placements) != 3 {
		t.Fatalf("want 3 placements, got %d", len(res.Placements))
	}
	if res.Placements[0].Item.SourceID != "soon" {
		t.Fatalf("earliest deadline should be placed first, got %q", res.Placements[0].Item.SourceID)
	}
	if res.Placements[0].Start.After(res.Placements[1].Start) {
		t.Fatal("soonest-deadline item should get the earliest slot")
	}
	if res.Placements[2].Item.SourceID != "nodeadline" {
		t.Fatalf("nil deadline should be placed last, got %q", res.Placements[2].Item.SourceID)
	}
}

func TestPlaceProjectSpreadsAcrossDays(t *testing.T) {
	start, end, tz := planWindow(t)
	item := PlanItem{
		Source: models.SourceProject, SourceID: "p1", Name: "thesis", Kind: models.SlotWork,
		NeedMinutes: 240, MinBlock: 30, MaxBlock: 90, Splittable: true, OnePerDay: true,
	}
	res := place([]PlanItem{item}, allHours(), nil, tz, start, end)
	if len(res.Unschedulable) != 0 {
		t.Fatalf("projects never refuse: %+v", res.Unschedulable)
	}
	var total time.Duration
	days := map[int]int{}
	for _, p := range res.Placements {
		d := p.Start.In(tz).YearDay()
		days[d]++
		if p.End.Sub(p.Start) > 90*time.Minute {
			t.Fatalf("project block > 90m: %v", p.End.Sub(p.Start))
		}
		total += p.End.Sub(p.Start)
	}
	if total != 4*time.Hour {
		t.Fatalf("total %v, want 4h", total)
	}
	for d, n := range days {
		if n > 1 {
			t.Fatalf("day %d has %d blocks; OnePerDay violated", d, n)
		}
	}
}

func TestPlaceHabitWithinWeekBounds(t *testing.T) {
	start, end, tz := planWindow(t)
	weekEnd := start.Add(7 * 24 * time.Hour)
	item := PlanItem{
		Source: models.SourceHabit, SourceID: "h1", Name: "run", Kind: models.SlotPersonal,
		NeedMinutes: 3 * 45, MinBlock: 45, MaxBlock: 45, Splittable: true, OnePerDay: true,
		NotAfter: weekEnd,
	}
	res := place([]PlanItem{item}, allHours(), nil, tz, start, end)
	if len(res.Placements) != 3 {
		t.Fatalf("want 3 habit blocks, got %d", len(res.Placements))
	}
	for _, p := range res.Placements {
		if p.End.After(weekEnd) {
			t.Fatalf("habit block outside its week: %v", p.End)
		}
		if p.End.Sub(p.Start) != 45*time.Minute {
			t.Fatalf("habit block must be exactly 45m, got %v", p.End.Sub(p.Start))
		}
	}
}

func TestPlaceItemsDontOverlap(t *testing.T) {
	start, end, tz := planWindow(t)
	// Narrow hours: only 9-11 daily → forces items to share scarce space.
	var hours []models.WorkingHour
	for d := 0; d < 7; d++ {
		hours = append(hours, models.WorkingHour{SlotKind: models.SlotPersonal, DayOfWeek: d, StartMinute: 9 * 60, EndMinute: 11 * 60})
	}
	items := []PlanItem{
		taskItem("a", "a", 120, nil),
		taskItem("b", "b", 120, nil),
	}
	res := place(items, hours, nil, tz, start, end)
	if len(res.Placements) != 2 {
		t.Fatalf("want 2 placements, got %d", len(res.Placements))
	}
	a, b := res.Placements[0], res.Placements[1]
	if a.Start.Before(b.End) && b.Start.Before(a.End) {
		t.Fatalf("placements overlap: %v-%v and %v-%v", a.Start, a.End, b.Start, b.End)
	}
}

// Idempotency lives in need computation: an item with zero need produces no
// placements.
func TestPlaceZeroNeedIsNoop(t *testing.T) {
	start, end, tz := planWindow(t)
	res := place([]PlanItem{taskItem("t1", "done already", 0, nil)}, allHours(), nil, tz, start, end)
	if len(res.Placements) != 0 || len(res.Unschedulable) != 0 {
		t.Fatalf("zero-need item should be a no-op: %+v", res)
	}
}
