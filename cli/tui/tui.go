// Package tui implements the art terminal UI client.
package tui

import (
	tea "charm.land/bubbletea/v2"
)

// Run builds the root model and runs the Bubble Tea program until the user
// quits.
func Run(cfg Config) error {
	p := tea.NewProgram(newRoot(cfg))
	_, err := p.Run()
	return err
}
