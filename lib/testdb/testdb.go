// Package testdb provides a Postgres-backed *gorm.DB for tests, skipping
// when TEST_DATABASE_URL is unset.
package testdb

import (
	"os"
	"testing"

	"github.com/icco/art/lib/models"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Open returns a fresh *gorm.DB with all tables dropped and re-migrated.
// Skips the test when TEST_DATABASE_URL is unset.
func Open(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	for _, m := range models.All() {
		_ = db.Migrator().DropTable(m)
	}
	if err := db.AutoMigrate(models.All()...); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	})
	return db
}
