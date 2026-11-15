package tui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
)

// settingsForm holds the knobs as strings; huh validates before submit and
// settings() reparses, so a bad number never reaches the API.
type settingsForm struct {
	triageEnabled bool
	triageDryRun  bool
	threshold     string
	backfillDays  string
	reconcileDays string
	dailyBudget   string
}

// settingsPage edits the runtime settings. Deploy-time config (timezone, owner
// emails, keys) is env-only and deliberately absent.
type settingsPage struct {
	client        *Client
	width, height int
	settings      Settings
	loaded        bool

	form *huh.Form
	fd   *settingsForm
	keys keyMap
}

func newSettingsPage(c *Client) settingsPage {
	return settingsPage{client: c, keys: defaultKeyMap()}
}

func (p settingsPage) Title() string   { return "settings" }
func (p settingsPage) FullInput() bool { return p.form != nil }
func (p settingsPage) bindings() []key.Binding {
	return []key.Binding{p.keys.Edit}
}

func (p settingsPage) Init() tea.Cmd { return loadSettings(p.client) }

func (p settingsPage) Update(msg tea.Msg) (Page, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		p.width, p.height = m.Width, m.Height
		if p.form != nil {
			p.form = p.form.WithWidth(m.Width).WithHeight(m.Height)
		}
		return p, nil
	case settingsMsg:
		p.settings, p.loaded = m.settings, true
		return p, nil
	case tea.KeyPressMsg:
		if p.form != nil {
			return p.updateForm(m)
		}
		if key.Matches(m, p.keys.Edit) && p.loaded {
			p.form, p.fd = newSettingsForm(p.settings, p.width, p.height)
			return p, p.form.Init()
		}
		return p, nil
	}
	if p.form != nil {
		return p.updateForm(msg)
	}
	return p, nil
}

func (p settingsPage) updateForm(msg tea.Msg) (Page, tea.Cmd) {
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
		return p, tea.Sequence(submit, loadSettings(p.client))
	case huh.StateAborted:
		p.form, p.fd = nil, nil
		return p, nil
	}
	return p, cmd
}

func (p settingsPage) submitForm() tea.Cmd {
	s, err := p.fd.settings()
	if err != nil {
		return func() tea.Msg { return errMsg{err} }
	}
	return saveSettings(p.client, s)
}

// settings builds the request payload, rejecting unparseable numbers.
func (fd *settingsForm) settings() (Settings, error) {
	threshold, err := strconv.ParseFloat(strings.TrimSpace(fd.threshold), 64)
	if err != nil {
		return Settings{}, fmt.Errorf("confidence threshold %q: %w", fd.threshold, errNotNumber)
	}
	// The form must round-trip this: submitting without it would send 0 and
	// silently turn the daily spend cap off.
	budget, err := strconv.ParseFloat(strings.TrimSpace(fd.dailyBudget), 64)
	if err != nil {
		return Settings{}, fmt.Errorf("daily budget %q: %w", fd.dailyBudget, errNotNumber)
	}
	nums := map[string]string{
		"backfill days":  fd.backfillDays,
		"reconcile days": fd.reconcileDays,
	}
	parsed := map[string]int{}
	for label, raw := range nums {
		n, convErr := strconv.Atoi(strings.TrimSpace(raw))
		if convErr != nil {
			return Settings{}, fmt.Errorf("%s %q: %w", label, raw, errNotWholeNumber)
		}
		parsed[label] = n
	}
	return Settings{
		TriageEnabled:             fd.triageEnabled,
		TriageDryRun:              fd.triageDryRun,
		TriageConfidenceThreshold: threshold,
		TriageBackfillDays:        parsed["backfill days"],
		TriageReconcileDays:       parsed["reconcile days"],
		DailyBudgetUSD:            budget,
	}, nil
}

func (p settingsPage) View() string {
	if p.form != nil {
		return p.form.View()
	}
	if !p.loaded {
		return titleStyle.Render("Settings") + "\n\n" + faintStyle.Render("loading…")
	}
	s := p.settings
	rows := [][2]string{
		{"Triage enabled", onOff(s.TriageEnabled)},
		{"Triage dry run", onOff(s.TriageDryRun)},
		{"Confidence threshold", strconv.FormatFloat(s.TriageConfidenceThreshold, 'f', -1, 64)},
		{"Triage backfill days", strconv.Itoa(s.TriageBackfillDays)},
		{"Triage reconcile days", strconv.Itoa(s.TriageReconcileDays)},
		{"Daily LLM budget", budgetLabel(s.DailyBudgetUSD)},
	}
	var b strings.Builder
	b.WriteString(titleStyle.Render("Settings") + "\n\n")
	for _, row := range rows {
		fmt.Fprintf(&b, "%-24s %s\n", headingStyle.Render(row[0]), row[1])
	}
	b.WriteString("\n" + faintStyle.Render("press e to edit · timezone stays an env var"))
	return b.String()
}

func onOff(b bool) string {
	if b {
		return okStyle.Render("yes")
	}
	return subtleStyle.Render("no")
}

func newSettingsForm(s Settings, w, ht int) (*huh.Form, *settingsForm) {
	fd := &settingsForm{
		triageEnabled: s.TriageEnabled,
		triageDryRun:  s.TriageDryRun,
		threshold:     strconv.FormatFloat(s.TriageConfidenceThreshold, 'f', -1, 64),
		backfillDays:  strconv.Itoa(s.TriageBackfillDays),
		reconcileDays: strconv.Itoa(s.TriageReconcileDays),
		dailyBudget:   strconv.FormatFloat(s.DailyBudgetUSD, 'f', -1, 64),
	}
	form := huh.NewForm(huh.NewGroup(
		huh.NewConfirm().Title("Triage enabled").Affirmative("on").Negative("off").Value(&fd.triageEnabled),
		huh.NewConfirm().Title("Triage dry run").Affirmative("dry").Negative("live").Value(&fd.triageDryRun),
		huh.NewInput().Title("Confidence threshold (0-1)").Value(&fd.threshold).Validate(validateFloat),
		huh.NewInput().Title("Triage backfill days").Value(&fd.backfillDays).Validate(validateInt),
		huh.NewInput().Title("Triage reconcile days").Value(&fd.reconcileDays).Validate(validateInt),
		huh.NewInput().Title("Daily LLM budget in USD (0 = unlimited)").Value(&fd.dailyBudget).Validate(validateFloat),
	))
	if w > 0 {
		form = form.WithWidth(w).WithHeight(ht)
	}
	return form, fd
}

// budgetLabel renders the daily Vertex AI ceiling; zero means the cap is off.
func budgetLabel(usd float64) string {
	if usd <= 0 {
		return "unlimited"
	}
	return "$" + strconv.FormatFloat(usd, 'f', 2, 64) + "/day"
}
