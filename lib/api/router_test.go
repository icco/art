package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/icco/art/lib/api/handlers"
	"github.com/icco/art/lib/config"
	"go.uber.org/zap"
)

func newTestRouter(rpm int) http.Handler {
	return NewRouter(Deps{
		Cfg: &config.Config{OIDCAudience: "x", OwnerEmails: []string{"a@b.com"}, RateLimitRPM: rpm},
		H:   &handlers.Handlers{},
		Log: zap.NewNop().Sugar(),
	})
}

func get(t *testing.T, h http.Handler, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestRouter_HealthzPublic(t *testing.T) {
	if got := get(t, newTestRouter(0), "/healthz", nil).Code; got != http.StatusOK {
		t.Fatalf("/healthz: got %d want 200", got)
	}
}

func TestRouter_MetricsPublic(t *testing.T) {
	if got := get(t, newTestRouter(0), "/metrics", nil).Code; got != http.StatusOK {
		t.Fatalf("/metrics: got %d want 200", got)
	}
}

func TestRouter_ProtectedRequiresAuth(t *testing.T) {
	if got := get(t, newTestRouter(0), "/events", nil).Code; got != http.StatusUnauthorized {
		t.Fatalf("/events: got %d want 401", got)
	}
}

func TestRouter_SecurityHeaders(t *testing.T) {
	h := get(t, newTestRouter(0), "/healthz", nil).Header()
	for k, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := h.Get(k); got != want {
			t.Errorf("%s: got %q want %q", k, got, want)
		}
	}
	if h.Get("Content-Security-Policy") == "" {
		t.Error("missing Content-Security-Policy")
	}
}

func TestRouter_HSTSOnlyOverHTTPS(t *testing.T) {
	plain := get(t, newTestRouter(0), "/healthz", nil).Header().Get("Strict-Transport-Security")
	if plain != "" {
		t.Errorf("HSTS should be absent over plaintext, got %q", plain)
	}
	secure := get(t, newTestRouter(0), "/healthz",
		map[string]string{"X-Forwarded-Proto": "https"}).Header().Get("Strict-Transport-Security")
	if secure == "" {
		t.Error("HSTS should be set when X-Forwarded-Proto=https")
	}
}

// A rotating spoofed leftmost X-Forwarded-For must not reset the counter.
func TestRouter_RateLimitNotBypassable(t *testing.T) {
	h := newTestRouter(2)
	var got429 bool
	for i := range 5 {
		xff := fmt.Sprintf("10.0.0.%d, 9.9.9.9", i)
		if get(t, h, "/healthz", map[string]string{"X-Forwarded-For": xff}).Code == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Fatal("rate limit never tripped despite rotating spoofed X-Forwarded-For")
	}
}

// A direct (non-proxied) client controls the whole XFF header, including the
// rightmost hop; its bucket must key on RemoteAddr instead.
func TestRouter_RateLimitIgnoresXFFFromUntrustedRemote(t *testing.T) {
	h := newTestRouter(2)
	var got429 bool
	for i := range 5 {
		req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", nil)
		req.RemoteAddr = "203.0.113.7:4444" // public address: not the proxy
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("10.0.0.%d", i))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code == http.StatusTooManyRequests {
			got429 = true
			break
		}
	}
	if !got429 {
		t.Fatal("rotating rightmost XFF from an untrusted remote bypassed the rate limit")
	}
}
