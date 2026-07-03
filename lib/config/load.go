// Package config loads art's runtime configuration from environment variables.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
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
	Triage         TriageConfig
	RateLimitRPM   int
}

// TriageConfig controls the Gmail email-triage agent.
type TriageConfig struct {
	Enabled             bool
	DryRun              bool
	BackfillDays        int
	MaxPerRun           int
	ConfidenceThreshold float64
	ReconcileDays       int
}

// OAuthConfig holds Google OAuth client credentials used for account linking.
type OAuthConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// VertexModel is hardcoded to the latest GA Gemini model on Vertex AI.
// Update here when Google ships a newer GA release.
const VertexModel = "gemini-2.5-pro"

// VertexConfig holds Vertex AI project and region settings for the LLM.
type VertexConfig struct {
	ProjectID string
	Location  string
}

// Load reads configuration from the process environment and validates it.
func Load() (*Config, error) {
	p := &envParser{}
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
		},
		Triage: TriageConfig{
			Enabled:             p.boolVar("TRIAGE_ENABLED", true),
			DryRun:              p.boolVar("TRIAGE_DRY_RUN", false),
			BackfillDays:        p.intVar("TRIAGE_BACKFILL_DAYS", 7),
			MaxPerRun:           p.intVar("TRIAGE_MAX_PER_RUN", 1000),
			ConfidenceThreshold: p.floatVar("TRIAGE_CONFIDENCE_THRESHOLD", 0.8),
			ReconcileDays:       p.intVar("TRIAGE_RECONCILE_DAYS", 7),
		},
		RateLimitRPM: p.intVar("RATE_LIMIT_RPM", 120),
	}
	// A set-but-unparseable value must not silently become the default:
	// TRIAGE_DRY_RUN=yes falling back to false runs triage live.
	if err := errors.Join(p.errs...); err != nil {
		return nil, err
	}

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
	if c.Vertex.ProjectID == "" {
		missing = append(missing, "VERTEX_PROJECT_ID")
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
	if t := c.Triage.ConfidenceThreshold; t < 0 || t > 1 {
		return fmt.Errorf("TRIAGE_CONFIDENCE_THRESHOLD must be in [0, 1], got %v", t)
	}
	return nil
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

// envParser parses typed env vars, collecting an error for any variable that
// is set but unparseable instead of silently using the default.
type envParser struct {
	errs []error
}

func (p *envParser) boolVar(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("%s: %q is not a valid bool", key, v))
		return def
	}
	return b
}

func (p *envParser) intVar(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("%s: %q is not a valid integer", key, v))
		return def
	}
	return n
}

func (p *envParser) floatVar(key string, def float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		p.errs = append(p.errs, fmt.Errorf("%s: %q is not a valid number", key, v))
		return def
	}
	return f
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
