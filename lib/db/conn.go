// Package db opens and migrates the Postgres database used by the art server.
package db

import (
	"fmt"

	"github.com/icco/art/lib/models"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"moul.io/zapgorm2"
)

// Open connects to dsn, wires zap logging, and runs AutoMigrate.
func Open(dsn string, log *zap.Logger) (*gorm.DB, error) {
	cfg := &gorm.Config{Logger: zapgorm2.New(log)}
	db, err := gorm.Open(postgres.Open(dsn), cfg)
	if err != nil {
		return nil, fmt.Errorf("gorm open: %w", err)
	}
	// AutoMigrate never alters an existing CHECK constraint, so a database
	// created before the Task model keeps the old two-value sessions.source
	// check forever. Drop it here and let AutoMigrate recreate it from the tag.
	if db.Migrator().HasTable(&models.Session{}) {
		if err := db.Exec(`ALTER TABLE sessions DROP CONSTRAINT IF EXISTS chk_sessions_source`).Error; err != nil {
			return nil, fmt.Errorf("drop sessions source check: %w", err)
		}
	}
	if err := db.AutoMigrate(models.All()...); err != nil {
		return nil, fmt.Errorf("auto-migrate: %w", err)
	}
	return db, nil
}
