package tui

import (
	"os"
	"strings"
)

// Config holds runtime configuration for the TUI client.
type Config struct {
	APIURL string
}

// LoadConfig builds a Config from environment variables, falling back to a
// sensible default when ART_API_URL is not set. A trailing slash would break
// every request path ("//projects" 404s in chi), so it's trimmed.
func LoadConfig() (Config, error) {
	return Config{
		APIURL: strings.TrimRight(envOr("ART_API_URL", "http://localhost:8080"), "/"),
	}, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
