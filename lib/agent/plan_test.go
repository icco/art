package agent

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/icco/art/lib/config"
	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/oauth"
	"github.com/icco/art/lib/settings"
	"github.com/icco/art/lib/testdb"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

func newCycle(t *testing.T) *cycle {
	t.Helper()
	db := testdb.Open(t)
	tz, _ := time.LoadLocation("America/Los_Angeles")
	cfg := &config.Config{Timezone: tz}
	p := &Planner{Cfg: cfg, DB: db, Settings: settings.New(db, nil)}
	return &cycle{p: p, vals: settings.DefaultsFrom(nil), summary: map[string]any{
		"projects_scheduled": 0,
		"habits_scheduled":   0,
		"errors":             []string{},
	}}
}

// openAllHours makes every day fully available for slotKind, so tests can
// isolate the invariant under test from working-hours filtering.
func openAllHours(t *testing.T, c *cycle, kind models.SlotKind) {
	t.Helper()
	for d := range 7 {
		if err := c.p.DB.Create(&models.WorkingHour{
			SlotKind: kind, DayOfWeek: d, StartMinute: 0, EndMinute: 1440,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoadStateEmpty(t *testing.T) {
	c := newCycle(t)
	projects, habits, err := c.loadState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 || len(habits) != 0 {
		t.Fatalf("expected empty state, got %+v / %+v", projects, habits)
	}
}

func TestLoadStateSeeded(t *testing.T) {
	c := newCycle(t)
	deadline := time.Now().Add(3 * 24 * time.Hour)
	if err := c.p.DB.Create(&models.Project{
		Name: "Design X", Kind: models.SlotWork, TargetHours: 4,
		Deadline: &deadline, Status: models.ProjectActive,
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

	projects, habits, err := c.loadState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].Name != "Design X" {
		t.Fatalf("projects = %+v", projects)
	}
	if projects[0].HoursRemaining != 4 {
		t.Errorf("hours remaining = %v, want 4", projects[0].HoursRemaining)
	}
	if projects[0].Deadline == "" {
		t.Error("deadline should be rendered")
	}
	if len(habits) != 1 || habits[0].Name != "Walk" {
		t.Fatalf("habits = %+v", habits)
	}
	// 3×/week scaled across the default 30-day horizon.
	if habits[0].TargetInWindow < 10 {
		t.Errorf("target in window = %d, want the weekly cadence scaled up", habits[0].TargetInWindow)
	}
}

func TestLoadStateProjectHoursFromSessions(t *testing.T) {
	c := newCycle(t)
	pj := &models.Project{Name: "P", Kind: models.SlotWork, TargetHours: 4, Status: models.ProjectActive}
	if err := c.p.DB.Create(pj).Error; err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(48 * time.Hour)
	if err := c.p.DB.Create(&models.Session{
		Source: models.SourceProject, SourceID: pj.ID, AccountKind: models.AccountWork,
		CalendarID: "primary", ScheduledStart: start, ScheduledEnd: start.Add(90 * time.Minute),
		Status: models.SessionPlanned,
	}).Error; err != nil {
		t.Fatal(err)
	}

	projects, _, err := c.loadState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 {
		t.Fatalf("projects = %+v", projects)
	}
	if got := projects[0].HoursRemaining; got < 2.4 || got > 2.6 {
		t.Errorf("hours remaining = %v, want ~2.5 (4 target less 1.5 scheduled)", got)
	}
}

func TestLoadStateBadCadenceSurfaced(t *testing.T) {
	c := newCycle(t)
	if err := c.p.DB.Create(&models.Habit{
		Name: "Bad", Kind: models.SlotPersonal, BlockDurationMinutes: 30,
		Cadence: datatypes.JSON("[]"), Active: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	_, habits, err := c.loadState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(habits) != 0 {
		t.Fatalf("habit with malformed cadence should be skipped, got %+v", habits)
	}
	if errs, _ := c.summary["errors"].([]string); len(errs) == 0 {
		t.Fatal("expected cadence error in run summary")
	}
}

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
	if err := db.Create(&second).Error; !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatalf("want gorm.ErrDuplicatedKey, got %v", err)
	}
}

func TestCommitFocusRejectsBadSource(t *testing.T) {
	c := newCycle(t)
	now := time.Now().UTC()
	if err := c.commitFocus(context.Background(), models.SourceKind("wrong"), "x", now, now.Add(time.Hour)); err == nil {
		t.Fatal("expected error on bad source")
	}
}

// TestCommitFocusEnforcesInvariants is the important one: every check here
// existed because an LLM chose the times. The deterministic loop calls the same
// function, so these guarantees must survive its removal.
func TestCommitFocusEnforcesInvariants(t *testing.T) {
	c := newCycle(t)
	ctx := context.Background()
	c.p.OAuth = oauth.NewFlow("cid", "csec", "http://localhost/cb", &oauth.Store{DB: c.p.DB})
	pj := &models.Project{Name: "P", Kind: models.SlotWork, TargetHours: 4, Status: models.ProjectActive}
	if err := c.p.DB.Create(pj).Error; err != nil {
		t.Fatal(err)
	}

	planFrom, _ := c.planWindow()
	commit := func(start, end time.Time) error {
		return c.commitFocus(ctx, models.SourceProject, pj.ID, start, end)
	}
	// The unlinked test account makes any commit error, so match the message.
	wantErr := func(start, end time.Time, substr string) {
		t.Helper()
		if err := commit(start, end); err == nil || !contains(err.Error(), substr) {
			t.Errorf("commit %s..%s: got %v, want error containing %q",
				start.Format(time.RFC3339), end.Format(time.RFC3339), err, substr)
		}
	}

	// Duration outside 30-90 minutes is rejected regardless of window.
	wantErr(planFrom, planFrom.Add(10*time.Minute), "minutes")
	wantErr(planFrom, planFrom.Add(3*time.Hour), "minutes")

	// Before the planning start, and past the window end.
	wantErr(planFrom.Add(-24*time.Hour), planFrom.Add(-23*time.Hour), "before planning start")
	_, windowEnd := c.planWindow()
	wantErr(windowEnd.Add(time.Hour), windowEnd.Add(2*time.Hour), "past the plan window end")

	// No working-hours window covers the block: rejected.
	wantErr(planFrom, planFrom.Add(time.Hour), "working hours")

	openAllHours(t, c, models.SlotWork)
	if err := c.p.DB.Create(&models.Session{
		Source: models.SourceProject, SourceID: pj.ID, AccountKind: models.AccountWork,
		CalendarID: "primary", ScheduledStart: planFrom, ScheduledEnd: planFrom.Add(time.Hour),
		Status: models.SessionPlanned,
	}).Error; err != nil {
		t.Fatal(err)
	}
	wantErr(planFrom, planFrom.Add(time.Hour), "overlaps")

	// A valid block passes validation and fails only at the unlinked calendar
	// client — i.e. it made it past every invariant check.
	if err := commit(planFrom.Add(time.Hour), planFrom.Add(2*time.Hour)); err == nil || !contains(err.Error(), "not linked") {
		t.Errorf("valid block should reach the calendar client, got: %v", err)
	}
}

func TestCommitFocusOneHabitPerDay(t *testing.T) {
	c := newCycle(t)
	ctx := context.Background()
	c.p.OAuth = oauth.NewFlow("cid", "csec", "http://localhost/cb", &oauth.Store{DB: c.p.DB})
	tz := c.p.Cfg.Timezone

	h := &models.Habit{
		Name: "Walk", Kind: models.SlotPersonal, BlockDurationMinutes: 60,
		Active: true, Cadence: datatypes.JSON(`{"type":"per_week","count":3}`),
	}
	if err := c.p.DB.Create(h).Error; err != nil {
		t.Fatal(err)
	}
	openAllHours(t, c, models.SlotPersonal)

	planFrom, windowEnd := c.planWindow()
	// Use the first whole local day after planning start, so intra-day blocks
	// are all in-window and share one calendar day.
	day := startOfDay(planFrom, tz)
	if !day.After(planFrom) {
		day = day.AddDate(0, 0, 1)
	}
	if day.AddDate(0, 0, 1).After(windowEnd) {
		t.Skip("too close to window end for a full in-window day")
	}
	commitHabit := func(start time.Time) error {
		return c.commitFocus(ctx, models.SourceHabit, h.ID, start, start.Add(time.Hour))
	}

	// The day's first habit block clears every invariant and only fails at the
	// unlinked calendar client — the per-day guard must not block it.
	if err := commitHabit(day.Add(10 * time.Hour)); err == nil || !contains(err.Error(), "not linked") {
		t.Fatalf("first habit block should reach the calendar client, got: %v", err)
	}
	// Seed a planned session for the same habit on that day (the commit above
	// failed at the client and never persisted one).
	if err := c.p.DB.Create(&models.Session{
		Source: models.SourceHabit, SourceID: h.ID, AccountKind: models.AccountPersonal,
		CalendarID: "primary", ScheduledStart: day.Add(10 * time.Hour), ScheduledEnd: day.Add(11 * time.Hour),
		Status: models.SessionPlanned,
	}).Error; err != nil {
		t.Fatal(err)
	}
	// A second, non-overlapping block for the same habit on the same day is
	// rejected: at most one block per day per habit.
	if err := commitHabit(day.Add(13 * time.Hour)); err == nil || !contains(err.Error(), "per day") {
		t.Errorf("second same-day habit block: got %v, want error containing %q", err, "per day")
	}
}

func TestResolveSource(t *testing.T) {
	c := newCycle(t)
	ctx := context.Background()
	pj := &models.Project{Name: "P", Kind: models.SlotWork, TargetHours: 1, Status: models.ProjectActive}
	if err := c.p.DB.Create(pj).Error; err != nil {
		t.Fatal(err)
	}
	name, kind, err := c.resolveSource(ctx, models.SourceProject, pj.ID)
	if err != nil || name != "P" || kind != models.SlotWork {
		t.Fatalf("resolve project: %q %q %v", name, kind, err)
	}
	h := &models.Habit{
		Name: "Walk", Kind: models.SlotPersonal, BlockDurationMinutes: 30,
		Active: true, Cadence: datatypes.JSON(`{"type":"per_week","count":1}`),
	}
	if err := c.p.DB.Create(h).Error; err != nil {
		t.Fatal(err)
	}
	name, kind, err = c.resolveSource(ctx, models.SourceHabit, h.ID)
	if err != nil || name != "Walk" || kind != models.SlotPersonal {
		t.Fatalf("resolve habit: %q %q %v", name, kind, err)
	}
	if _, _, err := c.resolveSource(ctx, models.SourceKind("nope"), "x"); err == nil {
		t.Fatal("expected error for unknown source kind")
	}
}

func TestIntVal(t *testing.T) {
	if got := intVal(3); got != 3 {
		t.Errorf("intVal(3) = %d", got)
	}
	if got := intVal("nope"); got != 0 {
		t.Errorf("intVal(non-int) = %d, want 0", got)
	}
	if got := intVal(nil); got != 0 {
		t.Errorf("intVal(nil) = %d, want 0", got)
	}
}

// ---- deterministic loop ----

// TestFillProjectStopsWhenNothingBookable covers the loop's termination: with
// no working hours there are no slots, so it must return rather than spin.
func TestFillProjectStopsWhenNothingBookable(t *testing.T) {
	c := newCycle(t)
	c.fillProject(context.Background(), projectInfo{
		ID: "11111111-1111-1111-1111-111111111111", Name: "P",
		Kind: string(models.SlotWork), HoursRemaining: 8,
	})
	if n := intVal(c.summary["projects_scheduled"]); n != 0 {
		t.Errorf("scheduled %d blocks with no working hours, want 0", n)
	}
}

func TestFillProjectSkipsMetProjects(t *testing.T) {
	c := newCycle(t)
	openAllHours(t, c, models.SlotWork)
	// Less than the 30-minute minimum block remains, so nothing should be
	// attempted at all — not even a rejected commit.
	c.fillProject(context.Background(), projectInfo{
		ID: "11111111-1111-1111-1111-111111111111", Name: "P",
		Kind: string(models.SlotWork), HoursRemaining: 0.25,
	})
	if errs, _ := c.summary["errors"].([]string); len(errs) != 0 {
		t.Errorf("a project under one block should be left alone, got errors %v", errs)
	}
}

func TestFillProjectRejectsBadKind(t *testing.T) {
	c := newCycle(t)
	c.fillProject(context.Background(), projectInfo{
		ID: "x", Name: "P", Kind: "nonsense", HoursRemaining: 4,
	})
	errs, _ := c.summary["errors"].([]string)
	if len(errs) == 0 || !contains(errs[0], "invalid kind") {
		t.Errorf("want an invalid-kind error, got %v", errs)
	}
}

func TestFillProjectRejectsBadDeadline(t *testing.T) {
	c := newCycle(t)
	c.fillProject(context.Background(), projectInfo{
		ID: "x", Name: "P", Kind: string(models.SlotWork),
		HoursRemaining: 4, Deadline: "not-a-time",
	})
	errs, _ := c.summary["errors"].([]string)
	if len(errs) == 0 || !contains(errs[0], "bad deadline") {
		t.Errorf("want a bad-deadline error, got %v", errs)
	}
}

// TestFillHabitRejectsOutOfBoundsBlock pins the early exit: commitFocus would
// reject every slot for such a habit, so the loop must say so once instead of
// failing per candidate.
func TestFillHabitRejectsOutOfBoundsBlock(t *testing.T) {
	c := newCycle(t)
	c.fillHabit(context.Background(), habitInfo{
		ID: "x", Name: "Tiny", Kind: string(models.SlotPersonal),
		BlockMinutes: 5, TargetInWindow: 3,
	})
	errs, _ := c.summary["errors"].([]string)
	if len(errs) != 1 || !contains(errs[0], "outside the allowed") {
		t.Errorf("want exactly one out-of-bounds error, got %v", errs)
	}
}

func TestFillHabitSkipsSatisfied(t *testing.T) {
	c := newCycle(t)
	openAllHours(t, c, models.SlotPersonal)
	c.fillHabit(context.Background(), habitInfo{
		ID: "x", Name: "Walk", Kind: string(models.SlotPersonal),
		BlockMinutes: 60, TargetInWindow: 3, ScheduledInWindow: 3,
	})
	if errs, _ := c.summary["errors"].([]string); len(errs) != 0 {
		t.Errorf("a satisfied habit should be left alone, got %v", errs)
	}
}

func TestFillHabitRejectsBadKind(t *testing.T) {
	c := newCycle(t)
	c.fillHabit(context.Background(), habitInfo{
		ID: "x", Name: "Walk", Kind: "nonsense", BlockMinutes: 60, TargetInWindow: 2,
	})
	errs, _ := c.summary["errors"].([]string)
	if len(errs) == 0 || !contains(errs[0], "invalid kind") {
		t.Errorf("want an invalid-kind error, got %v", errs)
	}
}

// TestFillProjectReachesTheCalendar is the happy path. Every other loop test
// asserts something did NOT happen, so a fillProject that never books would
// pass them all. Here the only thing standing between the loop and a booked
// event is the unlinked test calendar account, so a "not linked" error proves
// it sized a block, found a slot, and cleared every invariant in commitFocus.
func TestFillProjectReachesTheCalendar(t *testing.T) {
	c := newCycle(t)
	c.p.OAuth = oauth.NewFlow("cid", "csec", "http://localhost/cb", &oauth.Store{DB: c.p.DB})
	openAllHours(t, c, models.SlotWork)
	pj := &models.Project{Name: "P", Kind: models.SlotWork, TargetHours: 4, Status: models.ProjectActive}
	if err := c.p.DB.Create(pj).Error; err != nil {
		t.Fatal(err)
	}

	c.fillProject(context.Background(), projectInfo{
		ID: pj.ID, Name: pj.Name, Kind: string(models.SlotWork), HoursRemaining: 4,
	})

	// Each iteration tries every candidate in the batch, then gives up once no
	// slot was bookable — so expect one error per candidate, and no second
	// iteration on top of that.
	errs, _ := c.summary["errors"].([]string)
	if len(errs) == 0 {
		t.Fatal("fillProject booked nothing and reported nothing: it never reached commitFocus")
	}
	if len(errs) > slotOversample {
		t.Errorf("want at most %d attempts before giving up, got %d: %v", slotOversample, len(errs), errs)
	}
	for _, e := range errs {
		// "minutes" would mean the block was sized outside 30-90 — e.g. a
		// math.Min inversion asking for all 4 remaining hours at once.
		if contains(e, "minutes") {
			t.Errorf("block sized wrong rather than reaching the client: %v", e)
		}
		if !contains(e, "not linked") {
			t.Errorf("want the unlinked-account error, got %v", e)
		}
	}
}

// TestFillHabitBooksAcrossDistinctDays proves the habit loop iterates candidate
// slots instead of giving up on the first failure, and spreads across days.
func TestFillHabitBooksAcrossDistinctDays(t *testing.T) {
	c := newCycle(t)
	c.p.OAuth = oauth.NewFlow("cid", "csec", "http://localhost/cb", &oauth.Store{DB: c.p.DB})
	openAllHours(t, c, models.SlotPersonal)
	h := &models.Habit{
		Name: "Walk", Kind: models.SlotPersonal, BlockDurationMinutes: 60,
		Active: true, Cadence: datatypes.JSON(`{"type":"per_week","count":3}`),
	}
	if err := c.p.DB.Create(h).Error; err != nil {
		t.Fatal(err)
	}

	c.fillHabit(context.Background(), habitInfo{
		ID: h.ID, Name: h.Name, Kind: string(models.SlotPersonal),
		BlockMinutes: 60, TargetInWindow: 2, ScheduledInWindow: 0,
	})

	errs, _ := c.summary["errors"].([]string)
	// fillHabit continues past a failed commit, so both needed blocks are
	// attempted; one error would mean it bailed after the first.
	if len(errs) < 2 {
		t.Fatalf("want at least two attempts for a shortfall of two, got %v", errs)
	}
	for _, e := range errs {
		if !contains(e, "not linked") {
			t.Errorf("every attempt should reach the client, got %v", e)
		}
	}
}

// TestPlanEmptyState is the whole pass over an empty DB: it must succeed and
// call nothing.
func TestPlanEmptyState(t *testing.T) {
	c := newCycle(t)
	if err := c.plan(context.Background()); err != nil {
		t.Fatalf("plan on empty state: %v", err)
	}
	if n := intVal(c.summary["projects_scheduled"]); n != 0 {
		t.Errorf("projects_scheduled = %d, want 0", n)
	}
	if n := intVal(c.summary["habits_scheduled"]); n != 0 {
		t.Errorf("habits_scheduled = %d, want 0", n)
	}
}
