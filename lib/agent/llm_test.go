package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/icco/art/lib/config"
	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/oauth"
	"github.com/icco/art/lib/testdb"
	"gorm.io/datatypes"
	"gorm.io/gorm"
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

func TestListStateProjectHoursFromSessions(t *testing.T) {
	c := newCycle(t)
	pj := &models.Project{Name: "Book", Kind: models.SlotWork, TargetHours: 10, Status: models.ProjectActive}
	if err := c.p.DB.Create(pj).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	sessions := []models.Session{
		// 2h planned this week: counts against the target.
		{Source: models.SourceProject, SourceID: pj.ID, AccountKind: models.AccountWork, CalendarID: "primary",
			ScheduledStart: now, ScheduledEnd: now.Add(2 * time.Hour), Status: models.SessionPlanned},
		// 3h happened last week: target hours are lifetime, so it counts too.
		{Source: models.SourceProject, SourceID: pj.ID, AccountKind: models.AccountWork, CalendarID: "primary",
			ScheduledStart: now.AddDate(0, 0, -7), ScheduledEnd: now.AddDate(0, 0, -7).Add(3 * time.Hour), Status: models.SessionHappened},
		// 1h skipped: returns to the pool.
		{Source: models.SourceProject, SourceID: pj.ID, AccountKind: models.AccountWork, CalendarID: "primary",
			ScheduledStart: now.Add(3 * time.Hour), ScheduledEnd: now.Add(4 * time.Hour), Status: models.SessionSkipped},
	}
	for i := range sessions {
		if err := c.p.DB.Create(&sessions[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	got, err := c.listState(nil, listStateArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Projects) != 1 {
		t.Fatalf("projects: %+v", got.Projects)
	}
	if hr := got.Projects[0].HoursRemaining; hr != 5 {
		t.Fatalf("hours_remaining = %v, want 5 (10 target - 2 planned - 3 happened; skipped excluded)", hr)
	}
}

// ADK executes parallel tool calls from one model response in separate
// goroutines, so the per-run state mutations must be safe under -race.
func TestLLMCycleConcurrentToolState(t *testing.T) {
	c := &llmCycle{summary: map[string]any{
		"projects_scheduled": 0,
		"habits_scheduled":   0,
		"errors":             []string{},
	}}
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.recordScheduled(models.SourceProject)
			c.recordScheduled(models.SourceHabit)
			c.addErr("x")
		}()
	}
	wg.Wait()
	if got := intVal(c.summary["projects_scheduled"]); got != 50 {
		t.Fatalf("projects_scheduled = %d, want 50", got)
	}
	if got := intVal(c.summary["habits_scheduled"]); got != 50 {
		t.Fatalf("habits_scheduled = %d, want 50", got)
	}
	if errs, _ := c.summary["errors"].([]string); len(errs) != 50 {
		t.Fatalf("errors = %d, want 50", len(errs))
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

// The prompt tells the model to respect these invariants, but tools are the
// source of truth: a hallucinated commit must not reach the calendar.
func TestFocusEventID(t *testing.T) {
	t1 := time.Date(2026, 7, 6, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	a := focusEventID(models.SourceProject, "p1", t1, t2)
	if a != focusEventID(models.SourceProject, "p1", t1, t2) {
		t.Fatal("same commit must derive the same event ID")
	}
	if a == focusEventID(models.SourceHabit, "p1", t1, t2) {
		t.Fatal("different sources must derive different IDs")
	}
	// Google requires [a-v0-9], length 5-1024.
	if len(a) < 5 || len(a) > 1024 {
		t.Fatalf("bad length %d", len(a))
	}
	for _, r := range a {
		if (r < 'a' || r > 'v') && (r < '0' || r > '9') {
			t.Fatalf("invalid event-id rune %q in %q", r, a)
		}
	}
}

// Duplicate-key errors must translate to gorm.ErrDuplicatedKey so
// commitFocusBlock can treat a replayed insert as success.
func TestSessionDuplicateKeyTranslated(t *testing.T) {
	db := testdb.Open(t)
	id := "deadbeef01"
	mk := func() models.Session {
		return models.Session{
			Source: models.SourceProject, SourceID: "11111111-1111-1111-1111-111111111111",
			AccountKind: models.AccountWork, CalendarID: "primary", GoogleEventID: &id,
			ScheduledStart: time.Now(), ScheduledEnd: time.Now().Add(time.Hour),
			Status: models.SessionPlanned,
		}
	}
	first := mk()
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	second := mk()
	err := db.Create(&second).Error
	if !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatalf("want gorm.ErrDuplicatedKey, got %v", err)
	}
}

func TestListStateBadCadenceSurfaced(t *testing.T) {
	c := newCycle(t)
	if err := c.p.DB.Create(&models.Habit{
		Name: "Bad", Kind: models.SlotPersonal, BlockDurationMinutes: 30,
		Cadence: datatypes.JSON("[]"), Active: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	got, err := c.listState(nil, listStateArgs{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Habits) != 0 {
		t.Fatalf("habit with malformed cadence should be skipped, got %+v", got.Habits)
	}
	if errs, _ := c.summary["errors"].([]string); len(errs) == 0 {
		t.Fatal("expected cadence error in run summary")
	}
}

func TestCommitFocusBlockEnforcesInvariants(t *testing.T) {
	c := newCycle(t)
	c.ctx = context.Background()
	c.p.OAuth = oauth.NewFlow("cid", "csec", "http://localhost/cb", &oauth.Store{DB: c.p.DB})
	tz := c.p.Cfg.Timezone
	pj := &models.Project{Name: "P", Kind: models.SlotWork, TargetHours: 4, Status: models.ProjectActive}
	if err := c.p.DB.Create(pj).Error; err != nil {
		t.Fatal(err)
	}

	planFrom := PlanningStart(time.Now(), tz)
	_, weekEnd := WeekWindow(time.Now(), tz)
	if weekEnd.Sub(planFrom) < 3*time.Hour {
		t.Skip("too close to week end for deterministic in-window commits")
	}
	iso := func(ts time.Time) string { return ts.UTC().Format(time.RFC3339) }
	commit := func(start, end time.Time) error {
		_, err := c.commitFocusBlock(nil, commitFocusBlockArgs{
			Source: "project", SourceID: pj.ID, StartISO: iso(start), EndISO: iso(end),
		})
		return err
	}
	// Every rejection must name the violated invariant: an unlinked test
	// account makes even un-validated commits error, so err != nil alone
	// proves nothing.
	wantErr := func(start, end time.Time, substr string) {
		t.Helper()
		if err := commit(start, end); err == nil || !contains(err.Error(), substr) {
			t.Errorf("commit %s..%s: got %v, want error containing %q", iso(start), iso(end), err, substr)
		}
	}

	// Duration outside 30-90 minutes is rejected regardless of window.
	wantErr(planFrom, planFrom.Add(10*time.Minute), "minutes")
	wantErr(planFrom, planFrom.Add(3*time.Hour), "minutes")

	// No working-hours window covers the block: rejected.
	wantErr(planFrom, planFrom.Add(time.Hour), "working hours")

	// Open all-day working hours; an existing planned session blocks the range.
	for d := range 7 {
		if err := c.p.DB.Create(&models.WorkingHour{
			SlotKind: models.SlotWork, DayOfWeek: d, StartMinute: 0, EndMinute: 1440,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := c.p.DB.Create(&models.Session{
		Source: models.SourceProject, SourceID: pj.ID, AccountKind: models.AccountWork,
		CalendarID: "primary", ScheduledStart: planFrom, ScheduledEnd: planFrom.Add(time.Hour),
		Status: models.SessionPlanned,
	}).Error; err != nil {
		t.Fatal(err)
	}
	wantErr(planFrom, planFrom.Add(time.Hour), "overlaps")

	// A valid block passes validation and fails only at the unlinked
	// calendar client — i.e. it made it past every invariant check.
	err := commit(planFrom.Add(time.Hour), planFrom.Add(2*time.Hour))
	if err == nil || !contains(err.Error(), "not linked") {
		t.Errorf("valid block should reach the calendar client, got: %v", err)
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
