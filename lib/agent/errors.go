package agent

import "errors"

// Scheduling invariants commit_focus_block enforces. They are sentinels so a
// caller can match one with errors.Is; the text reaches the tool result.
var (
	errBadSourceKind     = errors.New("source must be 'project' or 'habit'")
	errUnknownSourceKind = errors.New("unknown source kind")
	errBlockDuration     = errors.New("block duration out of range")
	errBeforePlanStart   = errors.New("is before planning start")
	errPastPlanWindow    = errors.New("is past the plan window end")
	errOutsideHours      = errors.New("is outside working hours")
	errOverlapsBusy      = errors.New("overlaps an existing event or planned session")
	errHabitDailyLimit   = errors.New("habit already has a block that day; at most one per day")
)
