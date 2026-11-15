package tui

import (
	"testing"
	"time"
)

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
