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
	return db, nil
}
