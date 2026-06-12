package agent

import "time"

// WeekWindow returns [start, end) of the calendar week containing t (tz-aware).
// Start is Monday 00:00; end is the following Monday 00:00.
func WeekWindow(t time.Time, tz *time.Location) (time.Time, time.Time) {
	start := startOfWeek(t, tz)
	return start, start.Add(7 * 24 * time.Hour)
}

// PlanHorizon is how far ahead the planner books blocks.
const PlanHorizon = 14 * 24 * time.Hour

// PlanWindow returns the rolling planning window [NextHour(now), +14 days).
// Unlike WeekWindow it crosses calendar-week boundaries, so a Friday request
// can land on Monday.
func PlanWindow(now time.Time, _ *time.Location) (time.Time, time.Time) {
	start := NextHour(now)
	return start, start.Add(PlanHorizon)
}

// NextHour rounds t up to the next whole hour. The planner never schedules
// focus blocks that start in the in-progress hour.
func NextHour(t time.Time) time.Time {
	return t.UTC().Truncate(time.Hour).Add(time.Hour)
}

// PlanningStart returns the earliest moment the planner may schedule:
// max(weekStart, nextHour(now)).
func PlanningStart(now time.Time, tz *time.Location) time.Time {
	weekStart, _ := WeekWindow(now, tz)
	return maxTime(weekStart, NextHour(now))
}
