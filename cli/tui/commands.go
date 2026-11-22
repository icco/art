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

func loadSettings(c *Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := bg()
		defer cancel()
		s, err := c.GetSettings(ctx)
		if err != nil {
			return errMsg{err}
		}
		return settingsMsg{s}
	}
}

func saveSettings(c *Client, s Settings) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := bg()
		defer cancel()
		if _, err := c.UpdateSettings(ctx, s); err != nil {
			return errMsg{err}
		}
		return statusMsg("settings saved")
	}
}

func loadRuns(c *Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := bg()
		defer cancel()
		rs, err := c.ListRuns(ctx, "triage", 20)
		if err != nil {
			return errMsg{err}
		}
		return runsMsg{rs}
	}
}

// triageKind is both the agent-run kind polled and the status label.
const triageKind = "triage"

const (
	triagePollInterval       = 2 * time.Second
	triagePollTimeout        = 8 * time.Minute
	maxConsecutivePollErrors = 3
)

// triage starts a detached triage pass and polls until it lands.
func triage(c *Client) tea.Cmd {
	return func() tea.Msg {
		return startAndAwaitJob(c, func(ctx context.Context) (string, string, error) {
			status, job, err := c.TriageJob(ctx)
			return status, job.ID, err
		})
	}
}

// startAndAwaitJob triggers a detached pass and polls until a run past the
// baseline settles or the enqueued job finishes.
func startAndAwaitJob(c *Client, start func(context.Context) (string, string, error)) tea.Msg {
	latest, err := latestRunOf(c, triageKind)
	if err != nil {
		return errMsg{err}
	}
	startCtx, cancel := bg()
	status, jobID, err := start(startCtx)
	cancel()
	if err != nil {
		return errMsg{err}
	}
	baseline := pollBaseline(latest, status)

	runFails, jobFails := 0, 0
	deadline := timeNow().Add(triagePollTimeout)
	for timeNow().Before(deadline) {
		timeSleep(triagePollInterval)
		latest, err := latestRunOf(c, triageKind)
		if err != nil {
			runFails++
			if runFails >= maxConsecutivePollErrors {
				return errMsg{err}
			}
		} else {
			runFails = 0
			if settled(latest, baseline) {
				return runOutcome(latest)
			}
		}

		if jobID == "" {
			continue
		}
		jobCtx, jobCancel := bg()
		job, err := c.GetJob(jobCtx, jobID)
		jobCancel()
		if err != nil {
			jobFails++
			if jobFails >= maxConsecutivePollErrors {
				return errMsg{err}
			}
			continue
		}
		jobFails = 0
		switch job.Status {
		case "failed":
			if job.LastError != "" {
				return errMsg{fmt.Errorf("%s %w: %s", triageKind, errJobFailed, job.LastError)}
			}
			return errMsg{fmt.Errorf("%s %w", triageKind, errJobFailed)}
		case "succeeded":
			latest, err := latestRunOf(c, triageKind)
			if err != nil {
				return errMsg{err}
			}
			if settled(latest, baseline) {
				return runOutcome(latest)
			}
			return statusMsg(triageKind + " finished, no run recorded (disabled?)")
		}
	}
	return statusMsg(triageKind + " still running…")
}

func runOutcome(run *AgentRun) tea.Msg {
	if run.Status == "failed" {
		return errMsg{fmt.Errorf("%s %w: %s", triageKind, errJobFailed, run.Error)}
	}
	return statusMsg(triageKind + " done")
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
