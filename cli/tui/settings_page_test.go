package tui

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestWorkingHoursGridRendersClockTimes(t *testing.T) {
	var p Page = newWorkingHoursPage(nil)
	p, _ = p.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	p, _ = p.Update(workingHoursMsg{hours: []WorkingHour{
		{SlotKind: "work", DayOfWeek: 1, StartMinute: 540, EndMinute: 1080},
		{SlotKind: "personal", DayOfWeek: 0, StartMinute: 480, EndMinute: 1440},
		{SlotKind: "personal", DayOfWeek: 0, StartMinute: 60, EndMinute: 120},
	}})

	view := p.View()
	for _, want := range []string{"09:00-18:00", "01:00-02:00, 08:00-24:00", "Mon", "Sun", "—"} {
		if !strings.Contains(view, want) {
			t.Errorf("grid missing %q:\n%s", want, view)
		}
	}
	if strings.Contains(view, "540") {
		t.Errorf("grid should render clock times, not raw minutes:\n%s", view)
	}
}

// The cursor picks which cell the form edits, so moving it must change the form.
func TestWorkingHoursCursorSelectsCell(t *testing.T) {
	p := newWorkingHoursPage(nil)
	p.hours = []WorkingHour{{SlotKind: "personal", DayOfWeek: 3, StartMinute: 600, EndMinute: 660}}

	var page Page = p
	page, _ = page.Update(tea.KeyPressMsg{Code: 'j'})          // Sun -> Mon
	page, _ = page.Update(tea.KeyPressMsg{Code: 'j'})          // -> Tue
	page, _ = page.Update(tea.KeyPressMsg{Code: 'j'})          // -> Wed
	page, _ = page.Update(tea.KeyPressMsg{Code: tea.KeyRight}) // work -> personal
	page, _ = page.Update(tea.KeyPressMsg{Code: 'e'})

	got := page.(workingHoursPage)
	if !got.FullInput() {
		t.Fatal("e should open the edit form")
	}
	if got.fd.kind != "personal" || got.fd.day != 3 {
		t.Fatalf("form targets %s day %d, want personal day 3", got.fd.kind, got.fd.day)
	}
	if got.fd.windows != "10:00-11:00" {
		t.Fatalf("form should prefill the selected cell, got %q", got.fd.windows)
	}
}

// Left and right must move opposite ways, not both forward.
func TestWorkingHoursKindNavigationIsBidirectional(t *testing.T) {
	var page Page = newWorkingHoursPage(nil)
	if got := page.(workingHoursPage).kind; got != 0 {
		t.Fatalf("kind starts at %d, want 0", got)
	}
	page, _ = page.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if got := page.(workingHoursPage).kind; got != 1 {
		t.Fatalf("right moved to %d, want 1", got)
	}
	page, _ = page.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if got := page.(workingHoursPage).kind; got != 0 {
		t.Fatalf("left moved to %d, want back to 0", got)
	}
	// Left from the first column wraps to the last.
	page, _ = page.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if got := page.(workingHoursPage).kind; got != len(slotKinds)-1 {
		t.Fatalf("left from the first column landed on %d, want %d", got, len(slotKinds)-1)
	}
}

func TestWorkingHoursSubmitPatchesOneDay(t *testing.T) {
	var rec capturedReq
	server := captureServer(t, &rec, http.StatusOK)
	defer server.Close()

	p := workingHoursPage{
		client: stubClient(server),
		fd:     &hoursForm{kind: "personal", day: 6, windows: "08:30-12:00, 13:00-24:00"},
	}
	if msg, ok := p.submitForm()().(errMsg); ok {
		t.Fatalf("submit errored: %v", msg)
	}
	if rec.method != http.MethodPatch || rec.path != "/working-hours/personal/6" {
		t.Fatalf("expected PATCH /working-hours/personal/6, got %s %s", rec.method, rec.path)
	}
	var got []DayWindow
	if err := json.Unmarshal(rec.body, &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.body)
	}
	want := []DayWindow{{StartMinute: 510, EndMinute: 720}, {StartMinute: 780, EndMinute: 1440}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("payload = %+v, want %+v", got, want)
	}
}

func TestWorkingHoursSubmitRejectsBadTimes(t *testing.T) {
	for _, windows := range []string{"9-18", "09:00", "09:00-08:00", "25:00-26:00", "09:61-10:00"} {
		p := workingHoursPage{fd: &hoursForm{kind: "work", day: 1, windows: windows}}
		if _, ok := p.submitForm()().(errMsg); !ok {
			t.Errorf("%q should not submit", windows)
		}
	}
}

func TestSettingsPageRendersAndSubmits(t *testing.T) {
	var rec capturedReq
	server := captureServer(t, &rec, http.StatusOK)
	defer server.Close()

	var page Page = newSettingsPage(stubClient(server))
	page, _ = page.Update(settingsMsg{settings: Settings{
		SoftEventTitles: []string{"Lunch"}, TriageEnabled: true,
		TriageConfidenceThreshold: 0.8, TriageBackfillDays: 7, TriageReconcileDays: 7,
		PlanHorizonDays: 30, FocusBlockMinMinutes: 30, FocusBlockMaxMinutes: 90,
		DailyBudgetUSD: 2,
	}})
	view := page.View()
	for _, want := range []string{"Plan horizon days", "30", "Lunch", "30-90", "$2.00/day"} {
		if !strings.Contains(view, want) {
			t.Errorf("settings view missing %q:\n%s", want, view)
		}
	}

	p := page.(settingsPage)
	p.fd = &settingsForm{
		softTitles: "Lunch, Dinner Decompress", triageEnabled: true, triageDryRun: true,
		threshold: "0.6", backfillDays: "10", reconcileDays: "4",
		horizonDays: "21", blockMinMinute: "25", blockMaxMinute: "100",
		dailyBudget: "1.5",
	}
	if msg, ok := p.submitForm()().(errMsg); ok {
		t.Fatalf("submit errored: %v", msg)
	}
	if rec.method != http.MethodPut || rec.path != "/settings" {
		t.Fatalf("expected PUT /settings, got %s %s", rec.method, rec.path)
	}
	var got Settings
	if err := json.Unmarshal(rec.body, &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, rec.body)
	}
	// The budget must survive a form submit: sending 0 would turn the cap off.
	if got.DailyBudgetUSD != 1.5 {
		t.Errorf("daily budget = %v, want 1.5", got.DailyBudgetUSD)
	}
	if got.PlanHorizonDays != 21 || got.FocusBlockMinMinutes != 25 || got.FocusBlockMaxMinutes != 100 {
		t.Errorf("payload numbers wrong: %+v", got)
	}
	if !got.TriageDryRun || got.TriageConfidenceThreshold != 0.6 || got.TriageBackfillDays != 10 {
		t.Errorf("payload triage knobs wrong: %+v", got)
	}
	if len(got.SoftEventTitles) != 2 {
		t.Errorf("soft titles = %v", got.SoftEventTitles)
	}
}

func TestSettingsSubmitRejectsBadNumbers(t *testing.T) {
	base := settingsForm{
		threshold: "0.8", backfillDays: "7", reconcileDays: "7",
		horizonDays: "30", blockMinMinute: "30", blockMaxMinute: "90",
	}
	bad := map[string]func(*settingsForm){
		"threshold": func(f *settingsForm) { f.threshold = "high" },
		"horizon":   func(f *settingsForm) { f.horizonDays = "a month" },
		"min block": func(f *settingsForm) { f.blockMinMinute = "" },
	}
	for name, mutate := range bad {
		fd := base
		mutate(&fd)
		p := settingsPage{fd: &fd}
		if _, ok := p.submitForm()().(errMsg); !ok {
			t.Errorf("%s should not submit", name)
		}
	}
}
