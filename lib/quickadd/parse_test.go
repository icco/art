package quickadd

import (
	"strings"
	"testing"
	"time"

	"github.com/icco/art/lib/models"
)

// Fixed reference time: Wednesday 2026-06-10 14:30 in LA.
var la = mustLoad("America/Los_Angeles")

func mustLoad(name string) *time.Location {
	tz, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return tz
}

func wedNow() time.Time {
	return time.Date(2026, 6, 10, 14, 30, 0, 0, la)
}

func eod(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 23, 59, 59, 0, la)
}

func TestParse(t *testing.T) {
	cases := []struct {
		name     string
		input    string
		title    string
		duration int
		deadline *time.Time
		kind     models.SlotKind
	}{
		{
			name: "headline case", input: "pack office 2h by friday",
			title: "pack office", duration: 120, deadline: new(eod(2026, 6, 12)), kind: models.SlotPersonal,
		},
		{
			name: "defaults only", input: "call dentist",
			title: "call dentist", duration: 60, deadline: nil, kind: models.SlotPersonal,
		},
		{
			name: "minutes duration", input: "write standup notes 90m #work",
			title: "write standup notes", duration: 90, deadline: nil, kind: models.SlotWork,
		},
		{
			name: "h+m duration", input: "deep clean 1h30m",
			title: "deep clean", duration: 90, deadline: nil, kind: models.SlotPersonal,
		},
		{
			name: "fractional hours", input: "review doc 1.5h",
			title: "review doc", duration: 90, deadline: nil, kind: models.SlotPersonal,
		},
		{
			name: "due keyword and iso date", input: "file taxes 2h due 2026-06-20",
			title: "file taxes", duration: 120, deadline: new(eod(2026, 6, 20)), kind: models.SlotPersonal,
		},
		{
			name: "slash date this year", input: "buy gift by 6/15",
			title: "buy gift", duration: 60, deadline: new(eod(2026, 6, 15)), kind: models.SlotPersonal,
		},
		{
			name: "slash date rolls to next year", input: "renew passport by 1/5",
			title: "renew passport", duration: 60, deadline: new(eod(2027, 1, 5)), kind: models.SlotPersonal,
		},
		{
			name: "today", input: "send invoice 30m by today #work",
			title: "send invoice", duration: 30, deadline: new(eod(2026, 6, 10)), kind: models.SlotWork,
		},
		{
			name: "tomorrow", input: "prep slides 1h by tomorrow",
			title: "prep slides", duration: 60, deadline: new(eod(2026, 6, 11)), kind: models.SlotPersonal,
		},
		{
			name: "eow is sunday", input: "clean garage by eow",
			title: "clean garage", duration: 60, deadline: new(eod(2026, 6, 14)), kind: models.SlotPersonal,
		},
		{
			name: "weekday same as today means today", input: "water plants by wed",
			title: "water plants", duration: 60, deadline: new(eod(2026, 6, 10)), kind: models.SlotPersonal,
		},
		{
			name: "by not followed by deadline stays in title", input: "stop by store 1h",
			title: "stop by store", duration: 60, deadline: nil, kind: models.SlotPersonal,
		},
		{
			// Forgiving by design: the caller echoes the parse back, so a
			// missed deadline word is visible rather than fatal.
			name: "unknown word after by stays in title", input: "pack 2h by whenever",
			title: "pack by whenever", duration: 120, deadline: nil, kind: models.SlotPersonal,
		},
		{
			name: "kind tag anywhere", input: "#work plan offsite 2h",
			title: "plan offsite", duration: 120, deadline: nil, kind: models.SlotWork,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Parse(tc.input, wedNow(), la)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tc.input, err)
			}
			if got.Title != tc.title {
				t.Errorf("title: got %q, want %q", got.Title, tc.title)
			}
			if got.DurationMinutes != tc.duration {
				t.Errorf("duration: got %d, want %d", got.DurationMinutes, tc.duration)
			}
			if got.Kind != tc.kind {
				t.Errorf("kind: got %q, want %q", got.Kind, tc.kind)
			}
			switch {
			case tc.deadline == nil && got.Deadline != nil:
				t.Errorf("deadline: got %v, want nil", got.Deadline)
			case tc.deadline != nil && got.Deadline == nil:
				t.Errorf("deadline: got nil, want %v", tc.deadline)
			case tc.deadline != nil && !got.Deadline.Equal(*tc.deadline):
				t.Errorf("deadline: got %v, want %v", got.Deadline, tc.deadline)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantSub string
	}{
		{"empty input", "", "title"},
		{"only tokens no title", "2h by friday", "title"},
		{"duplicate duration", "pack 2h 3h", "duration"},
		{"past deadline", "pack 2h by 2026-06-01", "past"},
		{"duration too long", "move house 9h", "project"},
		{"zero duration", "blink 0m", "duration"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.input, wedNow(), la)
			if err == nil {
				t.Fatalf("Parse(%q): expected error", tc.input)
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.wantSub) {
				t.Errorf("error %q should mention %q", err, tc.wantSub)
			}
		})
	}
}
