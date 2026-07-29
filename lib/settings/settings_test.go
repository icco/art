package settings_test

import (
	"testing"
	"time"

	"github.com/icco/art/lib/calendar"
	"github.com/icco/art/lib/config"
	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/settings"
	"github.com/icco/art/lib/testdb"
)

func seedCfg() *config.Config {
	return &config.Config{
		SoftEventTitles: []string{"Morning Prep"},
		Triage: config.TriageConfig{
			Enabled: true, DryRun: false, BackfillDays: 14, ReconcileDays: 9,
			ConfidenceThreshold: 0.7,
		},
	}
}

// With no rows, every value comes from the env-loaded config.
func TestLoadFallsBackToEnvConfig(t *testing.T) {
	db := testdb.Open(t)
	got, err := settings.New(db, seedCfg()).Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.TriageBackfillDays != 14 || got.TriageReconcileDays != 9 {
		t.Errorf("triage days = %d/%d, want 14/9", got.TriageBackfillDays, got.TriageReconcileDays)
	}
	if got.TriageConfidenceThreshold != 0.7 {
		t.Errorf("threshold = %v, want 0.7", got.TriageConfidenceThreshold)
	}
	// Titles come through as written; normalization happens at match time.
	if len(got.SoftEventTitles) != 1 || got.SoftEventTitles[0] != "Morning Prep" {
		t.Errorf("soft titles = %v", got.SoftEventTitles)
	}
	// The planner knobs have no env var, so they take the built-in defaults.
	if got.PlanHorizonDays != settings.DefaultPlanHorizonDays {
		t.Errorf("horizon = %d, want %d", got.PlanHorizonDays, settings.DefaultPlanHorizonDays)
	}
	if got.FocusBlockMinMinutes != 30 || got.FocusBlockMaxMinutes != 90 {
		t.Errorf("block bounds = %d-%d, want 30-90", got.FocusBlockMinMinutes, got.FocusBlockMaxMinutes)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	db := testdb.Open(t)
	store := settings.New(db, seedCfg())

	want := settings.Values{
		SoftEventTitles:           []string{"Lunch", "Dinner Decompress"},
		TriageEnabled:             false,
		TriageDryRun:              true,
		TriageConfidenceThreshold: 0.55,
		TriageBackfillDays:        3,
		TriageReconcileDays:       0,
		PlanHorizonDays:           14,
		FocusBlockMinMinutes:      45,
		FocusBlockMaxMinutes:      120,
	}
	if err := store.Save(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.TriageEnabled || !got.TriageDryRun {
		t.Errorf("bools did not round-trip: %+v", got)
	}
	if got.TriageConfidenceThreshold != 0.55 || got.PlanHorizonDays != 14 {
		t.Errorf("numbers did not round-trip: %+v", got)
	}
	if got.FocusBlockMinMinutes != 45 || got.FocusBlockMaxMinutes != 120 {
		t.Errorf("block bounds did not round-trip: %+v", got)
	}
	if len(got.SoftEventTitles) != 2 || got.SoftEventTitles[0] != "Lunch" {
		t.Errorf("titles did not round-trip: %v", got.SoftEventTitles)
	}
	if !got.SoftTitles().Match("  lunch ") {
		t.Error("stored titles should still match case- and space-insensitively")
	}
	if got.PlanHorizon().Hours() != 14*24 {
		t.Errorf("PlanHorizon = %v", got.PlanHorizon())
	}

	// A second Save upserts rather than duplicating rows.
	want.PlanHorizonDays = 21
	if err := store.Save(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Model(&models.Setting{}).Count(&n)
	if n != int64(len(settings.Keys())) {
		t.Errorf("row count = %d, want %d", n, len(settings.Keys()))
	}
	got, _ = store.Load(t.Context())
	if got.PlanHorizonDays != 21 {
		t.Errorf("horizon after second save = %d, want 21", got.PlanHorizonDays)
	}
}

// A row for a key the store doesn't own must not reach Values, so a
// hand-inserted secret can never be served or acted on.
func TestLoadIgnoresUnknownKeys(t *testing.T) {
	db := testdb.Open(t)
	for _, key := range []string{"owner_emails", "token_encryption_key", "plan_horizon_dayz"} {
		if err := db.Create(&models.Setting{Key: key, Value: "nat@example.com"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	got, err := settings.New(db, seedCfg()).Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.PlanHorizonDays != settings.DefaultPlanHorizonDays {
		t.Errorf("an unknown key changed a value: %+v", got)
	}
}

// A corrupt value falls back to the default instead of failing the run.
func TestLoadIgnoresUnparseableValue(t *testing.T) {
	db := testdb.Open(t)
	if err := db.Create(&models.Setting{Key: settings.KeyPlanHorizonDays, Value: "soon"}).Error; err != nil {
		t.Fatal(err)
	}
	got, err := settings.New(db, seedCfg()).Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.PlanHorizonDays != settings.DefaultPlanHorizonDays {
		t.Errorf("horizon = %d, want the default", got.PlanHorizonDays)
	}
}

func TestValidate(t *testing.T) {
	base := settings.DefaultsFrom(nil)
	mutate := func(f func(v *settings.Values)) settings.Values {
		v := base
		f(&v)
		return v
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("defaults must be valid: %v", err)
	}
	cases := map[string]settings.Values{
		"threshold above one": mutate(func(v *settings.Values) { v.TriageConfidenceThreshold = 1.5 }),
		"negative threshold":  mutate(func(v *settings.Values) { v.TriageConfidenceThreshold = -0.1 }),
		"zero horizon":        mutate(func(v *settings.Values) { v.PlanHorizonDays = 0 }),
		"negative horizon":    mutate(func(v *settings.Values) { v.PlanHorizonDays = -30 }),
		"huge horizon":        mutate(func(v *settings.Values) { v.PlanHorizonDays = 4000 }),
		"zero backfill":       mutate(func(v *settings.Values) { v.TriageBackfillDays = 0 }),
		"min above max": mutate(func(v *settings.Values) {
			v.FocusBlockMinMinutes, v.FocusBlockMaxMinutes = 120, 60
		}),
		"tiny block":  mutate(func(v *settings.Values) { v.FocusBlockMinMinutes = 1 }),
		"blank title": mutate(func(v *settings.Values) { v.SoftEventTitles = []string{"Lunch", "  "} }),
	}
	for name, v := range cases {
		if err := v.Validate(); err == nil {
			t.Errorf("%s should be rejected", name)
		}
	}
}

// The plan window has to stay inside the synced events mirror. Past it the
// planner sees an empty calendar and books over meetings it can't see.
func TestValidateHorizonStopsAtTheMirror(t *testing.T) {
	mirrorDays := int(calendar.FutureWindow / (24 * time.Hour))
	v := settings.DefaultsFrom(nil)

	v.PlanHorizonDays = mirrorDays - 1
	if err := v.Validate(); err != nil {
		t.Errorf("horizon %d days should be allowed: %v", v.PlanHorizonDays, err)
	}
	for _, d := range []int{mirrorDays, mirrorDays + 1, 365} {
		v.PlanHorizonDays = d
		if err := v.Validate(); err == nil {
			t.Errorf("horizon %d days reaches past the %d-day mirror and should be rejected", d, mirrorDays)
		}
	}
}

// Save must refuse invalid values even when a caller skips Validate.
func TestSaveRejectsInvalid(t *testing.T) {
	db := testdb.Open(t)
	v := settings.DefaultsFrom(nil)
	v.PlanHorizonDays = 0
	if err := settings.New(db, nil).Save(t.Context(), v); err == nil {
		t.Fatal("Save should reject a non-positive horizon")
	}
	var n int64
	db.Model(&models.Setting{}).Count(&n)
	if n != 0 {
		t.Errorf("a rejected Save must write nothing, got %d rows", n)
	}
}
