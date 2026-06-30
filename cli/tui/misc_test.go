package tui

import (
	"strings"
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	t.Setenv("ART_API_URL", "http://example/api")
	c, err := LoadConfig()
	if err != nil || c.APIURL != "http://example/api" {
		t.Fatalf("LoadConfig: %v %+v", err, c)
	}
}

func TestEnvOr(t *testing.T) {
	t.Setenv("ART_X", "")
	if got := envOr("ART_X", "fallback"); got != "fallback" {
		t.Fatalf("envOr fallback: %q", got)
	}
	t.Setenv("ART_X", "set")
	if got := envOr("ART_X", "fallback"); got != "set" {
		t.Fatalf("envOr value: %q", got)
	}
}

func TestSameDay(t *testing.T) {
	a := time.Date(2026, 5, 25, 1, 0, 0, 0, time.UTC)
	b := time.Date(2026, 5, 25, 23, 0, 0, 0, time.UTC)
	if !sameDay(a, b) {
		t.Fatal("same-day check failed")
	}
	if sameDay(a, b.Add(24*time.Hour)) {
		t.Fatal("next-day should not be same day")
	}
}

func TestRenderEventLine(t *testing.T) {
	work := Event{Summary: "1:1", AccountKind: "work", StartTime: time.Now(), EndTime: time.Now().Add(time.Hour)}
	personal := Event{Summary: "Walk", AccountKind: "personal", StartTime: time.Now(), EndTime: time.Now().Add(time.Hour)}
	focus := Event{Summary: "Focus", EventType: "focusTime", IsArtManaged: true, StartTime: time.Now(), EndTime: time.Now().Add(time.Hour)}
	for _, e := range []Event{work, personal, focus} {
		if !strings.Contains(renderEventLine(e), e.Summary) {
			t.Fatalf("rendered line missing summary for %+v", e)
		}
	}
}

func TestRenderFormEmpty(t *testing.T) {
	a := newTestApp()
	a.form = formState{kind: formKindHabit, fields: []formField{{label: "name", value: "x"}}}
	if got := a.renderForm(); !strings.Contains(got, "name") {
		t.Fatalf("renderForm missing label: %q", got)
	}
}

func TestNewAppInit(t *testing.T) {
	a := NewApp(Config{APIURL: "http://localhost"})
	if a == nil || a.client == nil || a.screen != screenWeek {
		t.Fatal("NewApp didn't initialize")
	}
	if cmd := a.Init(); cmd == nil {
		t.Fatal("Init should return a non-nil command")
	}
}
