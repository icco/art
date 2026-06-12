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
func NewClient(cfg Config) *Client {
	return &Client{cfg: cfg, hc: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) idToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenExp.Add(-30*time.Second)) {
		return c.token, nil
	}
	// #nosec G204 -- audience comes from server config, not user input.
	cmd := exec.CommandContext(ctx, "gcloud", "auth", "print-identity-token",
		"--audiences="+c.cfg.Audience)
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
	Kind        string     `json:"kind"`
	TargetHours float64    `json:"target_hours"`
	Deadline    *time.Time `json:"deadline,omitempty"`
	Status      string     `json:"status"`
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

// AgentRun summarises a planner or sync run reported by the API.
type AgentRun struct {
	ID        string          `json:"id"`
	StartedAt time.Time       `json:"started_at"`
	Status    string          `json:"status"`
	Model     string          `json:"model"`
	Summary   json.RawMessage `json:"summary"`
	Error     string          `json:"error"`
}

// Task mirrors the API task resource.
type Task struct {
	ID              string     `json:"id"`
	Title           string     `json:"title"`
	Kind            string     `json:"kind"`
	DurationMinutes int        `json:"duration_minutes"`
	Deadline        *time.Time `json:"deadline,omitempty"`
	Status          string     `json:"status"`
	Notes           string     `json:"notes"`
}

// UpcomingBlock is one scheduled focus block from the status endpoint.
type UpcomingBlock struct {
	SessionID   string    `json:"session_id"`
	Source      string    `json:"source"`
	SourceID    string    `json:"source_id"`
	Title       string    `json:"title"`
	AccountKind string    `json:"account_kind"`
	Start       time.Time `json:"start"`
	End         time.Time `json:"end"`
	Status      string    `json:"status"`
}

// WorkingHour mirrors the API working-hours resource.
type WorkingHour struct {
	SlotKind    string `json:"slot_kind"`
	DayOfWeek   int    `json:"day_of_week"`
	StartMinute int    `json:"start_minute"`
	EndMinute   int    `json:"end_minute"`
}

// StatusReport is the aggregated /status response.
type StatusReport struct {
	Upcoming           []UpcomingBlock `json:"upcoming"`
	TasksPending       []Task          `json:"tasks_pending"`
	TasksUnschedulable []Task          `json:"tasks_unschedulable"`
	LastRun            *AgentRun       `json:"last_run,omitempty"`
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

// QuickAdd captures a one-line task ("pack office 2h by friday"); the
// server parses it and returns the created task so the caller can echo what
// was understood.
func (c *Client) QuickAdd(ctx context.Context, input string) (Task, error) {
	var out Task
	return out, c.do(ctx, "POST", "/tasks/quickadd", map[string]string{"input": input}, &out)
}

// ListTasks returns tasks, optionally filtered by comma-separated statuses.
func (c *Client) ListTasks(ctx context.Context, statuses string) ([]Task, error) {
	path := "/tasks?limit=500"
	if statuses != "" {
		path += "&status=" + statuses
	}
	var out []Task
	return out, c.do(ctx, "GET", path, nil, &out)
}

// UpdateTask applies a partial update to a task.
func (c *Client) UpdateTask(ctx context.Context, id string, patch map[string]any) (Task, error) {
	var out Task
	return out, c.do(ctx, "PATCH", "/tasks/"+id, patch, &out)
}

// DeleteTask removes the task with the given id.
func (c *Client) DeleteTask(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/tasks/"+id, nil, nil)
}

// ListWorkingHours returns all configured working-hour windows.
func (c *Client) ListWorkingHours(ctx context.Context) ([]WorkingHour, error) {
	var out []WorkingHour
	return out, c.do(ctx, "GET", "/working-hours", nil, &out)
}

// ReplaceWorkingHours atomically replaces the entire working-hours table;
// the API is replace-only, so always send the full set.
func (c *Client) ReplaceWorkingHours(ctx context.Context, hours []WorkingHour) ([]WorkingHour, error) {
	if hours == nil {
		hours = []WorkingHour{}
	}
	var out []WorkingHour
	return out, c.do(ctx, "PUT", "/working-hours", hours, &out)
}

// Status returns the aggregated status report.
func (c *Client) Status(ctx context.Context) (StatusReport, error) {
	var out StatusReport
	return out, c.do(ctx, "GET", "/status", nil, &out)
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
