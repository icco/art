package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2/compat"
)

// timeNow is the clock used by the TUI; overridable in tests.
var timeNow = time.Now

// timeSleep is the sleep used by polling loops; overridable in tests.
var timeSleep = time.Sleep

// detectDark reports whether the terminal has a dark background, so bubbles
// components can be styled appropriately.
func detectDark() bool {
	return compat.HasDarkBackground
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
		return errNotWholeNumber
	}
	return nil
}

func validateFloat(s string) error {
	if _, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err != nil {
		return errNotNumber
	}
	return nil
}
