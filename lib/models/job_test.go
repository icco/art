package models_test

import (
	"testing"
	"time"

	"go.icco.me/art/lib/models"
	"go.icco.me/art/lib/testdb"
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

func TestJobKinds(t *testing.T) {
	got := models.JobKinds()
	want := []models.JobKind{models.JobTriage}
	if len(got) != len(want) {
		t.Fatalf("JobKinds len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("JobKinds[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	for _, k := range []string{"sync", "planner", "reconcile"} {
		if models.JobKind(k).Valid() {
			t.Fatalf("%s is a retired job kind", k)
		}
	}
}

func TestJobOnePendingPerKind(t *testing.T) {
	db := testdb.Open(t)
	mk := func(status models.JobStatus) error {
		return db.Create(&models.Job{
			Kind: models.JobTriage, Status: status, RunAt: time.Now(), MaxAttempts: 4,
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
}
