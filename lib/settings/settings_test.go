package settings_test

import (
	"errors"
	"sync"
	"testing"

	"go.icco.me/art/lib/config"
	"go.icco.me/art/lib/models"
	"go.icco.me/art/lib/settings"
	"go.icco.me/art/lib/testdb"
)

func seedCfg() *config.Config {
	return &config.Config{
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
}

func TestSaveLoadRoundTrip(t *testing.T) {
	db := testdb.Open(t)
	store := settings.New(db, seedCfg())

	want := settings.Values{
		TriageEnabled:             false,
		TriageDryRun:              true,
		TriageConfidenceThreshold: 0.55,
		TriageBackfillDays:        3,
		TriageReconcileDays:       0,
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
	if got.TriageConfidenceThreshold != 0.55 || got.TriageBackfillDays != 3 {
		t.Errorf("numbers did not round-trip: %+v", got)
	}

	// A second Save upserts rather than duplicating rows.
	want.TriageBackfillDays = 21
	if err := store.Save(t.Context(), want); err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Model(&models.Setting{}).Count(&n)
	if n != int64(len(settings.Keys())) {
		t.Errorf("row count = %d, want %d", n, len(settings.Keys()))
	}
	got, _ = store.Load(t.Context())
	if got.TriageBackfillDays != 21 {
		t.Errorf("backfill after second save = %d, want 21", got.TriageBackfillDays)
	}
}

// A row for a key the store doesn't own must not reach Values, so a
// hand-inserted secret can never be served or acted on.
func TestLoadIgnoresUnknownKeys(t *testing.T) {
	db := testdb.Open(t)
	for _, key := range []string{"owner_emails", "token_encryption_key", "triage_backfill_dayz", "plan_horizon_days"} {
		if err := db.Create(&models.Setting{Key: key, Value: "nat@example.com"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	got, err := settings.New(db, seedCfg()).Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.TriageBackfillDays != 14 {
		t.Errorf("an unknown key changed a value: %+v", got)
	}
}

// A corrupt value falls back to the default instead of failing the run.
func TestLoadIgnoresUnparseableValue(t *testing.T) {
	db := testdb.Open(t)
	if err := db.Create(&models.Setting{Key: settings.KeyTriageBackfillDays, Value: "soon"}).Error; err != nil {
		t.Fatal(err)
	}
	got, err := settings.New(db, seedCfg()).Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if got.TriageBackfillDays != 14 {
		t.Errorf("backfill = %d, want the env default", got.TriageBackfillDays)
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
		"zero backfill":       mutate(func(v *settings.Values) { v.TriageBackfillDays = 0 }),
		"negative reconcile":  mutate(func(v *settings.Values) { v.TriageReconcileDays = -1 }),
		"huge budget":         mutate(func(v *settings.Values) { v.DailyBudgetUSD = 1000 }),
	}
	for name, v := range cases {
		if err := v.Validate(); err == nil {
			t.Errorf("%s should be rejected", name)
		}
	}
}

// Save must refuse invalid values even when a caller skips Validate.
func TestSaveRejectsInvalid(t *testing.T) {
	db := testdb.Open(t)
	v := settings.DefaultsFrom(nil)
	v.TriageBackfillDays = 0
	if err := settings.New(db, nil).Save(t.Context(), v); err == nil {
		t.Fatal("Save should reject a zero backfill")
	}
	var n int64
	db.Model(&models.Setting{}).Count(&n)
	if n != 0 {
		t.Errorf("a rejected Save must write nothing, got %d rows", n)
	}
}

func TestUpdateRoundTrip(t *testing.T) {
	db := testdb.Open(t)
	store := settings.New(db, seedCfg())

	got, err := store.Update(t.Context(), func(v *settings.Values) error {
		v.TriageBackfillDays = 30
		v.DailyBudgetUSD = 15.5
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.TriageBackfillDays != 30 || got.DailyBudgetUSD != 15.5 {
		t.Fatalf("unexpected updated values: %+v", got)
	}
	loaded, err := store.Load(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TriageBackfillDays != 30 || loaded.DailyBudgetUSD != 15.5 {
		t.Fatalf("unexpected loaded values: %+v", loaded)
	}
}

func TestUpdateRejectsInvalid(t *testing.T) {
	db := testdb.Open(t)
	store := settings.New(db, seedCfg())

	_, err := store.Update(t.Context(), func(v *settings.Values) error {
		v.TriageBackfillDays = 0
		return nil
	})
	if err == nil {
		t.Fatal("expected error on invalid setting")
	}
	if !errors.Is(err, settings.ErrInvalidSetting) {
		t.Fatalf("expected ErrInvalidSetting, got %v", err)
	}
	var n int64
	db.Model(&models.Setting{}).Count(&n)
	if n != 0 {
		t.Errorf("rejected Update must write nothing, got %d rows", n)
	}
}

func TestUpdateConcurrent(t *testing.T) {
	db := testdb.Open(t)
	store := settings.New(db, seedCfg())

	const workers = 4
	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for i := range workers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, err := store.Update(t.Context(), func(v *settings.Values) error {
				v.TriageBackfillDays = idx + 1
				return nil
			})
			if err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent update failed: %v", err)
	}
}
