// Package db opens and migrates the Postgres database used by the art server.
package db

import (
	"fmt"
	"strings"

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
	cfg := &gorm.Config{Logger: gormLog}
	db, err := gorm.Open(postgres.Open(dsn), cfg)
	if err != nil {
		return nil, fmt.Errorf("gorm open: %w", err)
	}
	if err := db.AutoMigrate(models.All()...); err != nil {
		return nil, fmt.Errorf("auto-migrate: %w", err)
	}
	if err := migrateEmailCategories(db); err != nil {
		return nil, fmt.Errorf("migrate email categories: %w", err)
	}
	return db, nil
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
		if !strings.Contains(def, "thinking") {
			return nil // already narrowed
		}
		if err := m.DropConstraint(&models.EmailMessage{}, name); err != nil {
			return err
		}
	}
	return db.Exec(
		`ALTER TABLE email_messages ADD CONSTRAINT chk_email_messages_category
		 CHECK (category IN ('archive', 'reply', 'keep'))`).Error
}
