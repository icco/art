package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/icco/art/lib/api/handlers"
)

func TestHealth(t *testing.T) {
	w := httptest.NewRecorder()
	handlers.Health(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("code: %d", w.Code)
	}
	if w.Body.String() == "" {
		t.Fatal("empty body")
	}
}
