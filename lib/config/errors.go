package config

import "errors"

// Startup configuration failures, wrapped so a caller can tell a missing
// variable from an unparseable one with errors.Is.
var (
	errMissingEnv = errors.New("missing required env vars")
	errInvalidEnv = errors.New("invalid env var")
)
