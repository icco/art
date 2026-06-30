package tui

import "charm.land/lipgloss/v2"

// Shared theme. Fixed hex colors so the UI reads on light or dark terminals;
// bubbles components that need an explicit mode are given isDark at New time.
var (
	titleStyle   = lipgloss.NewStyle().Bold(true)
	headingStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("110"))
	subtleStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	faintStyle   = lipgloss.NewStyle().Faint(true)
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true)
	okStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#87ff87"))

	workStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#5fafff"))
	personalStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#87ff87"))
	artStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaf00")).Bold(true)

	activeTabStyle = lipgloss.NewStyle().Padding(0, 1).Reverse(true)
	tabStyle       = lipgloss.NewStyle().Padding(0, 1)

	tileHeadStyle = headingStyle
)

// kindStyle returns the color style for a work/personal/art-managed item.
func kindStyle(accountKind string, artManaged bool) lipgloss.Style {
	switch {
	case artManaged:
		return artStyle
	case accountKind == "work":
		return workStyle
	default:
		return personalStyle
	}
}
