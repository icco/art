package tui

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

type (
	eventsLoadedMsg   []Event
	projectsLoadedMsg []Project
	habitsLoadedMsg   []Habit
	tasksLoadedMsg    []Task
	hoursLoadedMsg    []WorkingHour
	statusMsg         string
	errMsg            struct{ error }
)

func (a *App) loadWeek() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		from := a.weekAnchor
		to := from.AddDate(0, 0, 7)
		evs, err := a.client.ListEvents(ctx, from, to)
		if err != nil {
			return errMsg{err}
		}
		return eventsLoadedMsg(evs)
	}
}

func (a *App) loadProjects() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		ps, err := a.client.ListProjects(ctx)
		if err != nil {
			return errMsg{err}
		}
		return projectsLoadedMsg(ps)
	}
}

func (a *App) loadHabits() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		hs, err := a.client.ListHabits(ctx)
		if err != nil {
			return errMsg{err}
		}
		return habitsLoadedMsg(hs)
	}
}

func (a *App) loadTasks() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		ts, err := a.client.ListTasks(ctx, "")
		if err != nil {
			return errMsg{err}
		}
		return tasksLoadedMsg(ts)
	}
}

func (a *App) loadHours() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		hs, err := a.client.ListWorkingHours(ctx)
		if err != nil {
			return errMsg{err}
		}
		return hoursLoadedMsg(hs)
	}
}

func (a *App) deleteTask(id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := a.client.DeleteTask(ctx, id); err != nil {
			return errMsg{err}
		}
		return a.loadTasks()()
	}
}

func (a *App) toggleTaskDone(t Task) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		next := "done"
		if t.Status == "done" {
			next = "pending"
		}
		if _, err := a.client.UpdateTask(ctx, t.ID, map[string]any{"status": next}); err != nil {
			return errMsg{err}
		}
		return a.loadTasks()()
	}
}

func (a *App) replan() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		res, err := a.client.Replan(ctx)
		if err != nil {
			return errMsg{err}
		}
		return statusMsg("replan " + res.Status + ": " + string(res.Summary))
	}
}

func (a *App) sync() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := a.client.Sync(ctx); err != nil {
			return errMsg{err}
		}
		return statusMsg("sync done")
	}
}

func (a *App) submitForm() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		switch a.form.kind {
		case formKindProject:
			p, err := parseProjectForm(a.form.fields)
			if err != nil {
				return errMsg{err}
			}
			if _, err := a.client.CreateProject(ctx, p); err != nil {
				return errMsg{err}
			}
			a.screen = screenProjects
			return statusMsg("project created")
		case formKindHabit:
			h, err := parseHabitForm(a.form.fields)
			if err != nil {
				return errMsg{err}
			}
			if _, err := a.client.CreateHabit(ctx, h); err != nil {
				return errMsg{err}
			}
			a.screen = screenHabits
			return statusMsg("habit created")
		case formKindQuickAdd:
			input := strings.TrimSpace(a.form.fields[0].value)
			if input == "" {
				return errMsg{errors.New("task needs a description")}
			}
			task, err := a.client.QuickAdd(ctx, input)
			if err != nil {
				return errMsg{err}
			}
			a.screen = a.prevScreen
			return tea.Batch(a.loadTasks(), func() tea.Msg {
				return statusMsg("added: " + formatTask(task))
			})()
		case formKindHours:
			hours, err := parseHoursForm(a.form.fields)
			if err != nil {
				return errMsg{err}
			}
			if _, err := a.client.ReplaceWorkingHours(ctx, hours); err != nil {
				return errMsg{err}
			}
			a.screen = a.prevScreen
			return statusMsg("working hours saved")
		}
		return statusMsg("")
	}
}

func (a *App) deleteProject(id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := a.client.DeleteProject(ctx, id); err != nil {
			return errMsg{err}
		}
		return statusMsg("project deleted")
	}
}

func (a *App) deleteHabit(id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := a.client.DeleteHabit(ctx, id); err != nil {
			return errMsg{err}
		}
		return statusMsg("habit deleted")
	}
}

func parseProjectForm(fs []formField) (Project, error) {
	if len(fs) < 4 {
		return Project{}, errors.New("form incomplete")
	}
	hours, err := strconv.ParseFloat(strings.TrimSpace(fs[2].value), 64)
	if err != nil {
		return Project{}, errors.New("target hours must be a number")
	}
	p := Project{
		Name:        strings.TrimSpace(fs[0].value),
		Kind:        strings.TrimSpace(fs[1].value),
		TargetHours: hours,
	}
	if dl := strings.TrimSpace(fs[3].value); dl != "" {
		t, err := time.ParseInLocation("2006-01-02", dl, time.Local)
		if err != nil {
			return Project{}, errors.New("deadline must be YYYY-MM-DD")
		}
		p.Deadline = &t
	}
	return p, nil
}

func parseHabitForm(fs []formField) (Habit, error) {
	if len(fs) < 4 {
		return Habit{}, errors.New("form incomplete")
	}
	mins, err := strconv.Atoi(strings.TrimSpace(fs[2].value))
	if err != nil {
		return Habit{}, errors.New("block minutes must be integer")
	}
	count, err := strconv.Atoi(strings.TrimSpace(fs[3].value))
	if err != nil {
		return Habit{}, errors.New("per_week count must be integer")
	}
	return Habit{
		Name:                 strings.TrimSpace(fs[0].value),
		Kind:                 strings.TrimSpace(fs[1].value),
		BlockDurationMinutes: mins,
		Cadence:              Cadence{Type: "per_week", Count: count},
		Active:               true,
	}, nil
}
