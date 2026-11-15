package tui

import "errors"

// Sentinels for the TUI's own failures, so err113 is satisfied and a caller can
// match one with errors.Is. Most are form-validation text huh shows inline.
var (
	errEmptyToken     = errors.New("gcloud returned an empty token")
	errNotJWT         = errors.New("not a JWT")
	errNoExpClaim     = errors.New("exp claim missing")
	errAPIStatus      = errors.New("api request failed")
	errNotWholeNumber = errors.New("must be a whole number")
	errNotNumber      = errors.New("must be a number")
	errJobFailed      = errors.New("failed")
)
