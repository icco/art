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
const VertexModel = "gemini-3.1-pro"

// VertexConfig holds Vertex AI project and region settings for the LLM.
type VertexConfig struct {
	ProjectID string
	Location  string
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
		},
		Triage: TriageConfig{
			Enabled:             envBool("TRIAGE_ENABLED", true),
			DryRun:              envBool("TRIAGE_DRY_RUN", false),
			BackfillDays:        envInt("TRIAGE_BACKFILL_DAYS", 14),
			MaxPerRun:           envInt("TRIAGE_MAX_PER_RUN", 50),
			ConfidenceThreshold: envFloat("TRIAGE_CONFIDENCE_THRESHOLD", 0.8),
			ReconcileDays:       envInt("TRIAGE_RECONCILE_DAYS", 14),
		},
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

func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
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
