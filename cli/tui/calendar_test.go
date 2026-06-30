package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestCalendarAllDayPlacement(t *testing.T) {
	// Force a western timezone: all-day events are stored at UTC midnight, so a
	// naive .Local() conversion would shift them onto the previous day. The page
	// must still file them under their real calendar date and label them.
	origLoc := time.Local
	time.Local = time.FixedZone("MST", -7*3600)
	defer func() { time.Local = origLoc }()

	now := time.Date(2026, 6, 30, 9, 0, 0, 0, time.Local) // Tuesday
	orig := timeNow
	timeNow = func() time.Time { return now }
	defer func() { timeNow = orig }()

	var page Page = newCalendarPage(nil)
	page, _ = page.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	page, _ = page.Update(eventsMsg{[]Event{
		{Summary: "Canada Day", AccountKind: "personal", AllDay: true,
			StartTime: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
			EndTime:   time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC)},
		{Summary: "Standup", AccountKind: "work",
			StartTime: time.Date(2026, 7, 1, 15, 0, 0, 0, time.UTC),
			EndTime:   time.Date(2026, 7, 1, 15, 30, 0, 0, time.UTC)},
	}})

	view := page.View()
	if !strings.Contains(view, "all day") {
		t.Errorf("expected all-day label in view:\n%s", view)
	}
	if !strings.Contains(view, "08:00") {
		t.Errorf("expected timed event in local time (08:00 MST) in view:\n%s", view)
	}

	// Canada Day must sit under Wed Jul 1, not the previous day (Tue Jun 30).
	wed := strings.Index(view, "Wed Jul 1")
	thu := strings.Index(view, "Thu Jul 2")
	cd := strings.Index(view, "Canada Day")
	if wed < 0 || thu < 0 || cd < 0 {
		t.Fatalf("missing day headings or event (wed=%d thu=%d cd=%d):\n%s", wed, thu, cd, view)
	}
	if cd < wed || cd > thu {
		t.Errorf("Canada Day filed under the wrong day (wed=%d cd=%d thu=%d):\n%s", wed, cd, thu, view)
	}
}

func TestRenderEventLineAllDay(t *testing.T) {
	allDay := renderEventLine(Event{Summary: "Holiday", AccountKind: "personal", AllDay: true,
		StartTime: time.Date(2026, 7, 4, 0, 0, 0, 0, time.UTC)})
	if !strings.Contains(allDay, "all day") || strings.Contains(allDay, "00:00") {
		t.Errorf("all-day event should read 'all day', got %q", allDay)
	}

	timed := renderEventLine(Event{Summary: "Standup", AccountKind: "work",
		StartTime: time.Date(2026, 7, 4, 16, 0, 0, 0, time.UTC),
		EndTime:   time.Date(2026, 7, 4, 16, 30, 0, 0, time.UTC)})
	if !strings.Contains(timed, "–") {
		t.Errorf("timed event should show a time span, got %q", timed)
	}
}
