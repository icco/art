package agent

import "time"

// WeekWindow returns [start, end) of the calendar week containing t (tz-aware).
// Start is Monday 00:00; end is the following Monday 00:00. AddDate keeps the
// end on midnight across DST weeks (167/169 wall hours).
func WeekWindow(t time.Time, tz *time.Location) (time.Time, time.Time) {
	start := startOfWeek(t, tz)
	return start, start.AddDate(0, 0, 7)
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
