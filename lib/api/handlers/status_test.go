package handlers_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/icco/art/lib/models"
)

func TestStatus(t *testing.T) {
	h := taskHandlers(t)
	r := chi.NewRouter()
	r.Get("/status", h.Status)

	task := models.Task{Title: "pack office", Kind: models.SlotPersonal, DurationMinutes: 120, Status: models.TaskScheduled}
	pending := models.Task{Title: "call dentist", Kind: models.SlotPersonal, DurationMinutes: 30, Status: models.TaskPending}
	stuck := models.Task{Title: "impossible", Kind: models.SlotPersonal, DurationMinutes: 60, Status: models.TaskUnschedulable}
	for _, m := range []*models.Task{&task, &pending, &stuck} {
		if err := h.DB.Create(m).Error; err != nil {
			t.Fatal(err)
		}
	}
	evID := "ev1"
	start := time.Now().Add(4 * time.Hour).Truncate(time.Hour)
	if err := h.DB.Create(&models.Session{
		Source: models.SourceTask, SourceID: task.ID,
		AccountKind: models.AccountPersonal, CalendarID: "primary",
		GoogleEventID:  &evID,
		ScheduledStart: start, ScheduledEnd: start.Add(2 * time.Hour),
		Status: models.SessionPlanned,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.DB.Create(&models.AgentRun{
		StartedAt: time.Now(), Status: models.AgentRunSucceeded, Model: "deterministic",
		Summary: []byte(`{"tasks_scheduled":1}`),
	}).Error; err != nil {
		t.Fatal(err)
	}

	w := do(t, r, "GET", "/status", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status: %d %s", w.Code, w.Body)
	}
	var got struct {
		Upcoming []struct {
			Source string    `json:"source"`
			Title  string    `json:"title"`
			Start  time.Time `json:"start"`
			Status string    `json:"status"`
		} `json:"upcoming"`
		TasksPending       []models.Task    `json:"tasks_pending"`
		TasksUnschedulable []models.Task    `json:"tasks_unschedulable"`
		LastRun            *models.AgentRun `json:"last_run"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Upcoming) != 1 || got.Upcoming[0].Title != "pack office" {
		t.Fatalf("upcoming: %+v", got.Upcoming)
	}
	if len(got.TasksPending) != 1 || got.TasksPending[0].Title != "call dentist" {
		t.Fatalf("tasks_pending: %+v", got.TasksPending)
	}
	if len(got.TasksUnschedulable) != 1 || got.TasksUnschedulable[0].Title != "impossible" {
		t.Fatalf("tasks_unschedulable: %+v", got.TasksUnschedulable)
	}
	if got.LastRun == nil || got.LastRun.Model != "deterministic" {
		t.Fatalf("last_run: %+v", got.LastRun)
	}
}
