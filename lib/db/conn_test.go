package db

import (
	"os"
	"testing"

	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/testdb"
	"go.uber.org/zap"
)

func TestOpenAndMigrate(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := Open(dsn, zap.NewNop())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("underlying *sql.DB: %v", err)
	}
	defer func() { _ = sqlDB.Close() }()
	if err := sqlDB.Ping(); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestOpenBadDSN(t *testing.T) {
	_, err := Open("postgres://nobody:bad@127.0.0.1:1/none?sslmode=disable&connect_timeout=1", zap.NewNop())
	if err == nil {
		t.Fatal("expected open to fail on unreachable DB")
	}
}

func TestMigrateEmailCategories(t *testing.T) {
	db := testdb.Open(t)

	// Simulate a pre-migration database by widening the constraint so the
	// retired values can be inserted.
	db.Exec(`ALTER TABLE email_messages DROP CONSTRAINT IF EXISTS chk_email_messages_category`)
	if err := db.Exec(`ALTER TABLE email_messages ADD CONSTRAINT chk_email_messages_category CHECK (category IN ('archive','reply','read','thinking','keep'))`).Error; err != nil {
		t.Fatalf("widen constraint: %v", err)
	}

	legacy := models.EmailMessage{
		RunID:          "00000000-0000-0000-0000-000000000001",
		AccountKind:    models.AccountPersonal,
		GmailMessageID: "leg1",
		Category:       models.EmailCategory("read"),
		Action:         models.EmailAction("read"),
	}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatalf("seed legacy row: %v", err)
	}

	if err := migrateEmailCategories(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var got models.EmailMessage
	if err := db.First(&got, "gmail_message_id = ?", "leg1").Error; err != nil {
		t.Fatal(err)
	}
	if got.Category != models.EmailKeep || got.Action != models.ActionKeep {
		t.Errorf("legacy row = %q/%q, want keep/keep", got.Category, got.Action)
	}

	// The narrowed constraint now rejects a retired value.
	if err := db.Exec(`UPDATE email_messages SET category='thinking' WHERE gmail_message_id='leg1'`).Error; err == nil {
		t.Error("narrowed constraint should reject category='thinking'")
	}

	// Idempotent: a second run is a no-op.
	if err := migrateEmailCategories(db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestMigrateKindConstraints(t *testing.T) {
	db := testdb.Open(t)

	// Simulate a pre-migration database whose kind constraints predate
	// 'reconcile'.
	db.Exec(`ALTER TABLE jobs DROP CONSTRAINT IF EXISTS chk_jobs_kind`)
	if err := db.Exec(`ALTER TABLE jobs ADD CONSTRAINT chk_jobs_kind CHECK (kind IN ('sync','planner','triage'))`).Error; err != nil {
		t.Fatalf("narrow jobs constraint: %v", err)
	}
	db.Exec(`ALTER TABLE agent_runs DROP CONSTRAINT IF EXISTS chk_agent_runs_kind`)
	if err := db.Exec(`ALTER TABLE agent_runs ADD CONSTRAINT chk_agent_runs_kind CHECK (kind IN ('planner','triage'))`).Error; err != nil {
		t.Fatalf("narrow agent_runs constraint: %v", err)
	}
	if err := db.Create(&models.Job{Kind: models.JobKind("reconcile"), Status: models.JobPending, MaxAttempts: 4}).Error; err == nil {
		t.Fatal("pre-migration constraint should reject a reconcile job")
	}

	if err := migrateKindConstraints(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// The jobs.kind constraint stays permissive to 'reconcile' (a retired job
	// kind) so no data-dependent narrowing migration is needed; DropRetiredKinds
	// clears any leftover rows at startup.
	if err := db.Create(&models.Job{Kind: models.JobKind("reconcile"), Status: models.JobPending, MaxAttempts: 4}).Error; err != nil {
		t.Fatalf("reconcile job should insert after migration: %v", err)
	}
	if err := db.Create(&models.AgentRun{Kind: models.AgentRunReconcile, Status: models.AgentRunRunning}).Error; err != nil {
		t.Fatalf("reconcile agent run should insert after migration: %v", err)
	}

	// Idempotent: a second run detects the widened constraint and no-ops.
	if err := migrateKindConstraints(db); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

func TestDropArtCalendarColumn(t *testing.T) {
	db := testdb.Open(t)

	// Simulate a pre-migration database that still has the retired column.
	if err := db.Exec(`ALTER TABLE accounts ADD COLUMN IF NOT EXISTS art_calendar_id varchar(255)`).Error; err != nil {
		t.Fatalf("add legacy column: %v", err)
	}
	if !db.Migrator().HasColumn(&models.Account{}, "art_calendar_id") {
		t.Fatal("setup: expected art_calendar_id to exist")
	}

	if err := dropArtCalendarColumn(db); err != nil {
		t.Fatalf("drop: %v", err)
	}
	if db.Migrator().HasColumn(&models.Account{}, "art_calendar_id") {
		t.Error("art_calendar_id should be dropped")
	}

	// Idempotent: a second run on a database without the column is a no-op.
	if err := dropArtCalendarColumn(db); err != nil {
		t.Fatalf("second drop: %v", err)
	}
}
