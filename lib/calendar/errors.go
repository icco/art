package calendar

import "errors"

// Failures writing to or mirroring a Google Calendar.
var (
	errBadSourceKind  = errors.New("calendar: invalid source kind")
	errEndBeforeStart = errors.New("calendar: end must be after start")
	errNotArtManaged  = errors.New("calendar: refusing to delete non-Art event")
	errNoPrimaryCal   = errors.New("calendar: account has no primary calendar")
)
