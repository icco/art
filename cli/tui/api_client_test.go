package tui

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// encodeJSON writes a fake server response, failing the test on error.
func encodeJSON(t *testing.T, w io.Writer, v any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("encode fake response: %v", err)
	}
}

// decodeRequest parses a fake server request body, failing the test on error.
func decodeRequest(t *testing.T, r io.Reader, v any) {
	t.Helper()
	if err := json.NewDecoder(r).Decode(v); err != nil {
		t.Errorf("decode request body: %v", err)
	}
}

// stubClient builds a Client pointed at server with a pre-cached fake token so
// idToken() doesn't try to shell out to gcloud.
func stubClient(server *httptest.Server) *Client {
	c := NewClient(Config{APIURL: server.URL})
	c.token = "test-token"
	c.tokenExp = time.Now().Add(time.Hour)
	return c
}

func TestClientSetEmailArchived(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		decodeRequest(t, r.Body, &gotBody)
		encodeJSON(t, w, Email{ID: "e1", Archived: true})
	}))
	defer server.Close()

	got, err := stubClient(server).SetEmailArchived(context.Background(), "e1", true)
	if err != nil {
		t.Fatal(err)
	}
	if gotMethod != "POST" || gotPath != "/emails/e1/archive" {
		t.Fatalf("request = %s %s, want POST /emails/e1/archive", gotMethod, gotPath)
	}
	if gotBody["archived"] != true {
		t.Errorf("body archived = %v, want true", gotBody["archived"])
	}
	if !got.Archived {
		t.Errorf("returned email not archived: %+v", got)
	}
}

func TestClientErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusBadRequest)
	}))
	defer server.Close()
	_, err := stubClient(server).ListEmails(context.Background())
	if err == nil {
		t.Fatal("expected error on 400 response")
	}
}

func TestClientTriageJobAndGetJob(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && r.URL.Path == "/triage/run":
			encodeJSON(t, w, map[string]any{
				"status": "queued",
				"job":    Job{ID: "j-123", Kind: "triage", Status: "pending"},
			})
		case r.Method == "GET" && r.URL.Path == "/jobs/j-123":
			encodeJSON(t, w, Job{ID: "j-123", Kind: "triage", Status: "succeeded"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := stubClient(server)
	status, job, err := client.TriageJob(context.Background())
	if err != nil {
		t.Fatalf("TriageJob failed: %v", err)
	}
	if status != "queued" || job.ID != "j-123" {
		t.Fatalf("unexpected TriageJob result: status=%s, job=%+v", status, job)
	}

	gotJob, err := client.GetJob(context.Background(), "j-123")
	if err != nil {
		t.Fatalf("GetJob failed: %v", err)
	}
	if gotJob.ID != "j-123" || gotJob.Status != "succeeded" {
		t.Fatalf("unexpected GetJob result: %+v", gotJob)
	}
}
