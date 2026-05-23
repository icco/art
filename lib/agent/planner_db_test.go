package agent_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/icco/art/lib/agent"
	"github.com/icco/art/lib/config"
	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/oauth"
	"github.com/icco/art/lib/testdb"
	"gorm.io/datatypes"
)

func newPlanner(t *testing.T) *agent.Planner {
	t.Helper()
	db := testdb.Open(t)
	tz, _ := time.LoadLocation("America/Los_Angeles")
	cfg := &config.Config{
		Timezone: tz,
		Vertex:   config.VertexConfig{},
	}
	flow := oauth.NewFlow("cid", "csec", "http://localhost/cb", &oauth.Store{DB: db})
	return &agent.Planner{Cfg: cfg, DB: db, OAuth: flow}
}

// With no projects/habits and no linked accounts the planner should still
// record a successful agent_run row.
func TestPlannerRunEmpty(t *testing.T) {
	p := newPlanner(t)
	if err := p.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var n int64
	p.DB.Model(&models.AgentRun{}).Count(&n)
	if n != 1 {
		t.Fatalf("expected 1 agent_run row, got %d", n)
	}
}

// With a project but no linked account, planProjects records a per-item
// error in the summary but the cycle still succeeds.
func TestPlannerRunUnlinkedAccount(t *testing.T) {
	p := newPlanner(t)
	deadline := time.Now().Add(3 * 24 * time.Hour)
	if err := p.DB.Create(&models.Project{
		Name:        "Design X",
		Kind:        models.SlotWork,
		TargetHours: 2,
		Deadline:    &deadline,
		Status:      models.ProjectActive,
	}).Error; err != nil {
		t.Fatal(err)
	}
	cadJSON, _ := json.Marshal(models.Cadence{Type: "per_week", Count: 1})
	if err := p.DB.Create(&models.Habit{
		Name:                 "Walk",
		Kind:                 models.SlotPersonal,
		BlockDurationMinutes: 30,
		Cadence:              datatypes.JSON(cadJSON),
		Active:               true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := p.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var run models.AgentRun
	if err := p.DB.Order("started_at DESC").First(&run).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != models.AgentRunSucceeded {
		t.Fatalf("expected succeeded, got %s", run.Status)
	}
}

func TestFindFreeSlotsHonorsBusy(t *testing.T) {
	db := testdb.Open(t)
	tz, _ := time.LoadLocation("America/Los_Angeles")
	// Working hours: Mon 9-18.
	if err := db.Create(&models.WorkingHour{
		SlotKind: models.SlotWork, DayOfWeek: 1, StartMinute: 9 * 60, EndMinute: 18 * 60,
	}).Error; err != nil {
		t.Fatal(err)
	}
	// Existing meeting Mon 10-11 PT.
	monday10 := time.Date(2026, 5, 25, 10, 0, 0, 0, tz)
	if err := db.Create(&models.Event{
		AccountKind:   models.AccountWork,
		CalendarID:    "primary",
		GoogleEventID: "busy1",
		StartTime:     monday10,
		EndTime:       monday10.Add(time.Hour),
		Status:        "confirmed",
	}).Error; err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 5, 25, 9, 0, 0, 0, tz)
	to := time.Date(2026, 5, 25, 18, 0, 0, 0, tz)
	slots, err := agent.FindFreeSlots(context.Background(), db, tz, models.AccountWork, models.SlotWork, 60, from, to, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range slots {
		if s.Start.Before(monday10.Add(time.Hour)) && s.End.After(monday10) {
			t.Fatalf("slot %v-%v overlaps busy range", s.Start, s.End)
		}
	}
	if len(slots) == 0 {
		t.Fatal("expected at least one free slot in 9-18 window")
	}
}
