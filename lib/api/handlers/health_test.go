package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.icco.me/art/lib/api/handlers"
)

func TestHealth(t *testing.T) {
	w := httptest.NewRecorder()
	handlers.Health(w, httptest.NewRequestWithContext(t.Context(), "GET", "/healthz", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("code: %d", w.Code)
	}
	if w.Body.String() == "" {
		t.Fatal("empty body")
	}
}
