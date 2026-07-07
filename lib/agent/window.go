package agent

import "time"

// PlanHorizon is how far ahead the planner schedules: a rolling 30 days from
// the next whole hour.
const PlanHorizon = 30 * 24 * time.Hour

// WeekWindow returns [start, end) of the calendar week containing t (tz-aware).
// Start is Monday 00:00; end is the following Monday 00:00. It backs
// PlanningStart's floor; the planner itself schedules over PlanWindow.
func WeekWindow(t time.Time, tz *time.Location) (time.Time, time.Time) {
	start := startOfWeek(t, tz)
	return start, start.AddDate(0, 0, 7)
}

// PlanWindow returns [start, end) the planner may schedule into: from the next
// whole hour (PlanningStart) through PlanHorizon later.
func PlanWindow(now time.Time, tz *time.Location) (time.Time, time.Time) {
	start := PlanningStart(now, tz)
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
