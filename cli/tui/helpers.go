package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2/compat"
)

// timeNow is the clock used by the TUI; overridable in tests.
var timeNow = time.Now

// detectDark reports whether the terminal has a dark background, so bubbles
// components can be styled appropriately.
func detectDark() bool {
	return compat.HasDarkBackground
}

// startOfWeek returns Monday 00:00 (local) of the week containing t.
func startOfWeek(t time.Time) time.Time {
	weekday := int(t.Weekday()) // Sunday=0
	if weekday == 0 {
		weekday = 7
	}
	monday := t.AddDate(0, 0, -(weekday - 1))
	return time.Date(monday.Year(), monday.Month(), monday.Day(), 0, 0, 0, 0, monday.Location())
}

// progressBar renders a fixed-width bar; frac is clamped to [0,1].
func progressBar(frac float64, width int) string {
	if width <= 0 {
		return ""
	}
	switch {
	case frac < 0:
		frac = 0
	case frac > 1:
		frac = 1
	}
	filled := min(int(frac*float64(width)+0.5), width)
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

// relTime renders a compact relative time like "5m ago" or "3d ago".
func relTime(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours())/24)
	}
}

// sameLocalDay reports whether a and b fall on the same calendar day locally.
func sameLocalDay(a, b time.Time) bool {
	al, bl := a.Local(), b.Local()
	return al.Year() == bl.Year() && al.Month() == bl.Month() && al.Day() == bl.Day()
}

// truncate shortens s to n runes, adding an ellipsis when cut.
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n <= 1 {
		return string(r[:n])
	}
	return string(r[:n-1]) + "…"
}

// Form field validators; huh blocks submission until these pass.
func validateInt(s string) error {
	if _, err := strconv.Atoi(strings.TrimSpace(s)); err != nil {
		return errors.New("must be a whole number")
	}
	return nil
}

func validateFloat(s string) error {
	if _, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err != nil {
		return errors.New("must be a number")
	}
	return nil
}

func validateOptionalDate(s string) error {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	if _, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(s), time.Local); err != nil {
		return errors.New("must be YYYY-MM-DD")
	}
	return nil
}

// cadenceCounts tallies happened vs planned habit sessions for one habit.
func cadenceCounts(sessions []Session, sourceID string) (happened, planned int) {
	for _, s := range sessions {
		if s.Source != "habit" || s.SourceID != sourceID {
			continue
		}
		switch s.Status {
		case "happened":
			happened++
		case "planned":
			planned++
		}
	}
	return happened, planned
}
