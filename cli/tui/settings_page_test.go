package tui

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestSettingsPageRendersAndSubmits(t *testing.T) {
	var rec capturedReq
	server := captureServer(t, &rec, http.StatusOK)
	defer server.Close()

	var page Page = newSettingsPage(stubClient(server))
	page, _ = page.Update(settingsMsg{settings: Settings{
		TriageEnabled:             true,
		TriageConfidenceThreshold: 0.8, TriageBackfillDays: 7, TriageReconcileDays: 7,
		DailyBudgetUSD: 2,
	}})
	view := page.View()
	for _, want := range []string{"Triage backfill days", "7", "0.8", "$2.00/day"} {
		if !strings.Contains(view, want) {
			t.Errorf("settings view missing %q:\n%s", want, view)
		}
	}

	p := page.(settingsPage)
	p.fd = &settingsForm{
		triageEnabled: true, triageDryRun: true,
		threshold: "0.6", backfillDays: "10", reconcileDays: "4",
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
	if !got.TriageDryRun || got.TriageConfidenceThreshold != 0.6 || got.TriageBackfillDays != 10 {
		t.Errorf("payload triage knobs wrong: %+v", got)
	}
}

func TestSettingsSubmitRejectsBadNumbers(t *testing.T) {
	base := settingsForm{
		threshold: "0.8", backfillDays: "7", reconcileDays: "7",
		dailyBudget: "2",
	}
	bad := map[string]func(*settingsForm){
		"threshold": func(f *settingsForm) { f.threshold = "high" },
		"backfill":  func(f *settingsForm) { f.backfillDays = "a week" },
		"budget":    func(f *settingsForm) { f.dailyBudget = "" },
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
