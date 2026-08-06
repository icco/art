package handlers

import "errors"

// Request-validation failures. They are sentinels rather than errors.New at the
// return site so a caller can match one with errors.Is; the text is what the API
// hands back as the 400 body.
var (
	errNameRequired    = errors.New("name required")
	errNameEmpty       = errors.New("name cannot be empty")
	errKindInvalid     = errors.New("kind must be 'work' or 'personal'")
	errBlockMinutes    = errors.New("block_duration_minutes must be > 0")
	errCadenceRequired = errors.New("cadence with type and positive count required")
	errCadenceInvalid  = errors.New("cadence type must be per_week or per_day with a positive count")
	errTargetHours     = errors.New("target_hours must be > 0")
	errStatusInvalid   = errors.New("status must be one of active|paused|done")
	errDeadlineInvalid = errors.New("deadline must be an RFC3339 timestamp or null")
	errSlotKindInvalid = errors.New("slot_kind must be 'work' or 'personal'")
	errDayOfWeek       = errors.New("day_of_week must be 0-6")
	errStartMinute     = errors.New("start_minute must be 0-1439")
	errEndMinute       = errors.New("end_minute must be > start_minute and <= 1440")
	errOverlapping     = errors.New("overlapping windows")
)
