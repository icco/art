package calendar

import (
	"testing"

	"google.golang.org/api/calendar/v3"
)

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "", "x", "y"); got != "x" {
		t.Fatalf("got %q", got)
	}
	if got := firstNonEmpty(); got != "" {
		t.Fatalf("empty input should return empty, got %q", got)
	}
}

func TestEventTimesRFC3339(t *testing.T) {
	ev := &calendar.Event{
		Start: &calendar.EventDateTime{DateTime: "2026-05-25T10:00:00-07:00"},
		End:   &calendar.EventDateTime{DateTime: "2026-05-25T11:00:00-07:00"},
	}
	s, e, allDay := eventTimes(ev)
	if s.IsZero() || e.IsZero() || allDay {
		t.Fatalf("got %v %v allDay=%v", s, e, allDay)
	}
}

func TestEventTimesAllDay(t *testing.T) {
	ev := &calendar.Event{
		Start: &calendar.EventDateTime{Date: "2026-05-25"},
		End:   &calendar.EventDateTime{Date: "2026-05-26"},
	}
	s, e, allDay := eventTimes(ev)
	if s.IsZero() || e.IsZero() || !allDay {
		t.Fatalf("expected all-day, got %v %v %v", s, e, allDay)
	}
}

func TestEventTimesEmpty(t *testing.T) {
	ev := &calendar.Event{}
	s, e, _ := eventTimes(ev)
	if !s.IsZero() || !e.IsZero() {
		t.Fatal("expected zero times for empty event")
	}
}
