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

type Client struct {
	cfg Config
	hc  *http.Client

	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

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

type Project struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Kind        string     `json:"kind"`
	TargetHours float64    `json:"target_hours"`
	Deadline    *time.Time `json:"deadline,omitempty"`
	Status      string     `json:"status"`
}

type Habit struct {
	ID                   string  `json:"id"`
	Name                 string  `json:"name"`
	Description          string  `json:"description"`
	Kind                 string  `json:"kind"`
	BlockDurationMinutes int     `json:"block_duration_minutes"`
	Cadence              Cadence `json:"cadence"`
	Active               bool    `json:"active"`
}

type Cadence struct {
	Type             string   `json:"type"`
	Count            int      `json:"count"`
	PreferredWindows []string `json:"preferred_windows,omitempty"`
}

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

type AgentRun struct {
	ID      string          `json:"id"`
	Status  string          `json:"status"`
	Summary json.RawMessage `json:"summary"`
	Error   string          `json:"error"`
}

func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	var out []Project
	return out, c.do(ctx, "GET", "/projects?limit=500", nil, &out)
}

func (c *Client) CreateProject(ctx context.Context, p Project) (Project, error) {
	var out Project
	return out, c.do(ctx, "POST", "/projects", p, &out)
}

func (c *Client) DeleteProject(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/projects/"+id, nil, nil)
}

func (c *Client) ListHabits(ctx context.Context) ([]Habit, error) {
	var out []Habit
	return out, c.do(ctx, "GET", "/habits?limit=500", nil, &out)
}

func (c *Client) CreateHabit(ctx context.Context, h Habit) (Habit, error) {
	var out Habit
	return out, c.do(ctx, "POST", "/habits", h, &out)
}

func (c *Client) DeleteHabit(ctx context.Context, id string) error {
	return c.do(ctx, "DELETE", "/habits/"+id, nil, nil)
}

func (c *Client) ListEvents(ctx context.Context, from, to time.Time) ([]Event, error) {
	q := fmt.Sprintf("?from=%s&to=%s", from.UTC().Format(time.RFC3339), to.UTC().Format(time.RFC3339))
	var out []Event
	return out, c.do(ctx, "GET", "/events"+q, nil, &out)
}

func (c *Client) Replan(ctx context.Context) (AgentRun, error) {
	var out AgentRun
	return out, c.do(ctx, "POST", "/replan", nil, &out)
}

func (c *Client) Sync(ctx context.Context) error {
	return c.do(ctx, "POST", "/sync", nil, nil)
}
