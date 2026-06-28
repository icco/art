package handlers_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/icco/art/lib/api/handlers"
	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/testdb"
)

func newRouter(h *handlers.Handlers) http.Handler {
	r := chi.NewRouter()
	r.Get("/projects", h.ProjectsList)
	r.Post("/projects", h.ProjectsCreate)
	r.Patch("/projects/{id}", h.ProjectsUpdate)
	r.Delete("/projects/{id}", h.ProjectsDelete)
	r.Get("/habits", h.HabitsList)
	r.Post("/habits", h.HabitsCreate)
	r.Patch("/habits/{id}", h.HabitsUpdate)
	r.Delete("/habits/{id}", h.HabitsDelete)
	r.Get("/working-hours", h.WorkingHoursList)
	r.Put("/working-hours", h.WorkingHoursReplace)
	r.Get("/events", h.EventsList)
	r.Get("/sessions", h.SessionsList)
	r.Get("/emails", h.EmailsList)
	r.Post("/triage/run", h.TriageRun)
	return r
}

func do(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequestWithContext(t.Context(), method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestProjectsCRUD(t *testing.T) {
	db := testdb.Open(t)
	h := &handlers.Handlers{DB: db}
	r := newRouter(h)

	w := do(t, r, "POST", "/projects", map[string]any{
		"name": "Design X", "kind": "work", "target_hours": 4.0,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var created models.Project
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.ID == "" {
		t.Fatal("missing id")
	}

	w = do(t, r, "GET", "/projects", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d", w.Code)
	}
	var list []models.Project
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list) != 1 {
		t.Fatalf("expected 1 project, got %d", len(list))
	}

	w = do(t, r, "PATCH", "/projects/"+created.ID, map[string]any{"status": "paused"})
	if w.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}

	w = do(t, r, "DELETE", "/projects/"+created.ID, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", w.Code)
	}

	w = do(t, r, "DELETE", "/projects/00000000-0000-0000-0000-000000000000", nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("delete-missing: %d", w.Code)
	}
}

func TestProjectsValidation(t *testing.T) {
	db := testdb.Open(t)
	h := &handlers.Handlers{DB: db}
	r := newRouter(h)

	w := do(t, r, "POST", "/projects", map[string]any{"name": "", "target_hours": 1.0})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing name should 400: %d", w.Code)
	}
	w = do(t, r, "POST", "/projects", map[string]any{"name": "x", "kind": "moon", "target_hours": 1.0})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad kind should 400: %d", w.Code)
	}
	w = do(t, r, "POST", "/projects", map[string]any{"name": "x", "target_hours": 0})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("zero hours should 400: %d", w.Code)
	}
}

func TestHabitsCRUD(t *testing.T) {
	db := testdb.Open(t)
	h := &handlers.Handlers{DB: db}
	r := newRouter(h)

	w := do(t, r, "POST", "/habits", map[string]any{
		"name":                   "Walk",
		"kind":                   "personal",
		"block_duration_minutes": 30,
		"cadence":                map[string]any{"type": "per_week", "count": 3},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var hb models.Habit
	_ = json.Unmarshal(w.Body.Bytes(), &hb)

	w = do(t, r, "PATCH", "/habits/"+hb.ID, map[string]any{"block_duration_minutes": 45})
	if w.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", w.Code, w.Body)
	}

	w = do(t, r, "GET", "/habits", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d", w.Code)
	}

	w = do(t, r, "DELETE", "/habits/"+hb.ID, nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", w.Code)
	}
}

func TestHabitsValidation(t *testing.T) {
	db := testdb.Open(t)
	h := &handlers.Handlers{DB: db}
	r := newRouter(h)
	w := do(t, r, "POST", "/habits", map[string]any{
		"name": "x", "block_duration_minutes": 30,
		// missing cadence
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing cadence: %d", w.Code)
	}
}

func TestWorkingHoursReplaceList(t *testing.T) {
	db := testdb.Open(t)
	h := &handlers.Handlers{DB: db}
	r := newRouter(h)

	w := do(t, r, "PUT", "/working-hours", []map[string]any{
		{"slot_kind": "work", "day_of_week": 1, "start_minute": 540, "end_minute": 1080},
		{"slot_kind": "personal", "day_of_week": 6, "start_minute": 480, "end_minute": 1380},
	})
	if w.Code != http.StatusOK {
		t.Fatalf("replace: %d %s", w.Code, w.Body)
	}
	var got []models.WorkingHour
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || len(got) != 2 {
		t.Fatalf("list after replace: err=%v len=%d", err, len(got))
	}

	w = do(t, r, "PUT", "/working-hours", []map[string]any{
		{"slot_kind": "work", "day_of_week": 9, "start_minute": 0, "end_minute": 60},
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad day_of_week should 400: %d", w.Code)
	}
}

func TestEventsAndSessionsList(t *testing.T) {
	db := testdb.Open(t)
	h := &handlers.Handlers{DB: db}
	r := newRouter(h)
	w := do(t, r, "GET", "/events", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("events list empty: %d", w.Code)
	}
	w = do(t, r, "GET", "/events?kind=invalid", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad kind: %d", w.Code)
	}
	w = do(t, r, "GET", "/events?from=not-a-time", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad from: %d", w.Code)
	}
	w = do(t, r, "GET", "/sessions", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("sessions list empty: %d", w.Code)
	}
}
