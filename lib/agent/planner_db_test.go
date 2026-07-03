package agent_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/icco/art/lib/agent"
	"github.com/icco/art/lib/config"
	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/oauth"
	"github.com/icco/art/lib/testdb"
)

// newPlanner builds a Planner against the test DB. Tests that call Run() must
// skip when VERTEX_PROJECT_ID is unset, since Run now delegates to Vertex.
func newPlanner(t *testing.T) *agent.Planner {
	t.Helper()
	db := testdb.Open(t)
	tz, _ := time.LoadLocation("America/Los_Angeles")
	cfg := &config.Config{
		Timezone: tz,
		Vertex: config.VertexConfig{
			ProjectID: os.Getenv("VERTEX_PROJECT_ID"),
			Location:  cmpOr(os.Getenv("VERTEX_LOCATION"), "us-central1"),
		},
	}
	flow := oauth.NewFlow("cid", "csec", "http://localhost/cb", &oauth.Store{DB: db})
	return &agent.Planner{Cfg: cfg, DB: db, OAuth: flow}
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func skipUnlessVertex(t *testing.T) {
	t.Helper()
	if os.Getenv("VERTEX_PROJECT_ID") == "" {
		t.Skip("VERTEX_PROJECT_ID not set; skipping LLM-backed planner test")
	}
}

// Run delegates to the Vertex Gemini agent; we can only exercise it when
// real credentials are present. CI skips this by default.
func TestPlannerRunEmpty(t *testing.T) {
	skipUnlessVertex(t)
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

func TestFindFreeSlotsHonorsPlannedSessions(t *testing.T) {
	db := testdb.Open(t)
	tz, _ := time.LoadLocation("America/Los_Angeles")
	if err := db.Create(&models.WorkingHour{
		SlotKind: models.SlotWork, DayOfWeek: 1, StartMinute: 9 * 60, EndMinute: 18 * 60,
	}).Error; err != nil {
		t.Fatal(err)
	}
	// A focus block committed earlier in the same run exists only as a
	// session row; the mirroring Event row won't appear until the next sync.
	monday10 := time.Date(2026, 5, 25, 10, 0, 0, 0, tz)
	if err := db.Create(&models.Session{
		Source: models.SourceProject, SourceID: "11111111-1111-1111-1111-111111111111",
		AccountKind: models.AccountWork, CalendarID: "primary",
		ScheduledStart: monday10, ScheduledEnd: monday10.Add(time.Hour),
		Status: models.SessionPlanned,
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
			t.Fatalf("slot %v-%v overlaps planned session", s.Start, s.End)
		}
	}
	if len(slots) == 0 {
		t.Fatal("expected at least one free slot in 9-18 window")
	}
}
