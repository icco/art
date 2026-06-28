package email

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/icco/art/lib/config"
	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/testdb"
	gutillog "github.com/icco/gutil/logging"
)

func TestRunAllDisabled(t *testing.T) {
	db := testdb.Open(t)
	log, err := gutillog.NewLogger("test")
	if err != nil {
		t.Fatal(err)
	}
	ctx := gutillog.NewContext(context.Background(), log)

	r := &Runner{Cfg: &config.Config{Triage: config.TriageConfig{Enabled: false}}, DB: db}
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

func TestFinishRecordsSummary(t *testing.T) {
	db := testdb.Open(t)
	r := &Runner{Cfg: &config.Config{Triage: config.TriageConfig{DryRun: true}}, DB: db}

	run := models.AgentRun{Kind: models.AgentRunTriage, Status: models.AgentRunRunning}
	if err := db.Create(&run).Error; err != nil {
		t.Fatal(err)
	}

	counts := map[string]int{"archive": 3, "reply": 1}
	if err := r.finish(context.Background(), run.ID, counts, nil, 100, 40); err != nil {
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
	if err := r.finish(context.Background(), run2.ID, map[string]int{}, []string{"boom"}, 0, 0); err != nil {
		t.Fatal(err)
	}
	var got2 models.AgentRun
	_ = db.First(&got2, "id = ?", run2.ID).Error
	if got2.Status != models.AgentRunFailed || got2.Error != "boom" {
		t.Errorf("errored run: status=%q error=%q", got2.Status, got2.Error)
	}
}
