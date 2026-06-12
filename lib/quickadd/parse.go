// Package quickadd parses one-line task capture like "pack office 2h by friday".
package quickadd

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/icco/art/lib/models"
)

// MaxDurationMinutes caps a single task; anything longer should be a Project
// with a weekly target instead.
const MaxDurationMinutes = 8 * 60

// Parsed is the structured result of parsing a quick-add line.
type Parsed struct {
	Title           string
	DurationMinutes int
	Deadline        *time.Time
	Kind            models.SlotKind
}

var (
	hoursRe   = regexp.MustCompile(`^(\d+(?:\.\d+)?)h$`)
	minutesRe = regexp.MustCompile(`^(\d+)m$`)
	hourMinRe = regexp.MustCompile(`^(\d+)h(\d+)m$`)
	slashRe   = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})$`)
	isoRe     = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`)
)

var weekdays = map[string]time.Weekday{
	"sun": time.Sunday, "sunday": time.Sunday,
	"mon": time.Monday, "monday": time.Monday,
	"tue": time.Tuesday, "tues": time.Tuesday, "tuesday": time.Tuesday,
	"wed": time.Wednesday, "weds": time.Wednesday, "wednesday": time.Wednesday,
	"thu": time.Thursday, "thur": time.Thursday, "thurs": time.Thursday, "thursday": time.Thursday,
	"fri": time.Friday, "friday": time.Friday,
	"sat": time.Saturday, "saturday": time.Saturday,
}

// Parse extracts duration, deadline, and kind tokens from input; whatever
// remains, in order, is the title. Deadlines resolve to end-of-day in tz.
func Parse(input string, now time.Time, tz *time.Location) (Parsed, error) {
	p := Parsed{DurationMinutes: 0, Kind: models.SlotPersonal}
	tokens := strings.Fields(input)
	var title []string
	haveDuration := false

	for i := 0; i < len(tokens); i++ {
		tok := tokens[i]
		lower := strings.ToLower(tok)

		if lower == "#work" || lower == "#personal" {
			p.Kind = models.SlotKind(strings.TrimPrefix(lower, "#"))
			continue
		}

		if mins, ok, err := parseDuration(lower); err != nil {
			return Parsed{}, err
		} else if ok {
			if haveDuration {
				return Parsed{}, errors.New("more than one duration given")
			}
			haveDuration = true
			p.DurationMinutes = mins
			continue
		}

		if (lower == "by" || lower == "due") && i+1 < len(tokens) {
			deadline, ok, err := parseDeadline(strings.ToLower(tokens[i+1]), now, tz)
			if err != nil {
				return Parsed{}, err
			}
			if ok {
				p.Deadline = &deadline
				i++
				continue
			}
			// Not a date token ("stop by store", "pack by whenever"): keep
			// both words in the title. The caller echoes the parse back, so a
			// missed deadline is visible rather than fatal.
		}

		title = append(title, tok)
	}

	if !haveDuration {
		p.DurationMinutes = 60
	}
	if p.DurationMinutes <= 0 {
		return Parsed{}, errors.New("duration must be positive")
	}
	if p.DurationMinutes > MaxDurationMinutes {
		return Parsed{}, fmt.Errorf("%dm is too long for a task; create a project instead", p.DurationMinutes)
	}
	if p.Deadline != nil && p.Deadline.Before(now) {
		return Parsed{}, fmt.Errorf("deadline %s is in the past", p.Deadline.In(tz).Format("2006-01-02"))
	}
	p.Title = strings.Join(title, " ")
	if p.Title == "" {
		return Parsed{}, errors.New("task needs a title")
	}
	return p, nil
}

// parseDuration recognises "2h", "90m", "1.5h", "1h30m". The bool reports
// whether tok looked like a duration at all.
func parseDuration(tok string) (int, bool, error) {
	if m := hourMinRe.FindStringSubmatch(tok); m != nil {
		h, _ := strconv.Atoi(m[1])
		mins, _ := strconv.Atoi(m[2])
		return h*60 + mins, true, nil
	}
	if m := hoursRe.FindStringSubmatch(tok); m != nil {
		h, err := strconv.ParseFloat(m[1], 64)
		if err != nil {
			return 0, false, fmt.Errorf("bad duration %q", tok)
		}
		return int(h * 60), true, nil
	}
	if m := minutesRe.FindStringSubmatch(tok); m != nil {
		mins, _ := strconv.Atoi(m[1])
		return mins, true, nil
	}
	return 0, false, nil
}

// parseDeadline recognises weekday names, today/tomorrow/eow, M/D, and
// YYYY-MM-DD. The bool reports whether tok looked like a date token; an error
// means it did but was invalid (e.g. month 13).
func parseDeadline(tok string, now time.Time, tz *time.Location) (time.Time, bool, error) {
	local := now.In(tz)
	endOfDay := func(t time.Time) time.Time {
		return time.Date(t.Year(), t.Month(), t.Day(), 23, 59, 59, 0, tz)
	}

	switch tok {
	case "today":
		return endOfDay(local), true, nil
	case "tomorrow":
		return endOfDay(local.AddDate(0, 0, 1)), true, nil
	case "eow":
		days := (int(time.Sunday) - int(local.Weekday()) + 7) % 7
		return endOfDay(local.AddDate(0, 0, days)), true, nil
	}

	if wd, ok := weekdays[tok]; ok {
		days := (int(wd) - int(local.Weekday()) + 7) % 7
		return endOfDay(local.AddDate(0, 0, days)), true, nil
	}

	if m := isoRe.FindStringSubmatch(tok); m != nil {
		t, err := time.ParseInLocation("2006-01-02", tok, tz)
		if err != nil {
			return time.Time{}, true, fmt.Errorf("bad date %q", tok)
		}
		return endOfDay(t), true, nil
	}

	if m := slashRe.FindStringSubmatch(tok); m != nil {
		month, _ := strconv.Atoi(m[1])
		day, _ := strconv.Atoi(m[2])
		if month < 1 || month > 12 || day < 1 || day > 31 {
			return time.Time{}, true, fmt.Errorf("bad date %q", tok)
		}
		t := time.Date(local.Year(), time.Month(month), day, 23, 59, 59, 0, tz)
		if t.Before(local) {
			t = t.AddDate(1, 0, 0)
		}
		return t, true, nil
	}

	return time.Time{}, false, nil
}
