package tui

import tea "charm.land/bubbletea/v2"

// pageID identifies a top-level screen.
type pageID int

const (
	pageDashboard pageID = iota
	pageCalendar
	pageProjects
	pageHabits
	pageDigest
)

// pageOrder is the tab order used by the header and number keys.
var pageOrder = []pageID{pageDashboard, pageCalendar, pageProjects, pageHabits, pageDigest}

func (p pageID) String() string {
	switch p {
	case pageDashboard:
		return "dashboard"
	case pageCalendar:
		return "calendar"
	case pageProjects:
		return "projects"
	case pageHabits:
		return "habits"
	case pageDigest:
		return "digest"
	default:
		return "?"
	}
}

// Page is one screen in the model tree. Pages use value receivers and return
// themselves from Update, so the root can store the updated page without a
// type assertion. View returns a plain string; only the root composes the
// final tea.View (and owns alt-screen).
type Page interface {
	Init() tea.Cmd
	Update(tea.Msg) (Page, tea.Cmd)
	View() string
	Title() string
	// FullInput reports that the page is capturing raw keystrokes (an open
	// form or an active list filter), so the root must not steal global keys.
	FullInput() bool
}
