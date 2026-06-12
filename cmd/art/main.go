// Command art runs the interactive terminal UI for the art API, plus
// one-shot subcommands for quick capture and status.
package main

import (
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/icco/art/cli/tui"
)

const usage = `usage:
  art                      interactive TUI
  art add <description>    capture a task, e.g. art add "pack office 2h by friday"
  art status               upcoming blocks, open tasks, last planner run
`

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "add":
			if len(os.Args) < 3 {
				fmt.Fprint(os.Stderr, usage)
				os.Exit(2)
			}
			os.Exit(tui.RunAdd(strings.Join(os.Args[2:], " ")))
		case "status":
			os.Exit(tui.RunStatus())
		case "help", "-h", "--help":
			fmt.Print(usage)
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown command %q\n%s", os.Args[1], usage)
			os.Exit(2)
		}
	}

	cfg, err := tui.LoadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	app := tui.NewApp(cfg)
	p := tea.NewProgram(app, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
