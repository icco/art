package tui

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

type capturedReq struct {
	mu     sync.Mutex
	method string
	path   string
	body   []byte
}

func captureServer(t *testing.T, c *capturedReq, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		c.mu.Lock()
		c.method, c.path, c.body = r.Method, r.URL.Path, body
		c.mu.Unlock()
		w.WriteHeader(status)
		_, _ = w.Write([]byte("{}"))
	}))
}
