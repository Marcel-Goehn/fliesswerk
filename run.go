package main

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/Marcel-Goehn/kraftwerk/internal/frontier"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker"
)

// runRun starts a Run. With --dry-run it validates the config, reads the
// Snapshot and prints Iteration 1 and every skipped Ticket, writing
// nothing and starting nothing.
func runRun(args []string, stdout, stderr io.Writer) int {
	var dryRun bool
	values, root, code, ok := loadConfig("kraftwerk run", args, stderr, func(fs *flag.FlagSet) {
		fs.BoolVar(&dryRun, "dry-run", false, "show Iteration 1 and every skipped Ticket, writing nothing")
	})
	if !ok {
		return code
	}
	if !dryRun {
		return runFactory(values, root, stdout, stderr)
	}

	tr, err := newTracker(values)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kraftwerk run: %v\n", err)
		return exitConfigError
	}
	snap, err := tr.Snapshot(context.Background())
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kraftwerk run: %v\n", err)
		return exitStopped
	}
	limit := maxParallel(values)
	p := frontier.Compute(snap, frontier.Options{
		ReadyLabel:     readyLabel(values),
		MaxParallel:    limit,
		LinkBlockerFix: tr.Describe().LinkBlockerFix,
	})
	printPlan(stdout, stderr, p, limit)

	for _, s := range p.Skipped {
		if s.NeedsFix {
			return exitFailures
		}
	}
	return exitOK
}

// printPlan prints a dry run's Plan.
func printPlan(stdout, stderr io.Writer, p frontier.Plan, limit int) {
	noun := "Tickets"
	if len(p.Take) == 1 {
		noun = "Ticket"
	}
	_, _ = fmt.Fprintf(stdout, "Iteration 1 would take %d %s (max_parallel = %d):\n", len(p.Take), noun, limit)
	for _, tk := range p.Take {
		_, _ = fmt.Fprintf(stdout, "  %s  %s  (%s)\n", tk.ID, tk.Title, where(tk))
	}
	if len(p.Later) > 0 {
		_, _ = fmt.Fprintln(stdout, "\nReady, for a later Iteration:")
		for _, tk := range p.Later {
			_, _ = fmt.Fprintf(stdout, "  %s  %s  (%s)\n", tk.ID, tk.Title, where(tk))
		}
	}
	if len(p.Skipped) > 0 {
		_, _ = fmt.Fprintln(stdout, "\nSkipped:")
		for _, s := range p.Skipped {
			_, _ = fmt.Fprintf(stdout, "  %s  %s: %s\n", s.Ticket.ID, s.Ticket.Title, s.Reason)
			for _, fix := range s.Fix {
				_, _ = fmt.Fprintf(stdout, "       fix: %s\n", fix)
			}
		}
	}
	for _, w := range p.Warnings {
		_, _ = fmt.Fprintf(stderr, "warning: %s\n", w)
	}
	_, _ = fmt.Fprintln(stdout, "\nDry run: nothing was written and nothing was started.")
}

// where names a Ticket's Spec, or says it stands alone.
func where(tk tracker.Ticket) string {
	if tk.Parent == nil {
		return "standalone"
	}
	return "Spec " + tk.Parent.Display
}
