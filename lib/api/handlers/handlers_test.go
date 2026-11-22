package handlers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"go.icco.me/art/lib/api/handlers"
	"go.icco.me/art/lib/models"
	"go.icco.me/art/lib/testdb"
	"gorm.io/gorm"
)

func newRouter(h *handlers.Handlers) http.Handler {
	r := chi.NewRouter()
	r.Get("/emails", h.EmailsList)
	r.Get("/agent-runs", h.AgentRunsList)
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

// PATCH must distinguish "field absent" from "clear this field".
func mustDecode(t *testing.T, w *httptest.ResponseRecorder, v any) {
	t.Helper()
	if err := json.NewDecoder(w.Body).Decode(v); err != nil {
		t.Fatal(err)
	}
}

func TestAgentRunsList(t *testing.T) {
	db := testdb.Open(t)
	h := &handlers.Handlers{DB: db}
	r := newRouter(h)

	// Seed a historical planner run and two triage runs (the newer triage run is latest).
	runs := []models.AgentRun{
		{Kind: models.AgentRunPlanner, Status: models.AgentRunSucceeded, StartedAt: time.Unix(1000, 0)},
		{Kind: models.AgentRunTriage, Status: models.AgentRunSucceeded, StartedAt: time.Unix(2000, 0)},
		{Kind: models.AgentRunTriage, Status: models.AgentRunFailed, StartedAt: time.Unix(3000, 0)},
	}
	if err := db.Create(&runs).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Default: newest first.
	w := do(t, r, "GET", "/agent-runs", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	var all []models.AgentRun
	if err := json.Unmarshal(w.Body.Bytes(), &all); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 runs, got %d", len(all))
	}
	if all[0].StartedAt.Unix() != 3000 {
		t.Fatalf("expected newest first, got %d", all[0].StartedAt.Unix())
	}

	// kind filter + limit.
	w = do(t, r, "GET", "/agent-runs?kind=triage&limit=1", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("filtered list: %d %s", w.Code, w.Body)
	}
	var triage []models.AgentRun
	mustDecode(t, w, &triage)
	if len(triage) != 1 || triage[0].Kind != models.AgentRunTriage || triage[0].Status != models.AgentRunFailed {
		t.Fatalf("expected newest triage run only, got %+v", triage)
	}

	// invalid kind -> 400.
	w = do(t, r, "GET", "/agent-runs?kind=bogus", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad kind should 400: %d", w.Code)
	}
}

type stubTriage struct {
	msg models.EmailMessage
	err error
}

func (s stubTriage) Reverse(context.Context, string) (models.EmailMessage, error) {
	return s.msg, s.err
}
func (s stubTriage) SetArchived(context.Context, string, bool) (models.EmailMessage, error) {
	return s.msg, s.err
}

func TestEmailReverse(t *testing.T) {
	h := &handlers.Handlers{Triage: stubTriage{
		msg: models.EmailMessage{Action: models.ActionArchived, Reversed: true, ReversalKind: "unarchived"},
	}}
	r := chi.NewRouter()
	r.Post("/emails/{id}/reverse", h.EmailReverse)

	const validID = "11111111-1111-1111-1111-111111111111"
	const missingID = "00000000-0000-0000-0000-000000000000"

	if w := do(t, r, "POST", "/emails/not-a-uuid/reverse", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("malformed id: got %d, want 400", w.Code)
	}

	w := do(t, r, "POST", "/emails/"+validID+"/reverse", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("reverse: %d %s", w.Code, w.Body)
	}
	var got models.EmailMessage
	mustDecode(t, w, &got)
	if !got.Reversed {
		t.Error("expected reversed row in response")
	}

	h2 := &handlers.Handlers{Triage: stubTriage{err: gorm.ErrRecordNotFound}}
	r2 := chi.NewRouter()
	r2.Post("/emails/{id}/reverse", h2.EmailReverse)
	if w := do(t, r2, "POST", "/emails/"+missingID+"/reverse", nil); w.Code != http.StatusNotFound {
		t.Fatalf("unknown id: got %d, want 404", w.Code)
	}
}

func TestEmailSetArchived(t *testing.T) {
	h := &handlers.Handlers{Triage: stubTriage{
		msg: models.EmailMessage{Archived: true, Action: models.ActionArchived},
	}}
	r := chi.NewRouter()
	r.Post("/emails/{id}/archive", h.EmailSetArchived)

	const validID = "11111111-1111-1111-1111-111111111111"
	const missingID = "00000000-0000-0000-0000-000000000000"

	if w := do(t, r, "POST", "/emails/not-a-uuid/archive", map[string]any{"archived": true}); w.Code != http.StatusBadRequest {
		t.Fatalf("malformed id: got %d, want 400", w.Code)
	}

	w := do(t, r, "POST", "/emails/"+validID+"/archive", map[string]any{"archived": true})
	if w.Code != http.StatusOK {
		t.Fatalf("set archived: %d %s", w.Code, w.Body)
	}
	var got models.EmailMessage
	mustDecode(t, w, &got)
	if !got.Archived {
		t.Error("expected archived row in response")
	}

	// Malformed body is a client error.
	if w := do(t, r, "POST", "/emails/"+validID+"/archive", "not-json"); w.Code != http.StatusBadRequest {
		t.Fatalf("malformed body: got %d, want 400", w.Code)
	}

	// Unknown id surfaces as 404.
	h2 := &handlers.Handlers{Triage: stubTriage{err: gorm.ErrRecordNotFound}}
	r2 := chi.NewRouter()
	r2.Post("/emails/{id}/archive", h2.EmailSetArchived)
	if w := do(t, r2, "POST", "/emails/"+missingID+"/archive", map[string]any{"archived": false}); w.Code != http.StatusNotFound {
		t.Fatalf("unknown id: got %d, want 404", w.Code)
	}
}

func TestEmailReverseCanonicalizesID(t *testing.T) {
	var got string
	h := &handlers.Handlers{Triage: recordingTriage{id: &got}}
	r := chi.NewRouter()
	r.Post("/emails/{id}/reverse", h.EmailReverse)
	w := do(t, r, "POST", "/emails/urn:uuid:11111111-1111-1111-1111-111111111111/reverse", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("urn id: %d %s", w.Code, w.Body)
	}
	if got != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("service got %q, want canonical uuid", got)
	}
}

func TestServerErrorAfterDeadlineIs504(t *testing.T) {
	h := &handlers.Handlers{Triage: stubTriage{err: context.DeadlineExceeded}}
	r := chi.NewRouter()
	r.Post("/emails/{id}/reverse", h.EmailReverse)
	w := do(t, r, "POST", "/emails/11111111-1111-1111-1111-111111111111/reverse", nil)
	if w.Code != http.StatusGatewayTimeout {
		t.Fatalf("deadline: got %d, want 504", w.Code)
	}
}

type recordingTriage struct{ id *string }

func (s recordingTriage) Reverse(_ context.Context, id string) (models.EmailMessage, error) {
	*s.id = id
	return models.EmailMessage{}, nil
}
func (s recordingTriage) SetArchived(_ context.Context, id string, _ bool) (models.EmailMessage, error) {
	*s.id = id
	return models.EmailMessage{}, nil
}
