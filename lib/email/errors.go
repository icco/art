package email

import "errors"

// Triage failures: bad model output, and a pass that classified nothing.
var (
	errBadCategory    = errors.New("model returned an invalid category")
	errBadConfidence  = errors.New("model returned a confidence outside [0, 1]")
	errClassifiedNone = errors.New("triage classified none of the messages it attempted")
)
