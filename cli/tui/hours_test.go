package tui

import (
	"strings"
	"testing"
)

func TestParseHoursCell(t *testing.T) {
	got, err := parseHoursCell("9:00-17:00")
	if err != nil || len(got) != 1 || got[0].StartMinute != 540 || got[0].EndMinute != 1020 {
		t.Fatalf("simple window: %+v err=%v", got, err)
	}

	got, err = parseHoursCell("9:00-12:00, 13:30-17:00")
	if err != nil || len(got) != 2 || got[1].StartMinute != 810 {
		t.Fatalf("two windows: %+v err=%v", got, err)
	}

	if got, err = parseHoursCell("  "); err != nil || got != nil {
		t.Fatalf("blank cell should be empty: %+v err=%v", got, err)
	}

	for _, bad := range []string{"9-17", "9:00", "17:00-9:00", "9:00-25:00", "x"} {
		if _, err := parseHoursCell(bad); err == nil {
			t.Errorf("parseHoursCell(%q): expected error", bad)
		}
	}
}

func TestHoursFormRoundTrip(t *testing.T) {
	hours := []WorkingHour{
		{SlotKind: "work", DayOfWeek: 1, StartMinute: 540, EndMinute: 1020},
		{SlotKind: "work", DayOfWeek: 1, StartMinute: 1080, EndMinute: 1200},
		{SlotKind: "personal", DayOfWeek: 6, StartMinute: 600, EndMinute: 720},
	}
	fields := hoursFields(hours)
	if len(fields) != 14 {
		t.Fatalf("want 14 fields (7 days x 2 kinds), got %d", len(fields))
	}
	// Monday work cell holds both windows.
	var monWork formField
	for _, f := range fields {
		if strings.HasPrefix(f.label, "Mon work") {
			monWork = f
		}
	}
	if monWork.value != "9:00-17:00, 18:00-20:00" {
		t.Fatalf("Mon work cell: %q", monWork.value)
	}

	back, err := parseHoursForm(fields)
	if err != nil {
		t.Fatal(err)
	}
	// parseHoursForm orders by kind, day, start (the API list order).
	want := []WorkingHour{
		{SlotKind: "personal", DayOfWeek: 6, StartMinute: 600, EndMinute: 720},
		{SlotKind: "work", DayOfWeek: 1, StartMinute: 540, EndMinute: 1020},
		{SlotKind: "work", DayOfWeek: 1, StartMinute: 1080, EndMinute: 1200},
	}
	if len(back) != len(want) {
		t.Fatalf("round trip: got %d windows, want %d", len(back), len(want))
	}
	for i, w := range back {
		if w != want[i] {
			t.Fatalf("round trip window %d: %+v != %+v", i, w, want[i])
		}
	}
}
