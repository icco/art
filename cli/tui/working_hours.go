package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
)

// dayNames indexes weekdays the way the API does: 0 = Sunday.
var dayNames = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

// slotKinds is the column order of the grid.
var slotKinds = []string{"work", "personal"}

// hoursForm edits the selected cell. windows is a comma-separated list of
// HH:MM-HH:MM ranges, so a day with several windows stays one field.
type hoursForm struct {
	kind    string
	day     int
	windows string
}

// workingHoursPage shows the seven days per slot kind and edits one cell at a
// time; a cursor picks the cell so the form can prefill what it will replace.
type workingHoursPage struct {
	client        *Client
	width, height int
	hours         []WorkingHour
	day, kind     int

	form *huh.Form
	fd   *hoursForm
	keys keyMap
}

func newWorkingHoursPage(c *Client) workingHoursPage {
	return workingHoursPage{client: c, keys: defaultKeyMap()}
}

func (p workingHoursPage) Title() string   { return "hours" }
func (p workingHoursPage) FullInput() bool { return p.form != nil }
func (p workingHoursPage) bindings() []key.Binding {
	return []key.Binding{p.keys.Up, p.keys.Down, p.keys.PrevKind, p.keys.NextKind, p.keys.Edit}
}

func (p workingHoursPage) Init() tea.Cmd { return loadWorkingHours(p.client) }

func (p workingHoursPage) Update(msg tea.Msg) (Page, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		p.width, p.height = m.Width, m.Height
		if p.form != nil {
			p.form = p.form.WithWidth(m.Width).WithHeight(m.Height)
		}
		return p, nil
	case workingHoursMsg:
		p.hours = m.hours
		return p, nil
	case tea.KeyPressMsg:
		if p.form != nil {
			return p.updateForm(m)
		}
		return p.handleKey(m)
	}
	if p.form != nil {
		return p.updateForm(msg)
	}
	return p, nil
}

func (p workingHoursPage) handleKey(m tea.KeyPressMsg) (Page, tea.Cmd) {
	switch {
	case key.Matches(m, p.keys.Up):
		p.day = (p.day + len(dayNames) - 1) % len(dayNames)
	case key.Matches(m, p.keys.Down):
		p.day = (p.day + 1) % len(dayNames)
	case key.Matches(m, p.keys.PrevKind), key.Matches(m, p.keys.NextKind):
		p.kind = (p.kind + 1) % len(slotKinds)
	case key.Matches(m, p.keys.Edit):
		p.form, p.fd = newHoursForm(p.hours, slotKinds[p.kind], p.day, p.width, p.height)
		return p, p.form.Init()
	}
	return p, nil
}

func (p workingHoursPage) updateForm(msg tea.Msg) (Page, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok && k.String() == "esc" {
		p.form, p.fd = nil, nil
		return p, nil
	}
	form, cmd := p.form.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		p.form = f
	}
	switch p.form.State {
	case huh.StateCompleted:
		submit := p.submitForm()
		p.form, p.fd = nil, nil
		return p, tea.Sequence(submit, loadWorkingHours(p.client))
	case huh.StateAborted:
		p.form, p.fd = nil, nil
		return p, nil
	}
	return p, cmd
}

func (p workingHoursPage) submitForm() tea.Cmd {
	windows, err := parseWindows(p.fd.windows)
	if err != nil {
		return func() tea.Msg { return errMsg{err} }
	}
	return saveWorkingHoursDay(p.client, p.fd.kind, p.fd.day, windows)
}

func (p workingHoursPage) View() string {
	if p.form != nil {
		return p.form.View()
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("Working hours") + "\n\n")
	fmt.Fprintf(&b, "  %-5s  %-30s %s\n", "", headingStyle.Render("work"), headingStyle.Render("personal"))
	for day, name := range dayNames {
		cursor := "  "
		if day == p.day {
			cursor = "› "
		}
		cells := make([]string, len(slotKinds))
		for i, kind := range slotKinds {
			text := dayWindows(p.hours, kind, day)
			style := kindStyle(kind, false)
			if day == p.day && i == p.kind {
				style = style.Reverse(true)
			}
			cells[i] = style.Render(text)
		}
		fmt.Fprintf(&b, "%s%-5s  %-30s %s\n", cursor, name, cells[0], cells[1])
	}
	b.WriteString("\n" + faintStyle.Render("↑/↓ day · ←/→ kind · e edit the selected day"))
	return b.String()
}

// dayWindows renders one cell: every window for a kind and day, or an em dash.
func dayWindows(hours []WorkingHour, kind string, day int) string {
	var out []WorkingHour
	for _, h := range hours {
		if h.SlotKind == kind && h.DayOfWeek == day {
			out = append(out, h)
		}
	}
	if len(out) == 0 {
		return "—"
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartMinute < out[j].StartMinute })
	spans := make([]string, len(out))
	for i, h := range out {
		spans[i] = formatMinutes(h.StartMinute) + "-" + formatMinutes(h.EndMinute)
	}
	return strings.Join(spans, ", ")
}

// formatMinutes renders minutes past midnight as HH:MM; 1440 reads 24:00.
func formatMinutes(m int) string {
	return fmt.Sprintf("%02d:%02d", m/60, m%60)
}

// parseMinutes accepts HH:MM, including 24:00 for end-of-day.
func parseMinutes(s string) (int, error) {
	hPart, mPart, ok := strings.Cut(strings.TrimSpace(s), ":")
	if !ok {
		return 0, fmt.Errorf("%q must be HH:MM", s)
	}
	hh, err := strconv.Atoi(strings.TrimSpace(hPart))
	if err != nil {
		return 0, fmt.Errorf("%q must be HH:MM", s)
	}
	mm, err := strconv.Atoi(strings.TrimSpace(mPart))
	if err != nil {
		return 0, fmt.Errorf("%q must be HH:MM", s)
	}
	if hh < 0 || hh > 24 || mm < 0 || mm > 59 || hh*60+mm > 1440 {
		return 0, fmt.Errorf("%q is not a valid time", s)
	}
	return hh*60 + mm, nil
}

// parseWindows parses "09:00-12:00, 13:00-17:30"; empty clears the day.
func parseWindows(s string) ([]DayWindow, error) {
	out := []DayWindow{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		from, to, ok := strings.Cut(part, "-")
		if !ok {
			return nil, fmt.Errorf("%q must be HH:MM-HH:MM", part)
		}
		start, err := parseMinutes(from)
		if err != nil {
			return nil, err
		}
		end, err := parseMinutes(to)
		if err != nil {
			return nil, err
		}
		if end <= start {
			return nil, fmt.Errorf("%q must end after it starts", part)
		}
		out = append(out, DayWindow{StartMinute: start, EndMinute: end})
	}
	return out, nil
}

func validateWindows(s string) error {
	_, err := parseWindows(s)
	return err
}

// newHoursForm prefills the selected cell's current windows, so submitting
// unchanged is a no-op rather than a clear.
func newHoursForm(hours []WorkingHour, kind string, day, w, ht int) (*huh.Form, *hoursForm) {
	fd := &hoursForm{kind: kind, day: day}
	if cell := dayWindows(hours, kind, day); cell != "—" {
		fd.windows = cell
	}
	form := huh.NewForm(huh.NewGroup(
		huh.NewNote().Title(fmt.Sprintf("%s · %s", kind, dayNames[day])),
		huh.NewInput().Title("Windows (HH:MM-HH:MM, comma separated; empty clears the day)").
			Value(&fd.windows).Validate(validateWindows),
	))
	if w > 0 {
		form = form.WithWidth(w).WithHeight(ht)
	}
	return form, fd
}
