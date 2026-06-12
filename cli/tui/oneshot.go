package tui

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// RunAdd captures a task from a one-line description and prints what the
// server understood. Returns a process exit code.
func RunAdd(input string) int {
	return runAdd(os.Stdout, input)
}

// RunStatus prints upcoming blocks, open tasks, and the last planner run.
// Returns a process exit code.
func RunStatus() int {
	return runStatus(os.Stdout)
}

func runAdd(w io.Writer, input string) int {
	cfg, err := LoadConfig()
	if err != nil {
		_, _ = fmt.Fprintln(w, "config:", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	task, err := NewClient(cfg).QuickAdd(ctx, input)
	if err != nil {
		_, _ = fmt.Fprintln(w, "add failed:", err)
		return 1
	}
	_, _ = fmt.Fprintf(w, "added: %s\n", formatTask(task))
	return 0
}

func runStatus(w io.Writer) int {
	cfg, err := LoadConfig()
	if err != nil {
		_, _ = fmt.Fprintln(w, "config:", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	report, err := NewClient(cfg).Status(ctx)
	if err != nil {
		_, _ = fmt.Fprintln(w, "status failed:", err)
		return 1
	}
	_, _ = fmt.Fprint(w, formatStatus(report, time.Local))
	return 0
}

// formatTask renders the parse echo for `art add`.
func formatTask(t Task) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%q %s", t.Title, formatMinutes(t.DurationMinutes))
	if t.Deadline != nil {
		fmt.Fprintf(&b, " by %s", t.Deadline.Local().Format("Mon Jan 2"))
	}
	fmt.Fprintf(&b, " [%s]", t.Kind)
	return b.String()
}

// formatStatus renders the `art status` report.
func formatStatus(r StatusReport, tz *time.Location) string {
	var b strings.Builder

	b.WriteString("Upcoming blocks:\n")
	if len(r.Upcoming) == 0 {
		b.WriteString("  (none)\n")
	}
	for _, u := range r.Upcoming {
		fmt.Fprintf(&b, "  %s  %s-%s  %s: %s [%s]\n",
			u.Start.In(tz).Format("Mon Jan 02"),
			u.Start.In(tz).Format("15:04"),
			u.End.In(tz).Format("15:04"),
			u.Source, u.Title, u.AccountKind)
	}

	if len(r.TasksPending) > 0 {
		b.WriteString("\nPending tasks (not yet scheduled):\n")
		for _, t := range r.TasksPending {
			fmt.Fprintf(&b, "  %s\n", formatTask(t))
		}
	}
	if len(r.TasksUnschedulable) > 0 {
		b.WriteString("\nUnschedulable (didn't fit before deadline — edit or replan):\n")
		for _, t := range r.TasksUnschedulable {
			fmt.Fprintf(&b, "  %s\n", formatTask(t))
		}
	}

	if r.LastRun != nil {
		fmt.Fprintf(&b, "\nLast planner run: %s (%s, %s)\n",
			r.LastRun.StartedAt.In(tz).Format("Jan 02 15:04"),
			r.LastRun.Status, r.LastRun.Model)
		if r.LastRun.Error != "" {
			fmt.Fprintf(&b, "  error: %s\n", r.LastRun.Error)
		}
		if len(r.LastRun.Summary) > 0 && string(r.LastRun.Summary) != "{}" {
			fmt.Fprintf(&b, "  summary: %s\n", r.LastRun.Summary)
		}
	}
	return b.String()
}

func formatMinutes(mins int) string {
	switch {
	case mins < 60:
		return fmt.Sprintf("%dm", mins)
	case mins%60 == 0:
		return fmt.Sprintf("%dh", mins/60)
	default:
		return fmt.Sprintf("%dh%dm", mins/60, mins%60)
	}
}
