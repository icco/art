package tui

import (
	"fmt"
	"os"
)

// Config holds runtime configuration for the TUI client.
type Config struct {
	APIURL   string
	Audience string
}

// LoadConfig builds a Config from environment variables, falling back to
// sensible defaults when ART_API_URL or ART_API_AUDIENCE are not set.
func LoadConfig() (Config, error) {
	c := Config{
		APIURL:   envOr("ART_API_URL", "http://localhost:8080"),
		Audience: os.Getenv("ART_API_AUDIENCE"),
	}
	if c.Audience == "" {
		c.Audience = c.APIURL
	}
	if c.APIURL == "" {
		return c, fmt.Errorf("ART_API_URL is empty")
	}
	return c, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
