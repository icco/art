package agent

import (
	"context"
	"testing"
	"time"

	"github.com/icco/art/lib/config"
	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/testdb"
	"gorm.io/gorm"
)

// fakeCommit records calls and creates session rows the way the real
// CommitBlock does, minus Google.
type fakeCommit struct {
	db    *gorm.DB
	calls []models.Session
	fail  bool
}

func (f *fakeCommit) commit(_ context.Context, source models.SourceKind, sourceID string, start, end time.Time) (string, string, error) {
	if f.fail {
		return "", "", context.DeadlineExceeded
	}
	evID := "ev-" + sourceID + start.Format("150405")
	sess := models.Session{
		Source: source, SourceID: sourceID,
		AccountKind: models.AccountPersonal, CalendarID: "primary",
		GoogleEventID:  &evID,
		ScheduledStart: start, ScheduledEnd: end,
		Status: models.SessionPlanned,
	}
	if err := f.db.Create(&sess).Error; err != nil {
		return "", "", err
	}
	f.calls = append(f.calls, sess)
	return sess.ID, evID, nil
}

func deterministicPlanner(t *testing.T) (*Planner, *fakeCommit) {
	t.Helper()
	db := testdb.Open(t)
	tz, _ := time.LoadLocation("America/Los_Angeles")
	for d := range 7 {
		for _, k := range []models.SlotKind{models.SlotWork, models.SlotPersonal} {
			if err := db.Create(&models.WorkingHour{SlotKind: k, DayOfWeek: d, StartMinute: 9 * 60, EndMinute: 17 * 60}).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	p := &Planner{Cfg: &config.Config{Timezone: tz}, DB: db}
	return p, &fakeCommit{db: db}
}

func newSummary() map[string]any {
	return map[string]any{
		"projects_scheduled": 0,
		"habits_scheduled":   0,
		"tasks_scheduled":    0,
		"errors":             []string{},
	}
}

func TestDeterministicPlanSchedulesTaskAndIsIdempotent(t *testing.T) {
	p, fc := deterministicPlanner(t)
	task := models.Task{Title: "pack office", Kind: models.SlotPersonal, DurationMinutes: 120, Status: models.TaskPending}
	if err := p.DB.Create(&task).Error; err != nil {
		t.Fatal(err)
	}

	summary := newSummary()
	if err := p.deterministicPlanWith(context.Background(), summary, fc.commit); err != nil {
		t.Fatalf("plan: %v", err)
	}
	var total time.Duration
	for _, s := range fc.calls {
		total += s.ScheduledEnd.Sub(s.ScheduledStart)
	}
	if total != 2*time.Hour {
		t.Fatalf("scheduled %v, want 2h (calls: %d)", total, len(fc.calls))
	}
	var got models.Task
	if err := p.DB.First(&got, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != models.TaskScheduled {
		t.Fatalf("task status: %q, want scheduled", got.Status)
	}
	if summary["tasks_scheduled"].(int) < 1 {
		t.Fatalf("summary tasks_scheduled: %v", summary["tasks_scheduled"])
	}

	// Second run: everything covered, nothing new booked.
	before := len(fc.calls)
	if err := p.deterministicPlanWith(context.Background(), newSummary(), fc.commit); err != nil {
		t.Fatalf("replan: %v", err)
	}
	if len(fc.calls) != before {
		t.Fatalf("second run booked %d extra blocks", len(fc.calls)-before)
	}
}

func TestDeterministicPlanRefusesImpossibleDeadline(t *testing.T) {
	p, fc := deterministicPlanner(t)
	past := time.Now().Add(-24 * time.Hour)
	task := models.Task{Title: "too late", Kind: models.SlotPersonal, DurationMinutes: 60, Status: models.TaskPending, Deadline: &past}
	if err := p.DB.Create(&task).Error; err != nil {
		t.Fatal(err)
	}

	summary := newSummary()
	if err := p.deterministicPlanWith(context.Background(), summary, fc.commit); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if len(fc.calls) != 0 {
		t.Fatalf("refuse means no commits, got %d", len(fc.calls))
	}
	var got models.Task
	if err := p.DB.First(&got, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != models.TaskUnschedulable {
		t.Fatalf("task status: %q, want unschedulable", got.Status)
	}
	unsched, _ := summary["unschedulable"].([]map[string]any)
	if len(unsched) != 1 {
		t.Fatalf("summary unschedulable: %v", summary["unschedulable"])
	}
}

func TestDeterministicPlanProjectsAndHabits(t *testing.T) {
	p, fc := deterministicPlanner(t)
	pj := models.Project{Name: "thesis", Kind: models.SlotWork, TargetHours: 2, Status: models.ProjectActive}
	if err := p.DB.Create(&pj).Error; err != nil {
		t.Fatal(err)
	}
	h := models.Habit{Name: "run", Kind: models.SlotPersonal, BlockDurationMinutes: 45, Cadence: []byte(`{"type":"per_week","count":2}`), Active: true}
	if err := p.DB.Create(&h).Error; err != nil {
		t.Fatal(err)
	}

	summary := newSummary()
	if err := p.deterministicPlanWith(context.Background(), summary, fc.commit); err != nil {
		t.Fatalf("plan: %v", err)
	}

	var pjMinutes, habitBlocks int
	for _, s := range fc.calls {
		mins := int(s.ScheduledEnd.Sub(s.ScheduledStart).Minutes())
		switch s.Source {
		case models.SourceProject:
			pjMinutes += mins
			if mins > 90 || mins < 30 {
				t.Fatalf("project block %dm outside 30-90m", mins)
			}
		case models.SourceHabit:
			habitBlocks++
			if mins != 45 {
				t.Fatalf("habit block %dm, want 45", mins)
			}
		}
	}
	if pjMinutes != 120 {
		t.Fatalf("project minutes: %d, want 120", pjMinutes)
	}
	// 2/week over a 14-day window: 2 this week + 2 next week, but the current
	// week may be nearly over — at minimum next week's 2 must land.
	if habitBlocks < 2 {
		t.Fatalf("habit blocks: %d, want >= 2", habitBlocks)
	}
}
