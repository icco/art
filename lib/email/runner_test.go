package email

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.icco.me/art/lib/config"
	"go.icco.me/art/lib/models"
	"go.icco.me/art/lib/oauth"
	"go.icco.me/art/lib/settings"
	"go.icco.me/art/lib/testdb"
	gutillog "go.icco.me/gutil/logging"
)

func TestRunAllDisabled(t *testing.T) {
	db := testdb.Open(t)
	log, err := gutillog.NewLogger("test")
	if err != nil {
		t.Fatal(err)
	}
	ctx := gutillog.NewContext(context.Background(), log)

	cfg := &config.Config{Triage: config.TriageConfig{Enabled: false}}
	r := &Runner{Cfg: cfg, DB: db, Settings: settings.New(db, cfg)}
	if err := r.RunAll(ctx); err != nil {
		t.Fatalf("disabled RunAll: %v", err)
	}
	var n int64
	if err := db.Model(&models.AgentRun{}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("disabled triage created %d agent_runs, want 0", n)
	}
}

func TestRunAllSkipsWhenLockHeld(t *testing.T) {
	db := testdb.Open(t)
	log, err := gutillog.NewLogger("test")
	if err != nil {
		t.Fatal(err)
	}
	ctx := gutillog.NewContext(context.Background(), log)

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	var locked bool
	if err := conn.QueryRowContext(ctx, "SELECT pg_try_advisory_lock($1)", triageLockKey).Scan(&locked); err != nil || !locked {
		t.Fatalf("test could not take lock: %v %v", locked, err)
	}

	cfg := &config.Config{Triage: config.TriageConfig{Enabled: true}}
	r := &Runner{Cfg: cfg, DB: db, Settings: settings.New(db, cfg)}
	if err := r.RunAll(ctx); err != nil {
		t.Fatalf("locked RunAll should skip cleanly: %v", err)
	}
	var n int64
	if err := db.Model(&models.AgentRun{}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("RunAll did work while another pass held the lock (%d runs)", n)
	}
}

// Classifying nothing while having mail to classify is the shape a broken
// classifier takes; it used to report success.
func TestTriageWedged(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name                           string
		processed, errs, budgetStopped int
		want                           bool
	}{
		{"quiet inbox", 0, 0, 0, false},
		{"everything classified", 5, 0, 0, false},
		{"a few bad messages", 4, 2, 0, false},
		{"one message, and it failed", 0, 1, 0, true},
		{"every message failed", 0, 37, 0, true},
		{"budget spent before classifying anything", 0, 3, 1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := triageWedged(tt.processed, tt.errs, tt.budgetStopped)
			if got := err != nil; got != tt.want {
				t.Errorf("triageWedged(%d, %d, %d) = %v, want wedged=%v",
					tt.processed, tt.errs, tt.budgetStopped, err, tt.want)
			}
		})
	}
}

func TestTriageRunError(t *testing.T) {
	t.Parallel()
	wedgedErr := errClassifiedNone
	tests := []struct {
		name          string
		processed     int
		runErrs       []string
		budgetStopped int
		wedged        error
		wantErr       bool
	}{
		{"quiet inbox", 0, nil, 0, nil, false},
		{"processed mail", 3, nil, 0, nil, false},
		{"processed mail with account error", 3, []string{"work: client: failed"}, 0, nil, false},
		{"budget stopped upfront", 0, nil, 1, nil, false},
		{"budget stopped mid-run", 0, nil, 1, nil, false},
		{"wedged error", 0, nil, 0, wedgedErr, true},
		{"setup error classifier", 0, []string{"classifier: init failed"}, 0, nil, true},
		{"setup error gmail client", 0, []string{"personal: client: oauth error"}, 0, nil, true},
		{"setup error run account", 0, []string{"personal: ensure labels: timeout"}, 0, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := triageRunError(tt.processed, tt.runErrs, tt.budgetStopped, tt.wedged)
			if got := err != nil; got != tt.wantErr {
				t.Errorf("triageRunError(%d, %v, %d, %v) = %v, want err=%v",
					tt.processed, tt.runErrs, tt.budgetStopped, tt.wedged, err, tt.wantErr)
			}
		})
	}
}

func TestFinishRecordsSummary(t *testing.T) {
	db := testdb.Open(t)
	r := &Runner{Cfg: &config.Config{Triage: config.TriageConfig{DryRun: true}}, DB: db}

	run := models.AgentRun{Kind: models.AgentRunTriage, Status: models.AgentRunRunning}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}

	counts := map[string]int{"archive": 3, "reply": 1}
	if err := r.finish(context.Background(), run.ID, true, counts, nil, 100, 40); err != nil {
		t.Fatal(err)
	}

	var got models.AgentRun
	if err := db.First(&got, "id = ?", run.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != models.AgentRunSucceeded {
		t.Errorf("status = %q, want succeeded", got.Status)
	}
	if got.TokensIn != 100 || got.TokensOut != 40 {
		t.Errorf("tokens = %d/%d, want 100/40", got.TokensIn, got.TokensOut)
	}
	var summary map[string]any
	if err := json.Unmarshal(got.Summary, &summary); err != nil {
		t.Fatal(err)
	}
	if summary["archive"].(float64) != 3 || summary["dry_run"] != true {
		t.Errorf("summary = %v", summary)
	}

	// A run with errors is marked failed.
	run2 := models.AgentRun{Kind: models.AgentRunTriage, Status: models.AgentRunRunning}
	if err := db.Create(&run2).Error; err != nil {
		t.Fatal(err)
	}
	if err := r.finish(context.Background(), run2.ID, true, map[string]int{}, []string{"boom"}, 0, 0); err != nil {
		t.Fatal(err)
	}
	var got2 models.AgentRun
	_ = db.First(&got2, "id = ?", run2.ID).Error
	if got2.Status != models.AgentRunFailed || got2.Error != "boom" {
		t.Errorf("errored run: status=%q error=%q", got2.Status, got2.Error)
	}
}

func TestRunAllBudgetExhausted(t *testing.T) {
	db := testdb.Open(t)
	log, err := gutillog.NewLogger("test")
	if err != nil {
		t.Fatal(err)
	}
	ctx := gutillog.NewContext(context.Background(), log)

	cfg := &config.Config{
		Timezone: time.UTC,
		Triage: config.TriageConfig{
			Enabled:             true,
			BackfillDays:        config.DefaultTriageBackfillDays,
			ReconcileDays:       config.DefaultTriageReconcileDays,
			ConfidenceThreshold: config.DefaultTriageConfidenceThreshold,
		},
	}
	pastRun := models.AgentRun{
		Kind:      models.AgentRunTriage,
		StartedAt: time.Now(),
		Status:    models.AgentRunSucceeded,
		Model:     config.TriageModel,
		TokensIn:  100_000,
		TokensOut: 100_000,
	}
	if err := db.Create(&pastRun).Error; err != nil {
		t.Fatal(err)
	}

	st := settings.New(db, cfg)
	vals, err := st.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	vals.DailyBudgetUSD = 0.01
	vals.TriageEnabled = true
	if err := st.Save(ctx, vals); err != nil {
		t.Fatal(err)
	}
	r := &Runner{Cfg: cfg, DB: db, Settings: st}
	if err := r.RunAll(ctx); err != nil {
		t.Fatalf("budget exhausted RunAll should succeed, got: %v", err)
	}

	var latest models.AgentRun
	if err := db.Order("started_at desc").First(&latest).Error; err != nil {
		t.Fatal(err)
	}
	if latest.Status != models.AgentRunSucceeded {
		t.Errorf("status = %q, want succeeded", latest.Status)
	}
	var summary map[string]any
	if err := json.Unmarshal(latest.Summary, &summary); err != nil {
		t.Fatal(err)
	}
	if summary["budget_stopped"].(float64) < 1 {
		t.Errorf("expected budget_stopped in summary: %v", summary)
	}
}

func TestRunAllFailsOnSetupError(t *testing.T) {
	db := testdb.Open(t)
	log, err := gutillog.NewLogger("test")
	if err != nil {
		t.Fatal(err)
	}
	ctx := gutillog.NewContext(context.Background(), log)

	acc := models.Account{
		Kind:                  models.AccountPersonal,
		Email:                 "test@example.com",
		RefreshTokenEncrypted: []byte("invalid-token"),
	}
	if err := db.Create(&acc).Error; err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Timezone: time.UTC,
		Triage: config.TriageConfig{
			Enabled:             true,
			BackfillDays:        config.DefaultTriageBackfillDays,
			ReconcileDays:       config.DefaultTriageReconcileDays,
			ConfidenceThreshold: config.DefaultTriageConfidenceThreshold,
		},
	}
	st := settings.New(db, cfg)
	vals, err := st.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	vals.TriageEnabled = true
	if err := st.Save(ctx, vals); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GEMINI_API_KEY", "test-key")
	sealer, err := oauth.NewSealer(bytes.Repeat([]byte{0x02}, 32))
	if err != nil {
		t.Fatal(err)
	}
	flow := oauth.NewFlow("cid", "csec", "http://localhost/cb", &oauth.Store{DB: db, Sealer: sealer})
	r := &Runner{Cfg: cfg, DB: db, Settings: st, OAuth: flow}
	err = r.RunAll(ctx)
	if err == nil {
		t.Fatal("expected error from RunAll when account client setup fails, got nil")
	}

	var latest models.AgentRun
	if err := db.Order("started_at desc").First(&latest).Error; err != nil {
		t.Fatal(err)
	}
	if latest.Status != models.AgentRunFailed {
		t.Errorf("status = %q, want failed", latest.Status)
	}
	if !strings.HasPrefix(latest.Error, "personal: client:") {
		t.Errorf("error = %q, want the personal account client failure", latest.Error)
	}
}
