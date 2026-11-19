package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/Marcel-Goehn/fliesswerk/internal/frontier"
	"github.com/Marcel-Goehn/fliesswerk/internal/runlog"
	"github.com/Marcel-Goehn/fliesswerk/internal/tracker"
)

// runRun starts a Run, over one Spec or standalone Ticket if an argument
// names it. With --dry-run it validates the config, reads the Snapshot and
// prints Iteration 1 and every skipped Ticket, writing nothing and
// starting nothing.
func runRun(args []string, stdout, stderr io.Writer) int {
	var dryRun bool
	var view runView
	values, root, rest, code, ok := loadConfig("fliesswerk run", args, 1, stderr, func(fs *flag.FlagSet) {
		fs.BoolVar(&dryRun, "dry-run", false, "show Iteration 1 and every skipped Ticket, writing nothing")
		fs.BoolVar(&view.json, "json", false, "print the Run's summary as JSON to stdout, and the live lines to stderr")
		fs.BoolVar(&view.verbose, "verbose", false, "show the agent's text too")
	})
	if !ok {
		return code
	}
	var only string
	if len(rest) == 1 {
		only = rest[0]
	}
	if !dryRun {
		return runFactory(values, root, only, view, stdout, stderr)
	}

	s, code, ok := secretsForRun(values, root, stderr)
	if !ok {
		return code
	}
	stdout, stderr = s.redacted(stdout, stderr)
	tr, err := newTracker(values, s.trackerToken)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "fliesswerk run: %v\n", err)
		return exitConfigError
	}
	ctx := context.Background()
	user, err := tr.User(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "fliesswerk run: %v\n", err)
		return exitStopped
	}
	snap, err := tr.Snapshot(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "fliesswerk run: %v\n", err)
		return exitStopped
	}
	limit := maxParallel(values)
	p := frontier.Compute(snap, frontier.Options{
		ReadyLabel:     readyLabel(values),
		MaxParallel:    limit,
		LinkBlockerFix: tr.Describe().LinkBlockerFix,
		User:           user,
		Only:           only,
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
	printClaimedByOthers(stdout, p.ClaimedByOthers)
	for _, w := range p.Warnings {
		_, _ = fmt.Fprintf(stderr, "warning: %s\n", w)
	}
	_, _ = fmt.Fprintln(stdout, "\nDry run: nothing was written and nothing was started.")
}

// runLogs prints the directory of the latest Run. It doesn't need a valid
// config: the logs matter most when something is broken.
func runLogs(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		_, _ = fmt.Fprintf(stderr, "fliesswerk logs: unexpected argument %q\n", args[0])
		return exitConfigError
	}
	cwd, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "fliesswerk logs: can't find the current directory: %v\n", err)
		return exitConfigError
	}
	dir, ok := runlog.Latest(projectRoot(cwd))
	if !ok {
		_, _ = fmt.Fprintln(stderr, "fliesswerk logs: no Run has left logs here yet")
		return exitFailures
	}
	_, _ = fmt.Fprintln(stdout, dir)
	return exitOK
}

// where names a Ticket's Spec, or says it stands alone.
func where(tk tracker.Ticket) string {
	if tk.Parent == nil {
		return "standalone"
	}
	return "Spec " + tk.Parent.Display
}
