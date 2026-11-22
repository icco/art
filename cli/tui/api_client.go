// Package tui implements the art terminal UI client.
package tui

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Client is an authenticated HTTP client for the art API.
type Client struct {
	cfg Config
	hc  *http.Client

	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

// NewClient returns a Client configured to talk to the API endpoint in cfg.
//
// No client Timeout: it would override the per-request context deadlines in
// commands.go and cap the 5-min triage passes.
func NewClient(cfg Config) *Client {
	return &Client{cfg: cfg, hc: &http.Client{}}
}

func (c *Client) idToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenExp.Add(-30*time.Second)) {
		return c.token, nil
	}
	// No --audiences: gcloud rejects it for user accounts. The token's audience
	// is gcloud's client ID, which the server checks against OIDC_AUDIENCE.
	cmd := exec.CommandContext(ctx, "gcloud", "auth", "print-identity-token")
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("gcloud auth print-identity-token failed: %w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	tok := strings.TrimSpace(out.String())
	if tok == "" {
		return "", errEmptyToken
	}
	exp, err := jwtExp(tok)
	if err != nil {
		return "", fmt.Errorf("parse id token: %w", err)
	}
	c.token = tok
	c.tokenExp = exp
	return tok, nil
}

// The token comes from local gcloud so we trust it without verifying;
// we only need exp to decide when to refresh.
func jwtExp(tok string) (time.Time, error) {
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		return time.Time{}, errNotJWT
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return time.Time{}, err
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}, err
	}
	if claims.Exp == 0 {
		return time.Time{}, errNoExpClaim
	}
	return time.Unix(claims.Exp, 0), nil
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reqBody = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.APIURL+path, reqBody)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	tok, err := c.idToken(ctx)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+tok)

	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%w: %s %s: %s: %s", errAPIStatus, method, path, resp.Status, strings.TrimSpace(string(raw)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// AgentRun summarises a triage run reported by the API.
type AgentRun struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Status    string          `json:"status"`
	StartedAt time.Time       `json:"started_at"`
	EndedAt   *time.Time      `json:"ended_at,omitempty"`
	Summary   json.RawMessage `json:"summary"`
	Error     string          `json:"error"`
}

// Email mirrors the API triaged-message resource.
type Email struct {
	ID          string    `json:"id"`
	AccountKind string    `json:"account_kind"`
	From        string    `json:"from"`
	Subject     string    `json:"subject"`
	Summary     string    `json:"summary"`
	Category    string    `json:"category"`
	Action      string    `json:"action"`
	Applied     bool      `json:"applied"`
	Archived    bool      `json:"archived"`
	Reversed    bool      `json:"reversed"`
	ReceivedAt  time.Time `json:"received_at"`
}

// Settings mirrors the API settings resource: the runtime-editable knobs only.
type Settings struct {
	TriageEnabled             bool    `json:"triage_enabled"`
	TriageDryRun              bool    `json:"triage_dry_run"`
	TriageConfidenceThreshold float64 `json:"triage_confidence_threshold"`
	TriageBackfillDays        int     `json:"triage_backfill_days"`
	TriageReconcileDays       int     `json:"triage_reconcile_days"`
	DailyBudgetUSD            float64 `json:"daily_budget_usd"`
}

// GetSettings returns the current runtime settings.
func (c *Client) GetSettings(ctx context.Context) (Settings, error) {
	var out Settings
	return out, c.do(ctx, "GET", "/settings", nil, &out)
}

// UpdateSettings writes s and returns the stored result.
func (c *Client) UpdateSettings(ctx context.Context, s Settings) (Settings, error) {
	var out Settings
	return out, c.do(ctx, "PUT", "/settings", s, &out)
}

// ListRuns returns recent agent runs, newest first. kind is optional
// (e.g. "triage"); limit caps the count.
func (c *Client) ListRuns(ctx context.Context, kind string, limit int) ([]AgentRun, error) {
	q := fmt.Sprintf("?limit=%d", limit)
	if kind != "" {
		q += "&kind=" + kind
	}
	var out []AgentRun
	return out, c.do(ctx, "GET", "/agent-runs"+q, nil, &out)
}

// ListEmails returns recently triaged messages, newest first.
func (c *Client) ListEmails(ctx context.Context) ([]Email, error) {
	var out []Email
	return out, c.do(ctx, "GET", "/emails?limit=200", nil, &out)
}

// Job mirrors the API job resource.
type Job struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	LastError string `json:"last_error"`
}

// GetJob returns a job by id.
func (c *Client) GetJob(ctx context.Context, id string) (Job, error) {
	var out Job
	return out, c.do(ctx, "GET", "/jobs/"+id, nil, &out)
}

// TriageJob triggers a detached triage pass and returns the status and enqueued job.
func (c *Client) TriageJob(ctx context.Context) (string, Job, error) {
	var out struct {
		Status string `json:"status"`
		Job    Job    `json:"job"`
	}
	return out.Status, out.Job, c.do(ctx, "POST", "/triage/run", nil, &out)
}

// Triage triggers a detached triage pass and returns "started" or "running".
func (c *Client) Triage(ctx context.Context) (string, error) {
	status, _, err := c.TriageJob(ctx)
	return status, err
}

// ReverseEmail marks a triaged decision bad and undoes it server-side.
func (c *Client) ReverseEmail(ctx context.Context, id string) (Email, error) {
	var out Email
	return out, c.do(ctx, "POST", "/emails/"+id+"/reverse", nil, &out)
}

// SetEmailArchived moves a triaged message between the inbox and the archive.
func (c *Client) SetEmailArchived(ctx context.Context, id string, archived bool) (Email, error) {
	var out Email
	return out, c.do(ctx, "POST", "/emails/"+id+"/archive", map[string]bool{"archived": archived}, &out)
}
