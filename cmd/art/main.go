// Command art runs the interactive terminal UI for the art API.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/icco/art/cli/tui"
)

// Version is set by goreleaser via -ldflags at build time.
var Version = "dev"

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version", "-v":
			fmt.Println(Version)
			return
		case "--help", "-h":
			usage(os.Stdout)
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown argument %q\n\n", os.Args[1])
			usage(os.Stderr)
			os.Exit(2)
		}
	}
	cfg, err := tui.LoadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	if err := tui.Run(cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// usage prints CLI help: what art is, how it's configured, and its keybindings.
func usage(w io.Writer) {
	_, _ = fmt.Fprint(w, `art — terminal UI for the art personal scheduling agent.

Usage:
  art            launch the TUI
  art --help     show this help
  art --version  print the version

Configuration (environment):
  ART_API_URL       art server base URL (default http://localhost:8080)
  ART_API_AUDIENCE  OIDC audience for the ID token (default: ART_API_URL)

Authentication:
  Requests are authenticated as you via a Google ID token minted with
  `+"`gcloud auth print-identity-token`"+` — install and log in with gcloud first.

Keys:
  1/2/3/4/5  dashboard / calendar / projects / habits / digest
  ←/→ h/l    previous / next week (calendar)    a/e/d  add / edit / delete
  r          replan      s  sync      t  triage   ?  toggle help
  esc        back to dashboard                    q  quit
`)
}
