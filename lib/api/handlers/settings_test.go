package handlers_test

import (
	"net/http"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.icco.me/art/lib/api/handlers"
	"go.icco.me/art/lib/models"
	"go.icco.me/art/lib/settings"
	"go.icco.me/art/lib/testdb"
	"gorm.io/gorm"
)

func settingsRouter(db *gorm.DB) http.Handler {
	h := &handlers.Handlers{DB: db, Settings: settings.New(db, nil)}
	r := chi.NewRouter()
	r.Get("/settings", h.SettingsGet)
	r.Put("/settings", h.SettingsUpdate)
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
	if got.TriageBackfillDays != 7 {
		t.Fatalf("defaults not served: %+v", got)
	}

	w = do(t, r, "PUT", "/settings", map[string]any{
		"triage_backfill_days": 3, "daily_budget_usd": 5,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	mustDecode(t, w, &got)
	if got.TriageBackfillDays != 3 || got.DailyBudgetUSD != 5 {
		t.Errorf("update not applied: %+v", got)
	}
	// An absent field keeps its value rather than resetting to zero.
	if got.TriageReconcileDays != 7 || got.TriageConfidenceThreshold != 0.8 {
		t.Errorf("absent field was overwritten: %+v", got)
	}
}

func TestSettingsUpdateConcurrent(t *testing.T) {
	db := testdb.Open(t)
	r := settingsRouter(db)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		w := do(t, r, "PUT", "/settings", map[string]any{"triage_backfill_days": 3})
		if w.Code != http.StatusOK {
			t.Errorf("update 1 failed: %d %s", w.Code, w.Body)
		}
	}()
	go func() {
		defer wg.Done()
		w := do(t, r, "PUT", "/settings", map[string]any{"daily_budget_usd": 5})
		if w.Code != http.StatusOK {
			t.Errorf("update 2 failed: %d %s", w.Code, w.Body)
		}
	}()
	wg.Wait()

	var got settings.Values
	w := do(t, r, "GET", "/settings", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get: %d %s", w.Code, w.Body)
	}
	mustDecode(t, w, &got)
	if got.TriageBackfillDays != 3 {
		t.Errorf("triage_backfill_days was clobbered: %d", got.TriageBackfillDays)
	}
	if got.DailyBudgetUSD != 5 {
		t.Errorf("daily_budget_usd was clobbered: %v", got.DailyBudgetUSD)
	}
}

func TestSettingsUpdateValidation(t *testing.T) {
	db := testdb.Open(t)
	r := settingsRouter(db)

	bad := []map[string]any{
		{"triage_confidence_threshold": 4},
		{"triage_backfill_days": 0},
		{"daily_budget_usd": -1},
		{"plan_horizon_days": 7},
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
