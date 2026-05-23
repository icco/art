package tui

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type screen int

const (
	screenWeek screen = iota
	screenProjects
	screenHabits
	screenAddProject
	screenAddHabit
)

const (
	formKindProject = "project"
	formKindHabit   = "habit"
)

type App struct {
	cfg    Config
	client *Client

	screen screen
	status string

	weekAnchor time.Time
	events     []Event

	projects   []Project
	projCursor int

	habits      []Habit
	habitCursor int

	form formState

	width, height int
}

type formState struct {
	fields []formField
	active int
	kind   string // formKindProject or formKindHabit
}

type formField struct {
	label string
	value string
}

func NewApp(cfg Config) *App {
	return &App{
		cfg:        cfg,
		client:     NewClient(cfg),
		screen:     screenWeek,
		weekAnchor: startOfWeekLocal(time.Now()),
		status:     "press ? for help",
	}
}

func (a *App) Init() tea.Cmd {
	return tea.Batch(a.loadWeek(), a.loadProjects(), a.loadHabits())
}

func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = m.Width, m.Height
		return a, nil
	case tea.KeyMsg:
		return a.handleKey(m)
	case eventsLoadedMsg:
		a.events = []Event(m)
		return a, nil
	case projectsLoadedMsg:
		a.projects = []Project(m)
		return a, nil
	case habitsLoadedMsg:
		a.habits = []Habit(m)
		return a, nil
	case statusMsg:
		a.status = string(m)
		return a, nil
	case errMsg:
		a.status = "error: " + m.Error()
		return a, nil
	}
	return a, nil
}

func (a *App) handleKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if a.screen == screenAddProject || a.screen == screenAddHabit {
		return a.handleFormKey(k)
	}
	switch k.String() {
	case "ctrl+c", "q":
		return a, tea.Quit
	case "1":
		a.screen = screenWeek
	case "2":
		a.screen = screenProjects
	case "3":
		a.screen = screenHabits
	case "left":
		if a.screen == screenWeek {
			a.weekAnchor = a.weekAnchor.AddDate(0, 0, -7)
			return a, a.loadWeek()
		}
	case "right":
		if a.screen == screenWeek {
			a.weekAnchor = a.weekAnchor.AddDate(0, 0, 7)
			return a, a.loadWeek()
		}
	case "down", "j":
		if a.screen == screenProjects && a.projCursor < len(a.projects)-1 {
			a.projCursor++
		}
		if a.screen == screenHabits && a.habitCursor < len(a.habits)-1 {
			a.habitCursor++
		}
	case "up", "k":
		if a.screen == screenProjects && a.projCursor > 0 {
			a.projCursor--
		}
		if a.screen == screenHabits && a.habitCursor > 0 {
			a.habitCursor--
		}
	case "a":
		switch a.screen {
		case screenProjects:
			a.screen = screenAddProject
			a.form = formState{
				kind: formKindProject,
				fields: []formField{
					{label: "name"},
					{label: "kind (work|personal)", value: "work"},
					{label: "target hours"},
					{label: "deadline (YYYY-MM-DD, optional)"},
				},
			}
		case screenHabits:
			a.screen = screenAddHabit
			a.form = formState{
				kind: formKindHabit,
				fields: []formField{
					{label: "name"},
					{label: "kind (work|personal)", value: "personal"},
					{label: "block minutes", value: "30"},
					{label: "per_week count", value: "3"},
				},
			}
		}
	case "d":
		switch a.screen {
		case screenProjects:
			if a.projCursor < len(a.projects) {
				return a, a.deleteProject(a.projects[a.projCursor].ID)
			}
		case screenHabits:
			if a.habitCursor < len(a.habits) {
				return a, a.deleteHabit(a.habits[a.habitCursor].ID)
			}
		}
	case "r":
		return a, a.replan()
	case "s":
		return a, a.sync()
	case "?":
		a.status = "1=week 2=projects 3=habits  a=add  d=delete  r=replan  s=sync  ←→=week nav  q=quit"
	}
	return a, nil
}

func (a *App) handleFormKey(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch k.Type {
	case tea.KeyEsc:
		if a.form.kind == formKindProject {
			a.screen = screenProjects
		} else {
			a.screen = screenHabits
		}
		return a, nil
	case tea.KeyTab, tea.KeyDown:
		a.form.active = (a.form.active + 1) % len(a.form.fields)
		return a, nil
	case tea.KeyShiftTab, tea.KeyUp:
		a.form.active = (a.form.active - 1 + len(a.form.fields)) % len(a.form.fields)
		return a, nil
	case tea.KeyBackspace:
		f := &a.form.fields[a.form.active]
		if len(f.value) > 0 {
			f.value = f.value[:len(f.value)-1]
		}
		return a, nil
	case tea.KeyEnter:
		return a, a.submitForm()
	case tea.KeyRunes, tea.KeySpace:
		f := &a.form.fields[a.form.active]
		if k.Type == tea.KeySpace {
			f.value += " "
		} else {
			f.value += k.String()
		}
		return a, nil
	}
	return a, nil
}

func (a *App) View() string {
	header := a.renderHeader()
	body := ""
	switch a.screen {
	case screenWeek:
		body = a.renderWeek()
	case screenProjects:
		body = a.renderProjects()
	case screenHabits:
		body = a.renderHabits()
	case screenAddProject, screenAddHabit:
		body = a.renderForm()
	}
	return header + "\n" + body + "\n" + a.renderStatus()
}

func (a *App) renderHeader() string {
	tabs := []string{
		tabLabel("week", a.screen == screenWeek),
		tabLabel("projects", a.screen == screenProjects),
		tabLabel("habits", a.screen == screenHabits),
	}
	return lipgloss.NewStyle().Bold(true).Render("art ") + strings.Join(tabs, " ")
}

func tabLabel(name string, active bool) string {
	s := lipgloss.NewStyle().Padding(0, 1)
	if active {
		return s.Reverse(true).Render(name)
	}
	return s.Render(name)
}

func (a *App) renderStatus() string {
	return lipgloss.NewStyle().Faint(true).Render(a.status)
}
