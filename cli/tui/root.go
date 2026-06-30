package tui

import (
	"strings"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// chromeHeight is the vertical space the root reserves for the header and
// footer, subtracted from the window height before pages are sized.
const chromeHeight = 4

// rootModel is the top of the model tree: shared deps, global chrome, and the
// message router.
type rootModel struct {
	cfg    Config
	client *Client
	isDark bool

	width, height int

	pages map[pageID]Page
	stack []pageID

	keys     keyMap
	help     help.Model
	showHelp bool

	status string
	err    error
	busy   bool
}

func newRoot(cfg Config) rootModel {
	return newRootWithClient(cfg, NewClient(cfg), detectDark())
}

// newRootWithClient builds the root with an injected client, used by tests.
func newRootWithClient(cfg Config, c *Client, isDark bool) rootModel {
	h := help.New()
	h.Styles = help.DefaultStyles(isDark)

	pages := map[pageID]Page{
		pageDashboard: newDashboardPage(c),
		pageCalendar:  newCalendarPage(c),
		pageProjects:  newProjectsPage(c, isDark),
		pageHabits:    newHabitsPage(c, isDark),
		pageDigest:    newDigestPage(c, isDark),
	}
	return rootModel{
		cfg:    cfg,
		client: c,
		isDark: isDark,
		pages:  pages,
		stack:  []pageID{pageDashboard},
		keys:   defaultKeyMap(),
		help:   h,
		status: "press ? for help",
	}
}

func (m rootModel) current() pageID { return m.stack[len(m.stack)-1] }

// Init kicks off the initial page's data load.
func (m rootModel) Init() tea.Cmd {
	return m.pages[m.current()].Init()
}

func (m rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m.broadcast(m.pageSize())

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case statusMsg:
		m.status, m.err, m.busy = string(msg), nil, false
		return m.broadcast(msg)

	case errMsg:
		m.err, m.busy = msg.err, false
		return m.broadcast(msg)

	case navigateMsg:
		return m.navigate(msg.to)

	case backMsg:
		return m.pop()

	default:
		// Data-loaded and other messages broadcast to every page so any view
		// showing that resource stays fresh.
		return m.broadcast(msg)
	}
}

func (m rootModel) handleKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// ctrl+c always quits, even inside a form.
	if k.String() == "ctrl+c" {
		return m, tea.Quit
	}
	// When the current page is capturing raw input, only it sees keys.
	if m.pages[m.current()].FullInput() {
		return m.routeToCurrent(k)
	}

	switch {
	case key.Matches(k, m.keys.Quit):
		return m, tea.Quit
	case key.Matches(k, m.keys.Help):
		m.showHelp = !m.showHelp
		m.help.ShowAll = m.showHelp
		return m, nil
	case key.Matches(k, m.keys.Dashboard):
		return m.navigate(pageDashboard)
	case key.Matches(k, m.keys.Calendar):
		return m.navigate(pageCalendar)
	case key.Matches(k, m.keys.Projects):
		return m.navigate(pageProjects)
	case key.Matches(k, m.keys.Habits):
		return m.navigate(pageHabits)
	case key.Matches(k, m.keys.Digest):
		return m.navigate(pageDigest)
	case key.Matches(k, m.keys.Back):
		if len(m.stack) > 1 {
			return m.pop()
		}
		if m.current() != pageDashboard {
			return m.navigate(pageDashboard)
		}
		return m, nil
	case key.Matches(k, m.keys.Replan):
		m.busy, m.status = true, "replanning…"
		return m, tea.Sequence(replan(m.client), m.refreshCmd())
	case key.Matches(k, m.keys.Sync):
		m.busy, m.status = true, "syncing…"
		return m, tea.Sequence(syncCalendars(m.client), m.refreshCmd())
	case key.Matches(k, m.keys.Triage):
		m.busy, m.status = true, "triaging…"
		return m, tea.Sequence(triage(m.client), tea.Batch(loadEmails(m.client), loadRuns(m.client)))
	default:
		return m.routeToCurrent(k)
	}
}

// refreshCmd reloads the data that a plan/sync run may have changed.
func (m rootModel) refreshCmd() tea.Cmd {
	from, to := startOfWeek(timeNow()), startOfWeek(timeNow()).AddDate(0, 0, 7)
	return tea.Batch(loadEvents(m.client, from, to), loadSessions(m.client, from, to), loadProjects(m.client), loadRuns(m.client))
}

func (m rootModel) navigate(to pageID) (tea.Model, tea.Cmd) {
	if m.current() == to {
		return m, nil
	}
	m.stack = append(m.stack, to)
	return m, tea.Batch(m.pages[to].Init(), sizeCmd(m.pageSize()))
}

func (m rootModel) pop() (tea.Model, tea.Cmd) {
	if len(m.stack) <= 1 {
		return m, nil
	}
	m.stack = m.stack[:len(m.stack)-1]
	return m, sizeCmd(m.pageSize())
}

func (m rootModel) routeToCurrent(msg tea.Msg) (tea.Model, tea.Cmd) {
	id := m.current()
	updated, cmd := m.pages[id].Update(msg)
	m.pages[id] = updated
	return m, cmd
}

// broadcast routes a message to every page, collecting their commands.
func (m rootModel) broadcast(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	for id, p := range m.pages {
		updated, cmd := p.Update(msg)
		m.pages[id] = updated
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return m, tea.Batch(cmds...)
}

// pageSize returns a WindowSizeMsg with the height available to pages.
func (m rootModel) pageSize() tea.WindowSizeMsg {
	h := max(m.height-chromeHeight, 1)
	return tea.WindowSizeMsg{Width: m.width, Height: h}
}

func sizeCmd(sz tea.WindowSizeMsg) tea.Cmd {
	return func() tea.Msg { return sz }
}

func (m rootModel) View() tea.View {
	width := m.width
	if width == 0 {
		width = 80
	}
	header := m.renderHeader(width)
	body := m.pages[m.current()].View()
	footer := m.renderFooter(width)
	content := lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
	return tea.View{Content: content, AltScreen: true}
}

func (m rootModel) renderHeader(width int) string {
	tabs := make([]string, 0, len(pageOrder))
	cur := m.current()
	for _, id := range pageOrder {
		label := id.String()
		if id == cur {
			tabs = append(tabs, activeTabStyle.Render(label))
		} else {
			tabs = append(tabs, tabStyle.Render(label))
		}
	}
	left := titleStyle.Render("art") + " " + strings.Join(tabs, " ")
	return left + "\n" + subtleStyle.Render(strings.Repeat("─", width))
}

func (m rootModel) renderFooter(width int) string {
	status := m.status
	switch {
	case m.err != nil:
		status = errorStyle.Render("error: " + m.err.Error())
	case m.busy:
		status = subtleStyle.Render(m.status + " (working…)")
	default:
		status = subtleStyle.Render(status)
	}
	m.help.SetWidth(width)
	h := pageHelp{keys: m.keys, page: m.pages[m.current()].bindings()}
	return status + "\n" + m.help.View(h)
}
