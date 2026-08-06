package queue

import "errors"

// Job-dispatch failures.
var (
	errPanic          = errors.New("panic")
	errUnknownJobKind = errors.New("unknown job kind")
)
