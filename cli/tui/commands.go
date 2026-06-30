package tui

import (
	"context"
	"fmt"
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

const (
	triagePollInterval = 2 * time.Second
	triagePollTimeout  = 8 * time.Minute
)

// triage kicks off a detached server-side triage pass, then polls the runs
// list until it lands. The work survives even if the TUI exits mid-poll.
func triage(c *Client) tea.Cmd {
	return func() tea.Msg {
		// Snapshot the latest finished triage run so we can tell the run we
		// trigger apart from an earlier one.
		baseline, err := latestTriageID(c)
		if err != nil {
			return errMsg{err}
		}
		startCtx, cancel := bg()
		err = c.Triage(startCtx)
		cancel()
		if err != nil {
			return errMsg{err}
		}
		deadline := timeNow().Add(triagePollTimeout)
		for timeNow().Before(deadline) {
			time.Sleep(triagePollInterval)
			latest, err := latestTriageRun(c)
			if err != nil {
				continue
			}
			if settled(latest, baseline) {
				if latest.Status == "failed" {
					return errMsg{fmt.Errorf("triage failed: %s", latest.Error)}
				}
				return statusMsg("triage done")
			}
		}
		return statusMsg("triage still running…")
	}
}

// settled reports whether a triage run distinct from baseline has finished.
func settled(latest *AgentRun, baseline string) bool {
	return latest != nil && latest.Status != "running" && latest.ID != baseline
}

func latestTriageRun(c *Client) (*AgentRun, error) {
	ctx, cancel := bg()
	defer cancel()
	runs, err := c.ListRuns(ctx, "triage", 1)
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, nil
	}
	return &runs[0], nil
}

// latestTriageID returns the id of the most recent finished triage run, or ""
// when the latest is still running or none exist — i.e. no baseline to exclude.
func latestTriageID(c *Client) (string, error) {
	latest, err := latestTriageRun(c)
	if err != nil {
		return "", err
	}
	if latest == nil || latest.Status == "running" {
		return "", nil
	}
	return latest.ID, nil
}
