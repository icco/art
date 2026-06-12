package db

import (
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/icco/art/lib/models"
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

// TestOpenWidensSessionSourceConstraint simulates a database that predates the
// Task model (sessions.source CHECK only allows project/habit) and verifies
// that Open widens the constraint so task sessions insert cleanly. AutoMigrate
// alone never alters an existing CHECK constraint.
func TestOpenWidensSessionSourceConstraint(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := Open(dsn, zap.NewNop())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	// Regress the constraint to its pre-Task shape.
	if err := db.Exec(`ALTER TABLE sessions DROP CONSTRAINT IF EXISTS chk_sessions_source`).Error; err != nil {
		t.Fatalf("drop constraint: %v", err)
	}
	if err := db.Exec(`ALTER TABLE sessions ADD CONSTRAINT chk_sessions_source CHECK (source IN ('project','habit'))`).Error; err != nil {
		t.Fatalf("add old constraint: %v", err)
	}

	db2, err := Open(dsn, zap.NewNop())
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	sess := models.Session{
		Source:         models.SourceTask,
		SourceID:       uuid.NewString(),
		AccountKind:    models.AccountPersonal,
		CalendarID:     "primary",
		ScheduledStart: time.Now(),
		ScheduledEnd:   time.Now().Add(time.Hour),
		Status:         models.SessionPlanned,
	}
	if err := db2.Create(&sess).Error; err != nil {
		t.Fatalf("insert task session after Open: %v", err)
	}
	t.Cleanup(func() { db2.Delete(&sess) })
}

func TestOpenBadDSN(t *testing.T) {
	_, err := Open("postgres://nobody:bad@127.0.0.1:1/none?sslmode=disable&connect_timeout=1", zap.NewNop())
	if err == nil {
		t.Fatal("expected open to fail on unreachable DB")
	}
}
