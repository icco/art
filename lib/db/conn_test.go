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
