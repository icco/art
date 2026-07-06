// Package db opens and migrates the Postgres database used by the art server.
package db

import (
	"fmt"
	"strings"
	"time"

	"github.com/icco/art/lib/models"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"moul.io/zapgorm2"
)

// Open connects to dsn, wires zap logging, and runs AutoMigrate.
func Open(dsn string, log *zap.Logger) (*gorm.DB, error) {
	// ErrRecordNotFound is expected control flow (e.g. a calendar's first
	// sync), not an error worth a stack trace.
	gormLog := zapgorm2.New(log)
	gormLog.IgnoreRecordNotFoundError = true
	cfg := &gorm.Config{Logger: gormLog, TranslateError: true}
	db, err := gorm.Open(postgres.Open(dsn), cfg)
	if err != nil {
		return nil, fmt.Errorf("gorm open: %w", err)
	}
	// Bound the pool; the Postgres instance is shared with other services.
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("sql db: %w", err)
	}
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	if err := dropSessionGlobalEventIndex(db); err != nil {
		return nil, fmt.Errorf("drop session event index: %w", err)
	}
	if err := db.AutoMigrate(models.All()...); err != nil {
		return nil, fmt.Errorf("auto-migrate: %w", err)
	}
	if err := migrateEmailCategories(db); err != nil {
		return nil, fmt.Errorf("migrate email categories: %w", err)
	}
	if err := dropArtCalendarColumn(db); err != nil {
		return nil, fmt.Errorf("drop art calendar column: %w", err)
	}
	if err := migrateKindConstraints(db); err != nil {
		return nil, fmt.Errorf("migrate kind constraints: %w", err)
	}
	return db, nil
}

// migrateKindConstraints widens the jobs.kind and agent_runs.kind CHECK
// constraints to admit 'reconcile'. AutoMigrate creates a missing constraint but
// never alters an existing one, so this is explicit. Idempotent: skips when the
// constraint already admits reconcile, safe on a fresh database.
func migrateKindConstraints(db *gorm.DB) error {
	widen := func(model any, table, name, def string) error {
		m := db.Migrator()
		if m.HasConstraint(model, name) {
			var cur string
			if err := db.Raw(
				`SELECT pg_get_constraintdef(oid) FROM pg_constraint
				 WHERE conname = ? AND conrelid = ?::regclass`, name, table).
				Scan(&cur).Error; err != nil {
				return err
			}
			if cur != "" && strings.Contains(cur, "reconcile") {
				return nil
			}
			if err := m.DropConstraint(model, name); err != nil {
				return err
			}
		}
		return db.Exec(def).Error
	}
	if err := widen(&models.Job{}, "jobs", "chk_jobs_kind",
		`ALTER TABLE jobs ADD CONSTRAINT chk_jobs_kind
		 CHECK (kind IN ('sync','reconcile','planner','triage'))`); err != nil {
		return err
	}
	return widen(&models.AgentRun{}, "agent_runs", "chk_agent_runs_kind",
		`ALTER TABLE agent_runs ADD CONSTRAINT chk_agent_runs_kind
		 CHECK (kind IN ('planner','triage','reconcile'))`)
}

// dropSessionGlobalEventIndex retires the table-global unique index on
// sessions.google_event_id (now unique per account+calendar). AutoMigrate
// never drops indexes, so this is explicit. Idempotent.
func dropSessionGlobalEventIndex(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasIndex(&models.Session{}, "idx_session_google_event") {
		return nil
	}
	return m.DropIndex(&models.Session{}, "idx_session_google_event")
}

// dropArtCalendarColumn removes the retired art_calendar_id column. Art always
// writes focus blocks to the account's primary calendar, so the separate
// art-calendar override no longer exists. AutoMigrate never drops columns, so
// this is explicit. Idempotent: a no-op once the column is gone.
func dropArtCalendarColumn(db *gorm.DB) error {
	m := db.Migrator()
	if !m.HasColumn(&models.Account{}, "art_calendar_id") {
		return nil
	}
	return m.DropColumn(&models.Account{}, "art_calendar_id")
}

// migrateEmailCategories remaps the retired 'read'/'thinking' categories to
// 'keep' and narrows the category CHECK constraint to the current taxonomy.
// AutoMigrate creates a missing constraint but never alters an existing one,
// so this swap is explicit. Idempotent: a no-op once applied, safe on a fresh
// database. GORM names the field check constraint chk_<table>_<column>.
func migrateEmailCategories(db *gorm.DB) error {
	if err := db.Exec(
		`UPDATE email_messages SET category = 'keep', action = 'keep'
		 WHERE category IN ('read', 'thinking')`).Error; err != nil {
		return err
	}

	const name = "chk_email_messages_category"
	m := db.Migrator()
	if m.HasConstraint(&models.EmailMessage{}, name) {
		var def string
		if err := db.Raw(
			`SELECT pg_get_constraintdef(oid) FROM pg_constraint
			 WHERE conname = ? AND conrelid = 'email_messages'::regclass`, name).
			Scan(&def).Error; err != nil {
			return err
		}
		// Skip only when the constraint already enforces the new set; an empty
		// or still-wide def falls through to drop + re-add rather than silently
		// leaving a stale constraint.
		if def != "" && !strings.Contains(def, "read") && !strings.Contains(def, "thinking") {
			return nil
		}
		if err := m.DropConstraint(&models.EmailMessage{}, name); err != nil {
			return err
		}
	}
	return db.Exec(
		`ALTER TABLE email_messages ADD CONSTRAINT chk_email_messages_category
		 CHECK (category IN ('archive', 'reply', 'keep'))`).Error
}
