package tui

import (
	"fmt"
	"sort"
	"strings"
)

// dayNames indexes time.Weekday (0 = Sunday) for form labels.
var dayNames = [7]string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

// hoursFieldOrder is the on-screen row order: Monday first, both kinds per day.
var hoursFieldOrder = [7]int{1, 2, 3, 4, 5, 6, 0}

// hoursFields renders working hours as 14 editable cells (7 days × 2 kinds),
// each holding comma-separated HH:MM-HH:MM windows.
func hoursFields(hours []WorkingHour) []formField {
	cells := map[string][]WorkingHour{}
	for _, h := range hours {
		key := fmt.Sprintf("%d/%s", h.DayOfWeek, h.SlotKind)
		cells[key] = append(cells[key], h)
	}
	var fields []formField
	for _, day := range hoursFieldOrder {
		for _, kind := range []string{"work", "personal"} {
			ws := cells[fmt.Sprintf("%d/%s", day, kind)]
			sort.Slice(ws, func(i, j int) bool { return ws[i].StartMinute < ws[j].StartMinute })
			var parts []string
			for _, w := range ws {
				parts = append(parts, fmt.Sprintf("%s-%s", formatMinuteOfDay(w.StartMinute), formatMinuteOfDay(w.EndMinute)))
			}
			fields = append(fields, formField{
				label: fmt.Sprintf("%s %s", dayNames[day], kind),
				value: strings.Join(parts, ", "),
			})
		}
	}
	return fields
}

// parseHoursForm converts the 14 cells back into windows, ordered by kind
// then day then start (matching the API's list order).
func parseHoursForm(fields []formField) ([]WorkingHour, error) {
	var out []WorkingHour
	i := 0
	for _, day := range hoursFieldOrder {
		for _, kind := range []string{"work", "personal"} {
			if i >= len(fields) {
				return nil, fmt.Errorf("hours form incomplete")
			}
			windows, err := parseHoursCell(fields[i].value)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", dayNames[day], kind, err)
			}
			for _, w := range windows {
				w.SlotKind = kind
				w.DayOfWeek = day
				out = append(out, w)
			}
			i++
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SlotKind != out[j].SlotKind {
			return out[i].SlotKind < out[j].SlotKind
		}
		if out[i].DayOfWeek != out[j].DayOfWeek {
			return out[i].DayOfWeek < out[j].DayOfWeek
		}
		return out[i].StartMinute < out[j].StartMinute
	})
	return out, nil
}

// parseHoursCell parses comma-separated "HH:MM-HH:MM" windows. A blank cell
// means no hours that day.
func parseHoursCell(cell string) ([]WorkingHour, error) {
	cell = strings.TrimSpace(cell)
	if cell == "" {
		return nil, nil
	}
	var out []WorkingHour
	for part := range strings.SplitSeq(cell, ",") {
		part = strings.TrimSpace(part)
		from, to, ok := strings.Cut(part, "-")
		if !ok {
			return nil, fmt.Errorf("%q is not HH:MM-HH:MM", part)
		}
		start, err := parseMinuteOfDay(from)
		if err != nil {
			return nil, err
		}
		end, err := parseMinuteOfDay(to)
		if err != nil {
			return nil, err
		}
		if end <= start {
			return nil, fmt.Errorf("%q ends before it starts", part)
		}
		out = append(out, WorkingHour{StartMinute: start, EndMinute: end})
	}
	return out, nil
}

func parseMinuteOfDay(s string) (int, error) {
	s = strings.TrimSpace(s)
	var h, m int
	if _, err := fmt.Sscanf(s, "%d:%d", &h, &m); err != nil {
		return 0, fmt.Errorf("%q is not HH:MM", s)
	}
	if h < 0 || h > 24 || m < 0 || m > 59 || h*60+m > 1440 {
		return 0, fmt.Errorf("%q is out of range", s)
	}
	return h*60 + m, nil
}

func formatMinuteOfDay(min int) string {
	return fmt.Sprintf("%d:%02d", min/60, min%60)
}
