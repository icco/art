package tui

import (
	"os"
	"strings"
)

// Config holds runtime configuration for the TUI client.
type Config struct {
	APIURL string
}

// LoadConfig builds a Config from environment variables. A trailing slash in
// ART_API_URL would 404 every request, so it's trimmed.
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
