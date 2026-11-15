package tui

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.icco.me/art/lib/api/handlers"
	"go.icco.me/art/lib/settings"
	"go.icco.me/art/lib/testdb"
)

// Drives the real client against the real handlers so client/server request
// contract drift (e.g. sending fields DisallowUnknownFields rejects) fails here.
func TestClientAgainstRealHandlers(t *testing.T) {
	db := testdb.Open(t)
	h := &handlers.Handlers{DB: db, Settings: settings.New(db, nil)}
	r := chi.NewRouter()
	r.Get("/settings", h.SettingsGet)
	r.Put("/settings", h.SettingsUpdate)
	server := httptest.NewServer(r)
	defer server.Close()

	c := stubClient(server)
	ctx := context.Background()

	s, err := c.GetSettings(ctx)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	s.TriageBackfillDays = 3
	got, err := c.UpdateSettings(ctx, s)
	if err != nil {
		t.Fatalf("update settings: %v", err)
	}
	if got.TriageBackfillDays != 3 {
		t.Fatalf("backfill = %d, want 3", got.TriageBackfillDays)
	}
}
