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

	activeTabStyle = lipgloss.NewStyle().Padding(0, 1).Reverse(true)
	tabStyle       = lipgloss.NewStyle().Padding(0, 1)

	tileHeadStyle = headingStyle
)
