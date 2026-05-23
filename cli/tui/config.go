package tui

import (
	"fmt"
	"os"
)

type Config struct {
	APIURL   string
	Audience string
}

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
