// Package config loads art's runtime configuration from environment variables.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
)

// Config is the resolved runtime configuration for the art server.
type Config struct {
	Port           string
	DatabaseURL    string
	OwnerEmails    []string
	OIDCAudience   string
	Timezone       *time.Location
	TokenEncKey    []byte
	OAuth          OAuthConfig
	Vertex         VertexConfig
	CredentialsEnv string
	Planner        PlannerMode
	CronInterval   time.Duration
}

// PlannerMode selects which planning engine the cron and replan paths use.
type PlannerMode string

// Planner modes. Deterministic needs no external services; llm requires
// Vertex AI credentials and falls back to deterministic on failure.
const (
	PlannerDeterministic PlannerMode = "deterministic"
	PlannerLLM           PlannerMode = "llm"
)

// OAuthConfig holds Google OAuth client credentials used for account linking.
type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// VertexConfig holds Vertex AI project, region, and model settings for the LLM.
type VertexConfig struct {
	ProjectID string
	Location  string
	Model     string
}

// Load reads configuration from the process environment and validates it.
func Load() (*Config, error) {
	c := &Config{
		Port:           envOr("PORT", "8080"),
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		OIDCAudience:   os.Getenv("OIDC_AUDIENCE"),
		CredentialsEnv: os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"),
		OAuth: OAuthConfig{
			ClientID:     os.Getenv("GOOGLE_OAUTH_CLIENT_ID"),
			ClientSecret: os.Getenv("GOOGLE_OAUTH_CLIENT_SECRET"),
			RedirectURL:  envOr("GOOGLE_OAUTH_REDIRECT_URL", "http://localhost:8080/oauth/callback"),
		},
		Vertex: VertexConfig{
			ProjectID: os.Getenv("VERTEX_PROJECT_ID"),
			Location:  envOr("VERTEX_LOCATION", "us-central1"),
			Model:     envOr("VERTEX_MODEL", "gemini-3.1-pro"),
		},
		Planner: PlannerMode(envOr("ART_PLANNER", string(PlannerDeterministic))),
	}

	interval, err := time.ParseDuration(envOr("ART_CRON_INTERVAL", "1h"))
	if err != nil {
		return nil, fmt.Errorf("invalid ART_CRON_INTERVAL: %w", err)
	}
	c.CronInterval = interval

	for e := range strings.SplitSeq(os.Getenv("OWNER_EMAILS"), ",") {
		e = strings.TrimSpace(strings.ToLower(e))
		if e != "" {
			c.OwnerEmails = append(c.OwnerEmails, e)
		}
	}

	tz, err := time.LoadLocation(envOr("ART_TIMEZONE", "America/Los_Angeles"))
	if err != nil {
		return nil, fmt.Errorf("invalid ART_TIMEZONE: %w", err)
	}
	c.Timezone = tz

	key, err := decodeKey(os.Getenv("TOKEN_ENCRYPTION_KEY"))
	if err != nil {
		return nil, err
	}
	c.TokenEncKey = key

	return c, c.validate()
}

func (c *Config) validate() error {
	var missing []string
	if c.DatabaseURL == "" {
		missing = append(missing, "DATABASE_URL")
	}
	if len(c.OwnerEmails) == 0 {
		missing = append(missing, "OWNER_EMAILS")
	}
	if c.OAuth.ClientID == "" {
		missing = append(missing, "GOOGLE_OAUTH_CLIENT_ID")
	}
	if c.OAuth.ClientSecret == "" {
		missing = append(missing, "GOOGLE_OAUTH_CLIENT_SECRET")
	}
	if len(c.TokenEncKey) == 0 {
		missing = append(missing, "TOKEN_ENCRYPTION_KEY")
	}
	if c.OIDCAudience == "" {
		missing = append(missing, "OIDC_AUDIENCE")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required env vars: %s", strings.Join(missing, ", "))
	}
	switch c.Planner {
	case PlannerDeterministic:
	case PlannerLLM:
		if c.Vertex.ProjectID == "" {
			return errors.New("ART_PLANNER=llm requires VERTEX_PROJECT_ID")
		}
	default:
		return fmt.Errorf("invalid ART_PLANNER %q (want %q or %q)", c.Planner, PlannerDeterministic, PlannerLLM)
	}
	return nil
}

// LLMEnabled reports whether the LLM planner should be used.
func (c *Config) LLMEnabled() bool {
	return c.Planner == PlannerLLM && c.Vertex.ProjectID != ""
}

// OwnerAllowed reports whether email is in the configured OwnerEmails list.
func (c *Config) OwnerAllowed(email string) bool {
	return slices.Contains(c.OwnerEmails, strings.ToLower(strings.TrimSpace(email)))
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func decodeKey(raw string) ([]byte, error) {
	if raw == "" {
		return nil, nil
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(raw)
	}
	if err != nil {
		return nil, fmt.Errorf("TOKEN_ENCRYPTION_KEY: %w", err)
	}
	if len(key) != 32 {
		return nil, errors.New("TOKEN_ENCRYPTION_KEY must decode to 32 bytes (AES-256)")
	}
	return key, nil
}
