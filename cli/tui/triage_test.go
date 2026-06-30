package tui

import "testing"

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
