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
	slots, err := agent.FindFreeSlots(context.Background(), db, tz, models.AccountWork, models.SlotWork, 60, from, to, 5, nil)
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

func TestFindFreeSlotsAllDayEvents(t *testing.T) {
	db := testdb.Open(t)
	tz, _ := time.LoadLocation("America/Los_Angeles")
	for _, d := range []int{1, 2} {
		if err := db.Create(&models.WorkingHour{
			SlotKind: models.SlotWork, DayOfWeek: d, StartMinute: 9 * 60, EndMinute: 18 * 60,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	monday := time.Date(2026, 5, 25, 0, 0, 0, 0, tz)
	tuesday := monday.AddDate(0, 0, 1)
	if err := db.Create(&models.Event{
		AccountKind: models.AccountWork, CalendarID: "primary", GoogleEventID: "ooo1",
		StartTime: monday, EndTime: tuesday, AllDay: true, EventType: "outOfOffice", Status: "confirmed",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.Event{
		AccountKind: models.AccountWork, CalendarID: "primary", GoogleEventID: "bday1",
		StartTime: tuesday, EndTime: tuesday.AddDate(0, 0, 1), AllDay: true, EventType: "default", Status: "confirmed",
	}).Error; err != nil {
		t.Fatal(err)
	}
	slots, err := agent.FindFreeSlots(context.Background(), db, tz, models.AccountWork, models.SlotWork, 60,
		monday, tuesday.AddDate(0, 0, 1), 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) == 0 {
		t.Fatal("expected Tuesday slots despite the all-day birthday")
	}
	for _, s := range slots {
		if s.Start.Before(tuesday) {
			t.Fatalf("slot %v-%v booked over an all-day out-of-office", s.Start, s.End)
		}
	}
}

// A soft event opens its time up, but only as a last resort: every hard-free
// slot must be offered ahead of it.
func TestFindFreeSlotsRanksSoftEventsLast(t *testing.T) {
	db := testdb.Open(t)
	tz, _ := time.LoadLocation("America/Los_Angeles")
	if err := db.Create(&models.WorkingHour{
		SlotKind: models.SlotWork, DayOfWeek: 1, StartMinute: 9 * 60, EndMinute: 18 * 60,
	}).Error; err != nil {
		t.Fatal(err)
	}
	monday9 := time.Date(2026, 5, 25, 9, 0, 0, 0, tz)
	// A placeholder covering 9-11 and a real meeting covering 11-12.
	if err := db.Create(&models.Event{
		AccountKind: models.AccountWork, CalendarID: "primary", GoogleEventID: "soft1",
		Summary: "Morning Prep", StartTime: monday9, EndTime: monday9.Add(2 * time.Hour),
		Status: "confirmed",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.Event{
		AccountKind: models.AccountWork, CalendarID: "primary", GoogleEventID: "hard1",
		Summary: "Standup", StartTime: monday9.Add(2 * time.Hour), EndTime: monday9.Add(3 * time.Hour),
		Status: "confirmed",
	}).Error; err != nil {
		t.Fatal(err)
	}

	to := time.Date(2026, 5, 25, 18, 0, 0, 0, tz)
	soft := models.NewSoftTitles("Morning Prep")
	slots, err := agent.FindFreeSlots(context.Background(), db, tz, models.AccountWork, models.SlotWork, 60, monday9, to, 20, soft)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) == 0 {
		t.Fatal("expected slots")
	}
	if slots[0].Soft || !slots[0].Start.Equal(monday9.Add(3*time.Hour)) {
		t.Fatalf("first slot = %v (soft=%v), want the hard-free 12:00", slots[0].Start, slots[0].Soft)
	}
	var softStarts []time.Time
	seenSoft := false
	for _, s := range slots {
		if s.Soft {
			seenSoft = true
			softStarts = append(softStarts, s.Start)
			continue
		}
		if seenSoft {
			t.Fatalf("hard slot %v came after a soft slot", s.Start)
		}
		if s.Start.Before(monday9.Add(3 * time.Hour)) {
			t.Fatalf("slot %v before 12:00 should have been marked soft", s.Start)
		}
	}
	if len(softStarts) != 2 {
		t.Fatalf("soft slot starts = %v, want the two hours the placeholder covers", softStarts)
	}
	if !softStarts[0].Equal(monday9) {
		t.Fatalf("first soft slot = %v, want 9:00", softStarts[0])
	}

	// Without the soft list the placeholder is ordinary busy time.
	strict, err := agent.FindFreeSlots(context.Background(), db, tz, models.AccountWork, models.SlotWork, 60, monday9, to, 20, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range strict {
		if s.Soft || s.Start.Before(monday9.Add(3*time.Hour)) {
			t.Fatalf("slot %v (soft=%v) should not exist without soft titles", s.Start, s.Soft)
		}
	}
}

// A short placeholder must not swallow the hard-free time next to it: the slot
// starting after it wins over the one sitting on top of it.
func TestFindFreeSlotsGivesHardTimeFirstRefusal(t *testing.T) {
	db := testdb.Open(t)
	tz, _ := time.LoadLocation("America/Los_Angeles")
	if err := db.Create(&models.WorkingHour{
		SlotKind: models.SlotWork, DayOfWeek: 1, StartMinute: 9 * 60, EndMinute: 18 * 60,
	}).Error; err != nil {
		t.Fatal(err)
	}
	monday9 := time.Date(2026, 5, 25, 9, 0, 0, 0, tz)
	// Placeholder 9:00-9:30, then a real meeting eating 11:00 onward. A 60-min
	// block fits at 9:30 without touching the placeholder.
	if err := db.Create(&models.Event{
		AccountKind: models.AccountWork, CalendarID: "primary", GoogleEventID: "soft1",
		Summary: "Morning Prep", StartTime: monday9, EndTime: monday9.Add(30 * time.Minute),
		Status: "confirmed",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&models.Event{
		AccountKind: models.AccountWork, CalendarID: "primary", GoogleEventID: "hard1",
		Summary: "All-hands", StartTime: monday9.Add(2 * time.Hour), EndTime: monday9.Add(9 * time.Hour),
		Status: "confirmed",
	}).Error; err != nil {
		t.Fatal(err)
	}

	to := time.Date(2026, 5, 25, 18, 0, 0, 0, tz)
	soft := models.NewSoftTitles("Morning Prep")
	slots, err := agent.FindFreeSlots(context.Background(), db, tz, models.AccountWork, models.SlotWork, 60, monday9, to, 20, soft)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 1 {
		t.Fatalf("slots = %v, want just the 9:30 hard-free block", slots)
	}
	if slots[0].Soft || !slots[0].Start.Equal(monday9.Add(30*time.Minute)) {
		t.Fatalf("slot = %v (soft=%v), want hard 9:30", slots[0].Start, slots[0].Soft)
	}
}

// When the placeholder is the only thing left, its time does get offered.
func TestFindFreeSlotsUsesSoftTimeAsLastResort(t *testing.T) {
	db := testdb.Open(t)
	tz, _ := time.LoadLocation("America/Los_Angeles")
	if err := db.Create(&models.WorkingHour{
		SlotKind: models.SlotWork, DayOfWeek: 1, StartMinute: 9 * 60, EndMinute: 18 * 60,
	}).Error; err != nil {
		t.Fatal(err)
	}
	monday9 := time.Date(2026, 5, 25, 9, 0, 0, 0, tz)
	if err := db.Create(&models.Event{
		AccountKind: models.AccountWork, CalendarID: "primary", GoogleEventID: "soft1",
		Summary: "Dinner Decompress", StartTime: monday9, EndTime: monday9.Add(time.Hour),
		Status: "confirmed",
	}).Error; err != nil {
		t.Fatal(err)
	}
	// The rest of the day is genuinely booked.
	if err := db.Create(&models.Event{
		AccountKind: models.AccountWork, CalendarID: "primary", GoogleEventID: "hard1",
		Summary: "All-hands", StartTime: monday9.Add(time.Hour), EndTime: monday9.Add(9 * time.Hour),
		Status: "confirmed",
	}).Error; err != nil {
		t.Fatal(err)
	}

	to := time.Date(2026, 5, 25, 18, 0, 0, 0, tz)
	soft := models.NewSoftTitles("Dinner Decompress")
	slots, err := agent.FindFreeSlots(context.Background(), db, tz, models.AccountWork, models.SlotWork, 60, monday9, to, 20, soft)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 1 || !slots[0].Soft || !slots[0].Start.Equal(monday9) {
		t.Fatalf("slots = %v, want one soft 9:00 block", slots)
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
	slots, err := agent.FindFreeSlots(context.Background(), db, tz, models.AccountWork, models.SlotWork, 60, from, to, 5, nil)
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
