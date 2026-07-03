package tui

import "time"

// Message types flowing through the Bubble Tea update loop. Data-loaded
// messages are broadcast to every page so any view showing that resource
// stays fresh (this is what makes mutations reflect immediately). Status and
// error messages are handled by the root chrome.

// eventsMsg carries the queried window so pages showing a different week can
// ignore loads that don't cover it.
type eventsMsg struct {
	events   []Event
	from, to time.Time
}
type projectsMsg struct{ projects []Project }
type habitsMsg struct{ habits []Habit }
type sessionsMsg struct{ sessions []Session }
type emailsMsg struct{ emails []Email }
type runsMsg struct{ runs []AgentRun }

type statusMsg string

type errMsg struct{ err error }

func (e errMsg) Error() string { return e.err.Error() }

// navigateMsg asks the root to switch to a page; backMsg pops the nav stack.
type navigateMsg struct{ to pageID }
type backMsg struct{}
