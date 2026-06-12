package handlers_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/icco/art/lib/api/handlers"
	"github.com/icco/art/lib/config"
	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/testdb"
)

func newTaskRouter(h *handlers.Handlers) http.Handler {
	r := chi.NewRouter()
	r.Get("/tasks", h.TasksList)
	r.Post("/tasks", h.TasksCreate)
	r.Post("/tasks/quickadd", h.TasksQuickAdd)
	r.Patch("/tasks/{id}", h.TasksUpdate)
	r.Delete("/tasks/{id}", h.TasksDelete)
	return r
}

func taskHandlers(t *testing.T) *handlers.Handlers {
	t.Helper()
	tz, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	return &handlers.Handlers{DB: testdb.Open(t), Cfg: &config.Config{Timezone: tz}}
}

func TestTasksCRUD(t *testing.T) {
	h := taskHandlers(t)
	r := newTaskRouter(h)

	w := do(t, r, "POST", "/tasks", map[string]any{
		"title": "pack office", "kind": "personal", "duration_minutes": 120,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var created models.Task
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.ID == "" || created.Status != models.TaskPending {
		t.Fatalf("created task: %+v", created)
	}

	w = do(t, r, "GET", "/tasks", nil)
	var list []models.Task
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != http.StatusOK || len(list) != 1 {
		t.Fatalf("list: %d, %d tasks", w.Code, len(list))
	}

	// Status filter.
	w = do(t, r, "GET", "/tasks?status=done", nil)
	list = nil
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != http.StatusOK || len(list) != 0 {
		t.Fatalf("filtered list: %d, %d tasks", w.Code, len(list))
	}

	w = do(t, r, "PATCH", "/tasks/"+created.ID, map[string]any{"status": "done"})
	if w.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}

	w = do(t, r, "DELETE", "/tasks/"+created.ID, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", w.Code)
	}
	if w := do(t, r, "DELETE", "/tasks/"+created.ID, nil); w.Code != http.StatusNotFound {
		t.Fatalf("double delete: %d", w.Code)
	}
}

func TestTasksValidation(t *testing.T) {
	h := taskHandlers(t)
	r := newTaskRouter(h)

	for name, body := range map[string]map[string]any{
		"missing title":    {"kind": "personal", "duration_minutes": 30},
		"bad kind":         {"title": "x", "kind": "leisure", "duration_minutes": 30},
		"zero duration":    {"title": "x", "kind": "work", "duration_minutes": 0},
		"bad status value": {"title": "x", "kind": "work", "duration_minutes": 30, "status": "later"},
	} {
		if w := do(t, r, "POST", "/tasks", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", name, w.Code)
		}
	}
}

// Editing duration or deadline of an unschedulable task resets it to pending
// so the next planner pass retries it.
func TestTasksUpdateResetsUnschedulable(t *testing.T) {
	h := taskHandlers(t)
	r := newTaskRouter(h)

	task := models.Task{Title: "big thing", Kind: models.SlotPersonal, DurationMinutes: 240, Status: models.TaskUnschedulable}
	if err := h.DB.Create(&task).Error; err != nil {
		t.Fatal(err)
	}

	w := do(t, r, "PATCH", "/tasks/"+task.ID, map[string]any{"duration_minutes": 60})
	if w.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}
	var got models.Task
	if err := h.DB.First(&got, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.Status != models.TaskPending {
		t.Fatalf("status after duration edit: %q, want pending", got.Status)
	}
}

func TestTasksQuickAdd(t *testing.T) {
	h := taskHandlers(t)
	r := newTaskRouter(h)

	w := do(t, r, "POST", "/tasks/quickadd", map[string]any{"input": "pack office 2h by friday"})
	if w.Code != http.StatusCreated {
		t.Fatalf("quickadd: %d %s", w.Code, w.Body)
	}
	var created models.Task
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.Title != "pack office" || created.DurationMinutes != 120 || created.Deadline == nil {
		t.Fatalf("quickadd parse: %+v", created)
	}
	if created.Kind != models.SlotPersonal || created.Status != models.TaskPending {
		t.Fatalf("quickadd defaults: %+v", created)
	}

	w = do(t, r, "POST", "/tasks/quickadd", map[string]any{"input": "2h"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("quickadd no title: %d %s", w.Code, w.Body)
	}
}
