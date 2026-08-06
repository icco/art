package tui

import "errors"

// Sentinels for the TUI's own failures, so err113 is satisfied and a caller can
// match one with errors.Is. Most are form-validation text huh shows inline.
var (
	errEmptyToken      = errors.New("gcloud returned an empty token")
	errNotJWT          = errors.New("not a JWT")
	errNoExpClaim      = errors.New("exp claim missing")
	errAPIStatus       = errors.New("api request failed")
	errNotWholeNumber  = errors.New("must be a whole number")
	errNotNumber       = errors.New("must be a number")
	errNotADate        = errors.New("must be YYYY-MM-DD")
	errNotHHMM         = errors.New("must be HH:MM")
	errNotATime        = errors.New("is not a valid time")
	errNotHHMMRange    = errors.New("must be HH:MM-HH:MM")
	errEndsBeforeStart = errors.New("must end after it starts")
	errJobFailed       = errors.New("failed")
)
