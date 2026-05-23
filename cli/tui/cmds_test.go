package tui

import (
	"testing"
	"time"
)

func TestParseProjectForm(t *testing.T) {
	p, err := parseProjectForm([]formField{
		{label: "name", value: "Foo"},
		{label: "kind", value: "work"},
		{label: "hours", value: "4.5"},
		{label: "deadline", value: ""},
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p.Name != "Foo" || p.Kind != "work" || p.TargetHours != 4.5 {
		t.Fatalf("parsed wrong: %+v", p)
	}
	if p.Deadline != nil {
		t.Fatal("deadline should be nil when empty")
	}
}

func TestParseProjectFormBadHours(t *testing.T) {
	_, err := parseProjectForm([]formField{
		{value: "x"}, {value: "work"}, {value: "abc"}, {value: ""},
	})
	if err == nil {
		t.Fatal("expected error on non-numeric hours")
	}
}

func TestParseProjectFormBadDeadline(t *testing.T) {
	_, err := parseProjectForm([]formField{
		{value: "x"}, {value: "work"}, {value: "1"}, {value: "tomorrow"},
	})
	if err == nil {
		t.Fatal("expected error on non-date deadline")
	}
}

func TestParseHabitForm(t *testing.T) {
	h, err := parseHabitForm([]formField{
		{value: "Walk"},
		{value: "personal"},
		{value: "30"},
		{value: "3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if h.Name != "Walk" || h.BlockDurationMinutes != 30 || h.Cadence.Count != 3 {
		t.Fatalf("parsed wrong: %+v", h)
	}
}

func TestParseHabitFormBadInts(t *testing.T) {
	_, err := parseHabitForm([]formField{{value: "x"}, {value: "p"}, {value: "x"}, {value: "1"}})
	if err == nil {
		t.Fatal("expected error on non-int minutes")
	}
	_, err = parseHabitForm([]formField{{value: "x"}, {value: "p"}, {value: "1"}, {value: "x"}})
	if err == nil {
		t.Fatal("expected error on non-int count")
	}
}

func TestStartOfWeekLocal(t *testing.T) {
	got := startOfWeekLocal(time.Date(2026, 5, 27, 14, 0, 0, 0, time.UTC))
	if got.Weekday() != time.Monday {
		t.Fatalf("not monday: %v", got)
	}
}
