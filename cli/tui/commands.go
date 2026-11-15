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
				return errMsg{fmt.Errorf("%s %w: %s", label, errJobFailed, latest.Error)}
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
