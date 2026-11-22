package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSettled(t *testing.T) {
	cases := []struct {
		name     string
		latest   *AgentRun
		baseline string
		want     bool
	}{
		{"no runs yet", nil, "", false},
		{"still running", &AgentRun{ID: "r1", Status: "running"}, "", false},
		{"fresh terminal, no baseline", &AgentRun{ID: "r1", Status: "succeeded"}, "", true},
		{"still the baseline run", &AgentRun{ID: "old", Status: "succeeded"}, "old", false},
		{"new terminal run", &AgentRun{ID: "new", Status: "failed"}, "old", true},
	}
	for _, c := range cases {
		if got := settled(c.latest, c.baseline); got != c.want {
			t.Errorf("%s: settled=%v want %v", c.name, got, c.want)
		}
	}
}

func TestStartAndAwaitConsecutiveErrors(t *testing.T) {
	origSleep := timeSleep
	timeSleep = func(time.Duration) {}
	defer func() { timeSleep = origSleep }()

	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			// First call is latestRunOf before start
			encodeJSON(t, w, []AgentRun{})
			return
		}
		// Subsequent polls fail
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer server.Close()

	client := stubClient(server)
	msg := startAndAwaitJob(client, func(context.Context) (string, string, error) {
		return "started", "", nil
	})

	em, ok := msg.(errMsg)
	if !ok {
		t.Fatalf("expected errMsg, got %T: %+v", msg, msg)
	}
	if em.err == nil {
		t.Fatal("expected non-nil error")
	}
}

func TestStartAndAwaitDisabledJob(t *testing.T) {
	origSleep := timeSleep
	timeSleep = func(time.Duration) {}
	defer func() { timeSleep = origSleep }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/agent-runs":
			encodeJSON(t, w, []AgentRun{})
		case "/jobs/j-disabled":
			encodeJSON(t, w, Job{ID: "j-disabled", Status: "succeeded"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := stubClient(server)
	msg := startAndAwaitJob(client, func(context.Context) (string, string, error) {
		return "started", "j-disabled", nil
	})

	sm, ok := msg.(statusMsg)
	if !ok {
		t.Fatalf("expected statusMsg, got %T: %+v", msg, msg)
	}
	want := "triage finished, no run recorded (disabled?)"
	if string(sm) != want {
		t.Fatalf("got status %q, want %q", string(sm), want)
	}
}

func TestStartAndAwaitFailedJobWithoutRun(t *testing.T) {
	origSleep := timeSleep
	timeSleep = func(time.Duration) {}
	defer func() { timeSleep = origSleep }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/agent-runs":
			encodeJSON(t, w, []AgentRun{})
		case "/jobs/j-failed":
			encodeJSON(t, w, Job{ID: "j-failed", Status: "failed", LastError: "rate limit exceeded"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := stubClient(server)
	msg := startAndAwaitJob(client, func(context.Context) (string, string, error) {
		return "started", "j-failed", nil
	})

	em, ok := msg.(errMsg)
	if !ok {
		t.Fatalf("expected errMsg, got %T: %+v", msg, msg)
	}
	if !strings.Contains(em.err.Error(), "rate limit exceeded") {
		t.Fatalf("expected last_error in errMsg, got: %v", em.err)
	}
}

func TestStartAndAwaitRunsFailWhileJobPolls(t *testing.T) {
	origSleep := timeSleep
	timeSleep = func(time.Duration) {}
	defer func() { timeSleep = origSleep }()

	runCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/agent-runs":
			runCalls++
			if runCalls == 1 {
				encodeJSON(t, w, []AgentRun{})
				return
			}
			http.Error(w, "runs down", http.StatusInternalServerError)
		case "/jobs/j1":
			encodeJSON(t, w, Job{ID: "j1", Status: "running"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	msg := startAndAwaitJob(stubClient(server), func(context.Context) (string, string, error) {
		return "started", "j1", nil
	})
	if _, ok := msg.(errMsg); !ok {
		t.Fatalf("expected errMsg when /agent-runs keeps failing, got %T: %+v", msg, msg)
	}
}

func TestStartAndAwaitJobFailsWhileRunsPoll(t *testing.T) {
	origSleep := timeSleep
	timeSleep = func(time.Duration) {}
	defer func() { timeSleep = origSleep }()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/agent-runs":
			encodeJSON(t, w, []AgentRun{})
		case "/jobs/j1":
			http.Error(w, "jobs down", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	msg := startAndAwaitJob(stubClient(server), func(context.Context) (string, string, error) {
		return "started", "j1", nil
	})
	if _, ok := msg.(errMsg); !ok {
		t.Fatalf("expected errMsg when /jobs keeps failing, got %T: %+v", msg, msg)
	}
}

func TestStartAndAwaitFinalRunLookupFails(t *testing.T) {
	origSleep := timeSleep
	timeSleep = func(time.Duration) {}
	defer func() { timeSleep = origSleep }()

	runCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/agent-runs":
			runCalls++
			if runCalls <= 2 {
				encodeJSON(t, w, []AgentRun{})
				return
			}
			http.Error(w, "runs down", http.StatusInternalServerError)
		case "/jobs/j1":
			encodeJSON(t, w, Job{ID: "j1", Status: "succeeded"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	msg := startAndAwaitJob(stubClient(server), func(context.Context) (string, string, error) {
		return "started", "j1", nil
	})
	if _, ok := msg.(errMsg); !ok {
		t.Fatalf("expected errMsg when the final run lookup fails, got %T: %+v", msg, msg)
	}
}
