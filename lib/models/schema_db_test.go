package models_test

import (
	"testing"
	"time"

	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/testdb"
)

// Google event IDs are unique per calendar, not globally: the same invite on
// both accounts shares an ID and must be storable twice.
func TestSessionEventIDUniquePerCalendar(t *testing.T) {
	db := testdb.Open(t)
	id := "evshared"
	mk := func(kind models.AccountKind, cal string) models.Session {
		return models.Session{
			Source: models.SourceProject, SourceID: "11111111-1111-1111-1111-111111111111",
			AccountKind: kind, CalendarID: cal, GoogleEventID: &id,
			ScheduledStart: time.Now(), ScheduledEnd: time.Now().Add(time.Hour),
			Status: models.SessionPlanned,
		}
	}
	a := mk(models.AccountWork, "work-cal")
	if err := db.Create(&a).Error; err != nil {
		t.Fatal(err)
	}
	b := mk(models.AccountPersonal, "personal-cal")
	if err := db.Create(&b).Error; err != nil {
		t.Fatalf("same event id on another calendar must be allowed: %v", err)
	}
	dup := mk(models.AccountWork, "work-cal")
	if err := db.Create(&dup).Error; err == nil {
		t.Fatal("duplicate (account, calendar, event) must still be rejected")
	}
}

// The handler validates end > start, but agent code writes rows directly.
func TestWorkingHourRejectsInvertedWindow(t *testing.T) {
	db := testdb.Open(t)
	err := db.Create(&models.WorkingHour{
		SlotKind: models.SlotWork, DayOfWeek: 1, StartMinute: 600, EndMinute: 540,
	}).Error
	if err == nil {
		t.Fatal("inverted working-hours window must be rejected by the schema")
	}
}
