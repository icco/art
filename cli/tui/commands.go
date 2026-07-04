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
		return eventsMsg{events: ev, from: from, to: to}
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

func reverseEmail(c *Client, id string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := bg()
		defer cancel()
		if _, err := c.ReverseEmail(ctx, id); err != nil {
			return errMsg{err}
		}
		return statusMsg("decision reversed")
	}
}

func setEmailArchived(c *Client, id string, archived bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := bg()
		defer cancel()
		if _, err := c.SetEmailArchived(ctx, id, archived); err != nil {
			return errMsg{err}
		}
		if archived {
			return statusMsg("email archived")
		}
		return statusMsg("email moved to inbox")
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

// replan starts a detached planner pass and polls until it lands.
func replan(c *Client) tea.Cmd {
	return func() tea.Msg {
		return startAndAwait(c, "planner", c.Replan, "replan")
	}
}

// syncCalendars enqueues a sync job and polls it until it lands.
func syncCalendars(c *Client) tea.Cmd {
	return func() tea.Msg {
		startCtx, cancel := bg()
		job, err := c.Sync(startCtx)
		cancel()
		if err != nil {
			return errMsg{err}
		}
		var pollErr error
		deadline := timeNow().Add(triagePollTimeout)
		for timeNow().Before(deadline) {
			time.Sleep(triagePollInterval)
			pollCtx, cancel := bg()
			j, err := c.GetJob(pollCtx, job.ID)
			cancel()
			if err != nil {
				pollErr = err // transient poll errors retry, but keep the last one
				continue
			}
			pollErr = nil
			switch j.Status {
			case "succeeded":
				if j.LastError != "" {
					return statusMsg("sync done (account errors: " + j.LastError + ")")
				}
				return statusMsg("sync done")
			case "failed":
				return errMsg{fmt.Errorf("sync failed: %s", j.LastError)}
			}
		}
		if pollErr != nil {
			return errMsg{fmt.Errorf("sync status unknown: %w", pollErr)}
		}
		return statusMsg("sync still running…")
	}
}

const (
	triagePollInterval = 2 * time.Second
	triagePollTimeout  = 8 * time.Minute
)

// triage starts a detached triage pass and polls until it lands.
func triage(c *Client) tea.Cmd {
	return func() tea.Msg {
		return startAndAwait(c, "triage", c.Triage, "triage")
	}
}

// startAndAwait triggers a detached pass and polls until a run past the
// baseline settles.
func startAndAwait(c *Client, kind string, start func(context.Context) (string, error), label string) tea.Msg {
	latest, err := latestRunOf(c, kind)
	if err != nil {
		return errMsg{err}
	}
	startCtx, cancel := bg()
	status, err := start(startCtx)
	cancel()
	if err != nil {
		return errMsg{err}
	}
	baseline := pollBaseline(latest, status)

	deadline := timeNow().Add(triagePollTimeout)
	for timeNow().Before(deadline) {
		time.Sleep(triagePollInterval)
		latest, err := latestRunOf(c, kind)
		if err != nil {
			continue
		}
		if settled(latest, baseline) {
			if latest.Status == "failed" {
				return errMsg{fmt.Errorf("%s failed: %s", label, latest.Error)}
			}
			return statusMsg(label + " done")
		}
	}
	return statusMsg(label + " still running…")
}

// settled reports whether a run distinct from baseline has finished.
func settled(latest *AgentRun, baseline string) bool {
	return latest != nil && latest.Status != "running" && latest.ID != baseline
}

// pollBaseline picks the run whose completion should not count as ours:
// "started" excludes whatever was latest; "running" awaits the in-flight run.
func pollBaseline(latest *AgentRun, serverStatus string) string {
	if latest == nil {
		return ""
	}
	if serverStatus == "running" && latest.Status == "running" {
		return ""
	}
	return latest.ID
}

func latestRunOf(c *Client, kind string) (*AgentRun, error) {
	ctx, cancel := bg()
	defer cancel()
	runs, err := c.ListRuns(ctx, kind, 1)
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, nil
	}
	return &runs[0], nil
}
