package models

import (
	"slices"
	"strings"
)

// SoftTitles is the set of human-event summaries Art may schedule on top of.
// They are placeholder blocks (e.g. "Morning Catchup", "Dinner Decompress")
// that reserve intent rather than a hard commitment, so the planner treats a
// slot they cover as available-but-second-choice instead of busy.
type SoftTitles []string

// NewSoftTitles normalizes raw titles for matching: trimmed, lowercased,
// empties dropped.
func NewSoftTitles(raw ...string) SoftTitles {
	out := make(SoftTitles, 0, len(raw))
	for _, s := range raw {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// Match reports whether summary names a soft event.
func (s SoftTitles) Match(summary string) bool {
	if len(s) == 0 {
		return false
	}
	return slices.Contains(s, strings.ToLower(strings.TrimSpace(summary)))
}
