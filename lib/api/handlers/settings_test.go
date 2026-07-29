package handlers_test

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/icco/art/lib/api/handlers"
	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/settings"
	"github.com/icco/art/lib/testdb"
	"gorm.io/gorm"
)

func settingsRouter(db *gorm.DB) http.Handler {
	h := &handlers.Handlers{DB: db, Settings: settings.New(db, nil)}
	r := chi.NewRouter()
	r.Get("/settings", h.SettingsGet)
	r.Put("/settings", h.SettingsUpdate)
	r.Get("/working-hours", h.WorkingHoursList)
	r.Put("/working-hours", h.WorkingHoursReplace)
	r.Patch("/working-hours/{kind}/{day}", h.WorkingHoursPatchDay)
	return r
}

func TestSettingsGetAndUpdate(t *testing.T) {
	db := testdb.Open(t)
	r := settingsRouter(db)

	var got settings.Values
	w := do(t, r, "GET", "/settings", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get: %d %s", w.Code, w.Body)
	}
	mustDecode(t, w, &got)
	if got.PlanHorizonDays != settings.DefaultPlanHorizonDays {
		t.Fatalf("defaults not served: %+v", got)
	}

	w = do(t, r, "PUT", "/settings", map[string]any{
		"plan_horizon_days": 7, "focus_block_max_minutes": 120,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	mustDecode(t, w, &got)
	if got.PlanHorizonDays != 7 || got.FocusBlockMaxMinutes != 120 {
		t.Errorf("update not applied: %+v", got)
	}
	// An absent field keeps its value rather than resetting to zero.
	if got.FocusBlockMinMinutes != settings.DefaultFocusBlockMinMinutes {
		t.Errorf("absent field was overwritten: %+v", got)
	}
	if got.TriageBackfillDays == 0 {
		t.Error("absent triage days must not zero out")
	}
}

func TestSettingsUpdateValidation(t *testing.T) {
	db := testdb.Open(t)
	r := settingsRouter(db)

	bad := []map[string]any{
		{"plan_horizon_days": 0},
		{"plan_horizon_days": -1},
		{"triage_confidence_threshold": 4},
		{"focus_block_min_minutes": 120, "focus_block_max_minutes": 30},
		{"triage_backfill_days": 0},
		{"soft_event_titles": []string{"Lunch", " "}},
	}
	for _, body := range bad {
		if w := do(t, r, "PUT", "/settings", body); w.Code != http.StatusBadRequest {
			t.Errorf("%v should 400, got %d %s", body, w.Code, w.Body)
		}
	}
	// A non-numeric threshold never decodes.
	if w := do(t, r, "PUT", "/settings", map[string]any{"triage_confidence_threshold": "high"}); w.Code != http.StatusBadRequest {
		t.Errorf("unparseable threshold should 400, got %d", w.Code)
	}
}

// The typed request struct is the allowlist: an unknown or secret key is
// rejected outright and never stored.
func TestSettingsUpdateRejectsSecretKeys(t *testing.T) {
	db := testdb.Open(t)
	r := settingsRouter(db)

	for _, key := range []string{
		"owner_emails", "oidc_audience", "token_encryption_key", "database_url",
		"google_oauth_client_secret", "vertex_project_id", "timezone", "nonsense",
	} {
		w := do(t, r, "PUT", "/settings", map[string]any{key: "x"})
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s should be rejected, got %d %s", key, w.Code, w.Body)
		}
		var n int64
		db.Model(&models.Setting{}).Where("key = ?", key).Count(&n)
		if n != 0 {
			t.Errorf("%s reached the settings table", key)
		}
	}
}

func TestWorkingHoursPatchDay(t *testing.T) {
	db := testdb.Open(t)
	r := settingsRouter(db)

	w := do(t, r, "PUT", "/working-hours", []map[string]any{
		{"slot_kind": "work", "day_of_week": 1, "start_minute": 540, "end_minute": 1080},
		{"slot_kind": "work", "day_of_week": 2, "start_minute": 540, "end_minute": 1080},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("seed: %d %s", w.Code, w.Body)
	}

	// Two windows on one day, and Tuesday must be left alone.
	w = do(t, r, "PATCH", "/working-hours/work/1", []map[string]any{
		{"start_minute": 540, "end_minute": 720},
		{"start_minute": 780, "end_minute": 1080},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	var got []models.WorkingHour
	mustDecode(t, w, &got)
	if len(got) != 3 {
		t.Fatalf("want 3 rows (2 Monday + 1 Tuesday), got %d: %+v", len(got), got)
	}

	// An empty list clears just that day.
	w = do(t, r, "PATCH", "/working-hours/work/1", []map[string]any{})
	if w.Code != http.StatusOK {
		t.Fatalf("clear: %d %s", w.Code, w.Body)
	}
	mustDecode(t, w, &got)
	if len(got) != 1 || got[0].DayOfWeek != 2 {
		t.Fatalf("clear should leave Tuesday only, got %+v", got)
	}
}

func TestWorkingHoursPatchDayRejectsOverlapAndBadPath(t *testing.T) {
	db := testdb.Open(t)
	r := settingsRouter(db)

	// Overlapping submitted windows are rejected against the resulting set.
	w := do(t, r, "PATCH", "/working-hours/work/1", []map[string]any{
		{"start_minute": 540, "end_minute": 780},
		{"start_minute": 700, "end_minute": 1080},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("overlap should 400: %d %s", w.Code, w.Body)
	}
	var n int64
	db.Model(&models.WorkingHour{}).Count(&n)
	if n != 0 {
		t.Errorf("a rejected patch must write nothing, got %d rows", n)
	}

	// Bad path params and windows are client errors, not 404s.
	cases := []struct {
		path string
		body any
	}{
		{"/working-hours/moon/1", []map[string]any{}},
		{"/working-hours/work/9", []map[string]any{}},
		{"/working-hours/work/-1", []map[string]any{}},
		{"/working-hours/work/1", []map[string]any{{"start_minute": 600, "end_minute": 600}}},
		{"/working-hours/work/1", []map[string]any{{"start_minute": 600, "end_minute": 1441}}},
	}
	for _, tc := range cases {
		if w := do(t, r, "PATCH", tc.path, tc.body); w.Code != http.StatusBadRequest {
			t.Errorf("PATCH %s %v: got %d, want 400", tc.path, tc.body, w.Code)
		}
	}
}
