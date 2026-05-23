package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/icco/art/lib/config"
	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/testdb"
	"gorm.io/datatypes"
)

func newCycle(t *testing.T) *llmCycle {
	t.Helper()
	db := testdb.Open(t)
	tz, _ := time.LoadLocation("America/Los_Angeles")
	cfg := &config.Config{Timezone: tz}
	p := &Planner{Cfg: cfg, DB: db}
	return &llmCycle{p: p, summary: map[string]any{
		"projects_scheduled": 0,
		"habits_scheduled":   0,
		"errors":             []string{},
	}}
}

func TestListStateEmpty(t *testing.T) {
	c := newCycle(t)
	got, err := c.listState(nil, listStateArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Projects) != 0 || len(got.Habits) != 0 || len(got.WorkingHours) != 0 {
		t.Fatalf("expected empty state, got %+v", got)
	}
}

func TestListStateSeeded(t *testing.T) {
	c := newCycle(t)
	deadline := time.Now().Add(3 * 24 * time.Hour)
	if err := c.p.DB.Create(&models.Project{
		Name:        "Design X",
		Kind:        models.SlotWork,
		TargetHours: 4,
		Deadline:    &deadline,
		Status:      models.ProjectActive,
	}).Error; err != nil {
		t.Fatal(err)
	}
	cad, _ := json.Marshal(models.Cadence{Type: "per_week", Count: 3})
	if err := c.p.DB.Create(&models.Habit{
		Name: "Walk", Kind: models.SlotPersonal, BlockDurationMinutes: 30,
		Cadence: datatypes.JSON(cad), Active: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := c.p.DB.Create(&models.WorkingHour{
		SlotKind: models.SlotWork, DayOfWeek: 1, StartMinute: 540, EndMinute: 1080,
	}).Error; err != nil {
		t.Fatal(err)
	}
	got, err := c.listState(nil, listStateArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Projects) != 1 || got.Projects[0].Name != "Design X" {
		t.Fatalf("projects: %+v", got.Projects)
	}
	if len(got.Habits) != 1 || got.Habits[0].CadenceCount != 3 {
		t.Fatalf("habits: %+v", got.Habits)
	}
	if len(got.WorkingHours) != 1 || got.WorkingHours[0].DayOfWeek != 1 {
		t.Fatalf("working hours: %+v", got.WorkingHours)
	}
}

func TestFindFreeSlotsValidation(t *testing.T) {
	c := newCycle(t)
	if _, err := c.findFreeSlots(nil, findFreeSlotsArgs{AccountKind: "bad", SlotKind: "work", DurationMin: 30}); err == nil {
		t.Fatal("expected error on bad account_kind")
	}
	if _, err := c.findFreeSlots(nil, findFreeSlotsArgs{AccountKind: "work", SlotKind: "bad", DurationMin: 30}); err == nil {
		t.Fatal("expected error on bad slot_kind")
	}
}

func TestCommitFocusBlockValidation(t *testing.T) {
	c := newCycle(t)
	ctx := context.Background()
	_ = ctx
	if _, err := c.commitFocusBlock(nil, commitFocusBlockArgs{Source: "wrong"}); err == nil {
		t.Fatal("expected error on bad source")
	}
	now := time.Now().UTC()
	if _, err := c.commitFocusBlock(nil, commitFocusBlockArgs{
		Source: "project", SourceID: "x", StartISO: "not-a-time", EndISO: now.Format(time.RFC3339),
	}); err == nil {
		t.Fatal("expected error on bad start")
	}
	if _, err := c.commitFocusBlock(nil, commitFocusBlockArgs{
		Source: "project", SourceID: "x", StartISO: now.Format(time.RFC3339), EndISO: "not-a-time",
	}); err == nil {
		t.Fatal("expected error on bad end")
	}
	// Start before planning-start (current hour) → rejected.
	if _, err := c.commitFocusBlock(nil, commitFocusBlockArgs{
		Source: "project", SourceID: "x",
		StartISO: now.Add(-24 * time.Hour).Format(time.RFC3339),
		EndISO:   now.Add(-23 * time.Hour).Format(time.RFC3339),
	}); err == nil {
		t.Fatal("expected error when start is before planning_start")
	}
}

func TestInstruction(t *testing.T) {
	c := newCycle(t)
	got := c.instruction()
	if got == "" || len(got) < 50 {
		t.Fatalf("instruction looks empty: %q", got)
	}
	// Should include the planning window phrase from the prompt.
	if !contains(got, "Plan window:") {
		t.Fatalf("instruction missing plan window: %q", got)
	}
}

func TestIntVal(t *testing.T) {
	if intVal(7) != 7 {
		t.Fatal("intVal(7)")
	}
	if intVal("nope") != 0 {
		t.Fatal("intVal non-int should return 0")
	}
	if intVal(nil) != 0 {
		t.Fatal("intVal nil should return 0")
	}
}

func TestToolsRegistered(t *testing.T) {
	c := newCycle(t)
	tools, err := c.tools()
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(tools))
	}
	names := map[string]bool{}
	for _, tl := range tools {
		names[tl.Name()] = true
	}
	for _, want := range []string{"list_state", "find_free_slots", "commit_focus_block"} {
		if !names[want] {
			t.Errorf("missing tool %q (have %v)", want, names)
		}
	}
}

func TestResolveSource(t *testing.T) {
	c := newCycle(t)
	pj := &models.Project{Name: "P1", Kind: models.SlotWork, TargetHours: 1, Status: models.ProjectActive}
	if err := c.p.DB.Create(pj).Error; err != nil {
		t.Fatal(err)
	}
	name, kind, err := c.resolveSource(context.Background(), models.SourceProject, pj.ID)
	if err != nil || name != "P1" || kind != models.SlotWork {
		t.Fatalf("project resolve: %v %s %s", err, name, kind)
	}

	cad, _ := json.Marshal(models.Cadence{Type: "per_week", Count: 1})
	h := &models.Habit{Name: "H1", Kind: models.SlotPersonal, BlockDurationMinutes: 20, Cadence: datatypes.JSON(cad), Active: true}
	if err := c.p.DB.Create(h).Error; err != nil {
		t.Fatal(err)
	}
	name, kind, err = c.resolveSource(context.Background(), models.SourceHabit, h.ID)
	if err != nil || name != "H1" || kind != models.SlotPersonal {
		t.Fatalf("habit resolve: %v %s %s", err, name, kind)
	}

	if _, _, err := c.resolveSource(context.Background(), models.SourceProject, "nonexistent"); err == nil {
		t.Fatal("expected error for missing project")
	}
}

func TestFindFreeSlotsTool(t *testing.T) {
	c := newCycle(t)
	// Working hours: every day 0-1440 (open all day) so the search has room.
	for d := range 7 {
		if err := c.p.DB.Create(&models.WorkingHour{
			SlotKind: models.SlotWork, DayOfWeek: d, StartMinute: 0, EndMinute: 1440,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	got, err := c.findFreeSlots(nil, findFreeSlotsArgs{
		AccountKind: "work", SlotKind: "work", DurationMin: 60, MaxResults: 3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Slots) == 0 {
		t.Fatal("expected at least one free slot")
	}
}
