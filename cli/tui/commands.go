package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
)

// cmdTimeout bounds a normal data request. Long agent actions use longer ones.
const cmdTimeout = 20 * time.Second

func bg() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), cmdTimeout)
}

func loadEvents(c *Client, from, to time.Time) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := bg()
		defer cancel()
		ev, err := c.ListEvents(ctx, from, to)
		if err != nil {
			return errMsg{err}
		}
		return eventsMsg{ev}
	}
}

func loadProjects(c *Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := bg()
		defer cancel()
		ps, err := c.ListProjects(ctx)
		if err != nil {
			return errMsg{err}
		}
		return projectsMsg{ps}
	}
}

func loadHabits(c *Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := bg()
		defer cancel()
		hs, err := c.ListHabits(ctx)
		if err != nil {
			return errMsg{err}
		}
		return habitsMsg{hs}
	}
}

func loadSessions(c *Client, from, to time.Time) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := bg()
		defer cancel()
		ss, err := c.ListSessions(ctx, from, to)
		if err != nil {
			return errMsg{err}
		}
		return sessionsMsg{ss}
	}
}

func loadEmails(c *Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := bg()
		defer cancel()
		es, err := c.ListEmails(ctx)
		if err != nil {
			return errMsg{err}
		}
		return emailsMsg{es}
	}
}

func loadRuns(c *Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := bg()
		defer cancel()
		rs, err := c.ListRuns(ctx, "", 20)
		if err != nil {
			return errMsg{err}
		}
		return runsMsg{rs}
	}
}

func createProject(c *Client, p Project) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := bg()
		defer cancel()
		if _, err := c.CreateProject(ctx, p); err != nil {
			return errMsg{err}
		}
		return statusMsg("project created")
	}
}

func updateProject(c *Client, id string, p Project) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := bg()
		defer cancel()
		if _, err := c.UpdateProject(ctx, id, p); err != nil {
			return errMsg{err}
		}
		return statusMsg("project updated")
	}
}

func deleteProject(c *Client, id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := bg()
		defer cancel()
		if err := c.DeleteProject(ctx, id); err != nil {
			return errMsg{err}
		}
		return statusMsg("project deleted")
	}
}

func createHabit(c *Client, h Habit) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := bg()
		defer cancel()
		if _, err := c.CreateHabit(ctx, h); err != nil {
			return errMsg{err}
		}
		return statusMsg("habit created")
	}
}

func updateHabit(c *Client, id string, h Habit) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := bg()
		defer cancel()
		if _, err := c.UpdateHabit(ctx, id, h); err != nil {
			return errMsg{err}
		}
		return statusMsg("habit updated")
	}
}

func deleteHabit(c *Client, id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := bg()
		defer cancel()
		if err := c.DeleteHabit(ctx, id); err != nil {
			return errMsg{err}
		}
		return statusMsg("habit deleted")
	}
}

func replan(c *Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		run, err := c.Replan(ctx)
		if err != nil {
			return errMsg{err}
		}
		return statusMsg("replan: " + run.Status)
	}
}

func syncCalendars(c *Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if err := c.Sync(ctx); err != nil {
			return errMsg{err}
		}
		return statusMsg("sync done")
	}
}

func triage(c *Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := c.Triage(ctx); err != nil {
			return errMsg{err}
		}
		return statusMsg("triage done")
	}
}
