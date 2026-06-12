package db

import (
	"os"
	"testing"

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
