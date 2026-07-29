package models

import (
	"slices"
	"strings"
)

// SoftTitles are human-event summaries Art may schedule on top of: placeholder
// blocks that reserve intent, not a commitment. The planner treats the time they
// cover as available-but-second-choice rather than busy.
type SoftTitles []string

// NewSoftTitles normalizes titles for matching: trimmed, lowercased, no empties.
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
