package models_test

import (
	"testing"
	"time"

	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/testdb"
)

func TestJobKindValid(t *testing.T) {
	for _, k := range models.JobKinds() {
		if !k.Valid() {
			t.Errorf("kind %q should be valid", k)
		}
	}
	if models.JobKind("bogus").Valid() {
		t.Error("bogus kind should be invalid")
	}
	if models.JobStatus("bogus").Valid() {
		t.Error("bogus status should be invalid")
	}
	if !models.JobPending.Valid() {
		t.Error("pending should be valid")
	}
}

func TestJobOnePendingPerKind(t *testing.T) {
	db := testdb.Open(t)
	mk := func(status models.JobStatus) error {
		return db.Create(&models.Job{
			Kind: models.JobSync, Status: status, RunAt: time.Now(), MaxAttempts: 4,
		}).Error
	}
	if err := mk(models.JobSucceeded); err != nil {
		t.Fatalf("terminal row: %v", err)
	}
	if err := mk(models.JobPending); err != nil {
		t.Fatalf("first pending: %v", err)
	}
	if err := mk(models.JobPending); err == nil {
		t.Fatal("second pending job of same kind should violate the partial unique index")
	}
	if err := db.Create(&models.Job{
		Kind: models.JobTriage, Status: models.JobPending, RunAt: time.Now(), MaxAttempts: 4,
	}).Error; err != nil {
		t.Fatalf("pending job of another kind: %v", err)
	}
}
