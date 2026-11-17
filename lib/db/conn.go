// Package db opens and migrates the Postgres database used by the art server.
package db

import (
	"fmt"
	"strings"
	"time"

	"go.icco.me/art/lib/models"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormtracing "gorm.io/plugin/opentelemetry/tracing"
	"moul.io/zapgorm2"
)

// Open connects to dsn, wires zap logging, and runs AutoMigrate.
func Open(dsn string, log *zap.Logger) (*gorm.DB, error) {
	// ErrRecordNotFound is expected control flow, not an error worth a stack trace.
	gormLog := zapgorm2.New(log)
	gormLog.IgnoreRecordNotFoundError = true
	cfg := &gorm.Config{Logger: gormLog, TranslateError: true}
	db, err := gorm.Open(postgres.Open(dsn), cfg)
	if err != nil {
		return nil, fmt.Errorf("gorm open: %w", err)
	}
	if err := db.Use(gormtracing.NewPlugin(gormtracing.WithoutMetrics(), gormtracing.WithoutQueryVariables())); err != nil {
		return nil, fmt.Errorf("gorm tracing plugin: %w", err)
	}
	// Bound the pool; the Postgres instance is shared with other services.
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("sql db: %w", err)
	}
	sqlDB.SetMaxOpenConns(10)
	sqlDB.SetMaxIdleConns(5)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	if err := db.AutoMigrate(models.All()...); err != nil {
		return nil, fmt.Errorf("auto-migrate: %w", err)
	}
	if err := migrateEmailCategories(db); err != nil {
		return nil, fmt.Errorf("migrate email categories: %w", err)
	}
	if err := dropScheduling(db); err != nil {
		return nil, fmt.Errorf("drop scheduling schema: %w", err)
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
//
// Reconcile is retired as a job kind but stays an agent-run kind; the jobs
// constraint is left permissive to avoid a narrowing migration. JobKinds() is
// the source of truth.
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

// dropScheduling removes the tables and account columns of the retired calendar
// planner. AutoMigrate never drops anything, so this is explicit. Idempotent.
func dropScheduling(db *gorm.DB) error {
	m := db.Migrator()
	for _, col := range []string{"art_calendar_id", "primary_calendar_id"} {
		if m.HasColumn(&models.Account{}, col) {
			if err := m.DropColumn(&models.Account{}, col); err != nil {
				return err
			}
		}
	}
	return m.DropTable("sessions", "habits", "projects", "working_hours", "events", "sync_states")
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
