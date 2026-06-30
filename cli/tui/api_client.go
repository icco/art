// Package tui implements the art terminal UI client.
package tui

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
// commands.go and cap the 5-min triage/replan passes.
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
		return "", errors.New("gcloud returned an empty token")
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
		return time.Time{}, errors.New("not a JWT")
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
		return time.Time{}, errors.New("exp claim missing")
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
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(raw)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// Project mirrors the API project resource.
type Project struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Kind           string     `json:"kind"`
	TargetHours    float64    `json:"target_hours"`
	ScheduledHours float64    `json:"scheduled_hours"`
	Deadline       *time.Time `json:"deadline,omitempty"`
	Status         string     `json:"status"`
}

// Habit mirrors the API habit resource.
type Habit struct {
	ID                   string  `json:"id"`
	Name                 string  `json:"name"`
	Description          string  `json:"description"`
	Kind                 string  `json:"kind"`
	BlockDurationMinutes int     `json:"block_duration_minutes"`
	Cadence              Cadence `json:"cadence"`
	Active               bool    `json:"active"`
}

// Cadence describes how often a habit should occur.
type Cadence struct {
	Type             string   `json:"type"`
	Count            int      `json:"count"`
	PreferredWindows []string `json:"preferred_windows,omitempty"`
}

// Event mirrors the API calendar event resource.
type Event struct {
	ID           string    `json:"id"`
	AccountKind  string    `json:"account_kind"`
	Summary      string    `json:"summary"`
	StartTime    time.Time `json:"start_time"`
	EndTime      time.Time `json:"end_time"`
	AllDay       bool      `json:"all_day"`
	EventType    string    `json:"event_type"`
	IsArtManaged bool      `json:"is_art_managed"`
}

// AgentRun summarises a planner or triage run reported by the API.
type AgentRun struct {
	ID        string          `json:"id"`
	Kind      string          `json:"kind"`
	Status    string          `json:"status"`
	StartedAt time.Time       `json:"started_at"`
	EndedAt   *time.Time      `json:"ended_at,omitempty"`
	Summary   json.RawMessage `json:"summary"`
	Error     string          `json:"error"`
}

// Session mirrors a planner-scheduled focus block (project or habit).
type Session struct {
	ID             string    `json:"id"`
	Source         string    `json:"source"` // "project" or "habit"
	SourceID       string    `json:"source_id"`
	AccountKind    string    `json:"account_kind"`
	ScheduledStart time.Time `json:"scheduled_start"`
	ScheduledEnd   time.Time `json:"scheduled_end"`
	Status         string    `json:"status"` // planned|happened|skipped|moved
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
	Reversed    bool      `json:"reversed"`
	ReceivedAt  time.Time `json:"received_at"`
}

// ListProjects returns all projects visible to the caller.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var out []Project
	return out, c.do(ctx, "GET", "/projects?limit=500", nil, &out)
}

// CreateProject creates a new project from p.
func (c *Client) CreateProject(ctx context.Context, p Project) (Project, error) {
	var out Project
	return out, c.do(ctx, "POST", "/projects", p, &out)
}

// UpdateProject patches the project with the given id.
func (c *Client) UpdateProject(ctx context.Context, id string, p Project) (Project, error) {
	var out Project
	return out, c.do(ctx, "PATCH", "/projects/"+id, p, &out)
}

// DeleteProject removes the project with the given id.
func (c *Client) DeleteProject(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/projects/"+id, nil, nil)
}

// ListHabits returns all habits visible to the caller.
func (c *Client) ListHabits(ctx context.Context) ([]Habit, error) {
	var out []Habit
	return out, c.do(ctx, "GET", "/habits?limit=500", nil, &out)
}

// CreateHabit creates a new habit from h.
func (c *Client) CreateHabit(ctx context.Context, h Habit) (Habit, error) {
	var out Habit
	return out, c.do(ctx, "POST", "/habits", h, &out)
}

// UpdateHabit patches the habit with the given id.
func (c *Client) UpdateHabit(ctx context.Context, id string, h Habit) (Habit, error) {
	var out Habit
	return out, c.do(ctx, "PATCH", "/habits/"+id, h, &out)
}

// DeleteHabit removes the habit with the given id.
func (c *Client) DeleteHabit(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/habits/"+id, nil, nil)
}

// ListEvents returns events between from and to (inclusive of from, exclusive of to).
func (c *Client) ListEvents(ctx context.Context, from, to time.Time) ([]Event, error) {
	q := fmt.Sprintf("?from=%s&to=%s", from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
	var out []Event
	return out, c.do(ctx, "GET", "/events"+q, nil, &out)
}

// ListSessions returns planner-scheduled focus blocks between from and to.
func (c *Client) ListSessions(ctx context.Context, from, to time.Time) ([]Session, error) {
	q := fmt.Sprintf("?from=%s&to=%s", from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
	var out []Session
	return out, c.do(ctx, "GET", "/sessions"+q, nil, &out)
}

// ListRuns returns recent agent runs, newest first. kind is optional
// ("planner" or "triage"); limit caps the count.
func (c *Client) ListRuns(ctx context.Context, kind string, limit int) ([]AgentRun, error) {
	q := fmt.Sprintf("?limit=%d", limit)
	if kind != "" {
		q += "&kind=" + kind
	}
	var out []AgentRun
	return out, c.do(ctx, "GET", "/agent-runs"+q, nil, &out)
}

// Replan triggers a planner run on the server and returns its result.
func (c *Client) Replan(ctx context.Context) (AgentRun, error) {
	var out AgentRun
	return out, c.do(ctx, "POST", "/replan", nil, &out)
}

// Sync triggers a sync of upstream calendars on the server.
func (c *Client) Sync(ctx context.Context) error {
	return c.do(ctx, "POST", "/sync", nil, nil)
}

// ListEmails returns recently triaged messages, newest first.
func (c *Client) ListEmails(ctx context.Context) ([]Email, error) {
	var out []Email
	return out, c.do(ctx, "GET", "/emails?limit=200", nil, &out)
}

// Triage triggers an email-triage pass on the server.
func (c *Client) Triage(ctx context.Context) error {
	return c.do(ctx, "POST", "/triage/run", nil, nil)
}

// ReverseEmail marks a triaged decision bad and undoes it server-side.
func (c *Client) ReverseEmail(ctx context.Context, id string) (Email, error) {
	var out Email
	return out, c.do(ctx, "POST", "/emails/"+id+"/reverse", nil, &out)
}
