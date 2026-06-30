package tui

import (
	"testing"
	"time"
)

func TestStartOfWeek(t *testing.T) {
	// Wed 2026-07-01 14:30 -> Mon 2026-06-29 00:00.
	in := time.Date(2026, 7, 1, 14, 30, 0, 0, time.Local)
	got := startOfWeek(in)
	want := time.Date(2026, 6, 29, 0, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("startOfWeek(%v) = %v, want %v", in, got, want)
	}
	// A Monday maps to itself at midnight.
	mon := time.Date(2026, 6, 29, 9, 0, 0, 0, time.Local)
	if got := startOfWeek(mon); !got.Equal(want) {
		t.Fatalf("startOfWeek(monday) = %v, want %v", got, want)
	}
	// A Sunday maps back to the prior Monday.
	sun := time.Date(2026, 7, 5, 23, 0, 0, 0, time.Local)
	if got := startOfWeek(sun); !got.Equal(want) {
		t.Fatalf("startOfWeek(sunday) = %v, want %v", got, want)
	}
}

func TestProgressBar(t *testing.T) {
	tests := []struct {
		frac  float64
		width int
		want  string
	}{
		{0, 4, "░░░░"},
		{1, 4, "████"},
		{0.5, 4, "██░░"},
		{-1, 3, "░░░"},  // clamped low
		{2, 3, "███"},   // clamped high
		{0.5, 0, ""},    // zero width
	}
	for _, tc := range tests {
		if got := progressBar(tc.frac, tc.width); got != tc.want {
			t.Errorf("progressBar(%v, %d) = %q, want %q", tc.frac, tc.width, got, tc.want)
		}
	}
}

func TestRelTime(t *testing.T) {
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		t    time.Time
		want string
	}{
		{now.Add(-30 * time.Second), "just now"},
		{now.Add(-5 * time.Minute), "5m ago"},
		{now.Add(-2 * time.Hour), "2h ago"},
		{now.Add(-3 * 24 * time.Hour), "3d ago"},
	}
	for _, tc := range tests {
		if got := relTime(tc.t, now); got != tc.want {
			t.Errorf("relTime(%v) = %q, want %q", tc.t, got, tc.want)
		}
	}
}

func TestCadenceCounts(t *testing.T) {
	sessions := []Session{
		{Source: "habit", SourceID: "h1", Status: "happened"},
		{Source: "habit", SourceID: "h1", Status: "planned"},
		{Source: "habit", SourceID: "h1", Status: "skipped"},
		{Source: "habit", SourceID: "h2", Status: "happened"},
		{Source: "project", SourceID: "h1", Status: "happened"}, // wrong source
	}
	happened, planned := cadenceCounts(sessions, "h1")
	if happened != 1 || planned != 1 {
		t.Fatalf("cadenceCounts(h1) = (%d,%d), want (1,1)", happened, planned)
	}
	if h, p := cadenceCounts(sessions, "missing"); h != 0 || p != 0 {
		t.Fatalf("cadenceCounts(missing) = (%d,%d), want (0,0)", h, p)
	}
}
