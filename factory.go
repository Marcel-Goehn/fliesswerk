package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Marcel-Goehn/kraftwerk/internal/factory"
	"github.com/Marcel-Goehn/kraftwerk/internal/harness"
	"github.com/Marcel-Goehn/kraftwerk/internal/harness/claudecode"
	"github.com/Marcel-Goehn/kraftwerk/internal/step"
)

// Defaults for the keys of a Run the config may leave out.
const (
	defaultMaxIterations = 10
	defaultBaseBranch    = "main"
	defaultPrefix        = "kraftwerk"
	defaultSandbox       = "container"
)

// remote is the git remote branches are cut from and pushed to.
const remote = "origin"

// runFactory runs the Factory over the ready Tickets and prints how the Run
// ended. Only `kind = "none"` exists so far, and only the implement Step.
func runFactory(values map[string]any, root string, stdout, stderr io.Writer) int {
	if kind := stringAt(values, defaultSandbox, "sandbox", "kind"); kind != "none" {
		_, _ = fmt.Fprintf(stderr, "kraftwerk run: the %s Sandbox isn't available in this kraftwerk yet; run with `--sandbox none --i-accept-unsandboxed-risk` (or set them in .kraftwerk/config.local.toml)\n",
			kind)
		return exitConfigError
	}
	tr, err := newTracker(values)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kraftwerk run: %v\n", err)
		return exitConfigError
	}

	res := factory.Run(context.Background(), factory.Options{
		Tracker:        tr,
		Root:           root,
		Remote:         remote,
		BaseBranch:     stringAt(values, defaultBaseBranch, "branches", "base_branch"),
		Prefix:         stringAt(values, defaultPrefix, "branches", "prefix"),
		ReadyLabel:     readyLabel(values),
		MaxParallel:    maxParallel(values),
		MaxIterations:  intAt(values, defaultMaxIterations, "max_iterations"),
		Implement:      implementer(values, stdout),
		Out:            stdout,
		LinkBlockerFix: tr.Describe().LinkBlockerFix,
	})
	return printResult(stdout, stderr, res)
}

// implementer runs the implement Step with the configured Harness, printing
// a line per tool call.
func implementer(values map[string]any, out io.Writer) func(context.Context, factory.Step) harness.Outcome {
	s := harness.Step{
		Model:            stepSetting(values, "implement", "model"),
		Effort:           stepSetting(values, "implement", "effort"),
		ReportSchema:     harness.ReportSchema,
		ScrubCredentials: boolAt(values, true, "sandbox", "scrub_credentials"),
	}
	return func(ctx context.Context, req factory.Step) harness.Outcome {
		r := step.Runner{
			Harness: claudecode.ClaudeCode{}, // "claude_code" is the only Harness
			OnEvent: func(e harness.Event) {
				if e.Kind == harness.ToolCall {
					_, _ = fmt.Fprintln(out, strings.TrimSpace(fmt.Sprintf("%s implement: %s %s", req.Ticket.ID, e.Tool, e.Summary)))
				}
			},
		}
		st := s
		st.Prompt = req.Prompt
		return r.Run(ctx, st, req.Dir)
	}
}

// stepSetting is a Step's own setting, or else the top-level one.
func stepSetting(values map[string]any, stepName, key string) string {
	return stringAt(values, stringAt(values, "", key), "steps", stepName, key)
}

// printResult prints how the Run ended and returns its exit code.
func printResult(stdout, stderr io.Writer, res factory.Result) int {
	_, _ = fmt.Fprintf(stdout, "\nRun finished after %d %s: %d closed, %d failed.\n",
		res.Iterations, pluralize(res.Iterations, "Iteration", "Iterations"), len(res.Closed), len(res.Failed))
	for _, tk := range res.Closed {
		_, _ = fmt.Fprintf(stdout, "  closed  %s  %s\n", tk.ID, tk.Title)
	}
	for _, f := range res.Failed {
		_, _ = fmt.Fprintf(stdout, "  failed  %s  %s: %s\n", f.Ticket.ID, f.Ticket.Title, f.Reason)
	}
	var setup *factory.SetupError
	switch {
	case errors.As(res.Stopped, &setup):
		_, _ = fmt.Fprintf(stderr, "kraftwerk run: %v\n", res.Stopped)
		return exitConfigError
	case res.Stopped != nil:
		_, _ = fmt.Fprintf(stderr, "kraftwerk run: stopped: %v\n", res.Stopped)
		return exitStopped
	case len(res.Failed) > 0 || res.NeedsFix:
		return exitFailures
	}
	return exitOK
}

// pluralize is one if n is 1, else many.
func pluralize(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
