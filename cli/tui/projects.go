package tui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
)

// projectForm holds form-bound values on the heap so huh's *string bindings
// survive the value-receiver page being copied each Update.
type projectForm struct {
	name, kind, hours, deadline string
}

type projectItem struct{ p Project }

func (i projectItem) Title() string       { return i.p.Name }
func (i projectItem) FilterValue() string { return i.p.Name }
func (i projectItem) Description() string {
	due := "no deadline"
	if i.p.Deadline != nil {
		due = "due " + i.p.Deadline.Format("2006-01-02")
	}
	return fmt.Sprintf("[%s] %.0f/%.0fh · %s · %s", i.p.Kind, i.p.ScheduledHours, i.p.TargetHours, i.p.Status, due)
}

type projectsPage struct {
	client        *Client
	width, height int
	list          list.Model

	form   *huh.Form
	fd     *projectForm
	editID string
	keys   keyMap
}

func newProjectsPage(c *Client, isDark bool) projectsPage {
	d := list.NewDefaultDelegate()
	d.Styles = list.NewDefaultItemStyles(isDark)
	l := list.New(nil, d, 0, 0)
	l.Title = "Projects"
	l.SetShowHelp(false)
	return projectsPage{client: c, list: l, keys: defaultKeyMap()}
}

func (p projectsPage) Title() string   { return "projects" }
func (p projectsPage) FullInput() bool { return p.form != nil || p.list.SettingFilter() }
func (p projectsPage) bindings() []key.Binding {
	return []key.Binding{p.keys.Add, p.keys.Edit, p.keys.Delete}
}

func (p projectsPage) Init() tea.Cmd { return loadProjects(p.client) }

func (p projectsPage) Update(msg tea.Msg) (Page, tea.Cmd) {
	switch m := msg.(type) {
	case tea.WindowSizeMsg:
		p.width, p.height = m.Width, m.Height
		p.list.SetSize(m.Width, m.Height)
		if p.form != nil {
			p.form = p.form.WithWidth(m.Width).WithHeight(m.Height)
		}
		return p, nil
	case projectsMsg:
		return p, p.list.SetItems(projectItems(m.projects))
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

func (p projectsPage) handleKey(m tea.KeyPressMsg) (Page, tea.Cmd) {
	if p.list.SettingFilter() {
		var cmd tea.Cmd
		p.list, cmd = p.list.Update(m)
		return p, cmd
	}
	switch {
	case key.Matches(m, p.keys.Add):
		p.form, p.fd, p.editID = newProjectForm(nil, p.width, p.height)
		return p, p.form.Init()
	case key.Matches(m, p.keys.Edit):
		if it, ok := p.list.SelectedItem().(projectItem); ok {
			pr := it.p
			p.form, p.fd, p.editID = newProjectForm(&pr, p.width, p.height)
			return p, p.form.Init()
		}
	case key.Matches(m, p.keys.Delete):
		if it, ok := p.list.SelectedItem().(projectItem); ok {
			return p, tea.Sequence(deleteProject(p.client, it.p.ID), loadProjects(p.client))
		}
	}
	var cmd tea.Cmd
	p.list, cmd = p.list.Update(m)
	return p, cmd
}

func (p projectsPage) updateForm(msg tea.Msg) (Page, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok && k.String() == "esc" {
		p.form, p.fd, p.editID = nil, nil, ""
		return p, nil
	}
	form, cmd := p.form.Update(msg)
	if f, ok := form.(*huh.Form); ok {
		p.form = f
	}
	switch p.form.State {
	case huh.StateCompleted:
		submit := p.submitForm()
		p.form, p.fd, p.editID = nil, nil, ""
		return p, tea.Sequence(submit, loadProjects(p.client))
	case huh.StateAborted:
		p.form, p.fd, p.editID = nil, nil, ""
		return p, nil
	}
	return p, cmd
}

func (p projectsPage) submitForm() tea.Cmd {
	pr, err := p.fd.project()
	if err != nil {
		return func() tea.Msg { return errMsg{err} }
	}
	if p.editID != "" {
		return updateProject(p.client, p.editID, pr)
	}
	return createProject(p.client, pr)
}

// project builds the request payload, rejecting unparseable values.
func (fd *projectForm) project() (Project, error) {
	hours, err := strconv.ParseFloat(strings.TrimSpace(fd.hours), 64)
	if err != nil {
		return Project{}, fmt.Errorf("target hours %q is not a number", fd.hours)
	}
	pr := Project{
		Name:        strings.TrimSpace(fd.name),
		Kind:        fd.kind,
		TargetHours: hours,
	}
	if dl := strings.TrimSpace(fd.deadline); dl != "" {
		t, err := time.ParseInLocation("2006-01-02", dl, time.Local)
		if err != nil {
			return Project{}, fmt.Errorf("deadline %q is not YYYY-MM-DD", dl)
		}
		pr.Deadline = &t
	}
	return pr, nil
}

func (p projectsPage) View() string {
	if p.form != nil {
		return p.form.View()
	}
	if len(p.list.Items()) == 0 {
		title := p.list.Styles.Title.Render(p.list.Title)
		return title + "\n\n" + faintStyle.Render("No projects yet — press a to add.")
	}
	return p.list.View()
}

func projectItems(projects []Project) []list.Item {
	items := make([]list.Item, len(projects))
	for i, pr := range projects {
		items[i] = projectItem{pr}
	}
	return items
}

func newProjectForm(pr *Project, w, h int) (*huh.Form, *projectForm, string) {
	fd := &projectForm{kind: "work"}
	editID := ""
	if pr != nil {
		editID = pr.ID
		fd.name = pr.Name
		fd.kind = pr.Kind
		fd.hours = strconv.FormatFloat(pr.TargetHours, 'f', -1, 64)
		if pr.Deadline != nil {
			fd.deadline = pr.Deadline.Format("2006-01-02")
		}
	}
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().Title("Name").Value(&fd.name).Validate(huh.ValidateNotEmpty()),
		huh.NewSelect[string]().Title("Kind").Options(huh.NewOptions("work", "personal")...).Value(&fd.kind),
		huh.NewInput().Title("Target hours").Value(&fd.hours).Validate(validateFloat),
		huh.NewInput().Title("Deadline (YYYY-MM-DD, optional)").Value(&fd.deadline).Validate(validateOptionalDate),
	))
	if w > 0 {
		form = form.WithWidth(w).WithHeight(h)
	}
	return form, fd, editID
}
