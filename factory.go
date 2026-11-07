package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/factory"
	"github.com/Marcel-Goehn/kraftwerk/internal/harness"
	"github.com/Marcel-Goehn/kraftwerk/internal/step"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker"
	"github.com/Marcel-Goehn/kraftwerk/internal/verify"
)

// Defaults for the keys of a Run the config may leave out.
const (
	defaultMaxIterations = 10
	defaultBaseBranch    = "main"
	defaultPrefix        = "kraftwerk"
	defaultSandbox       = "container"
	defaultCannotDoLabel = "needs-investigation"
	// defaultComposeFile is the compose file of a project that defines
	// Services without naming one.
	defaultComposeFile = "compose.yaml"
)

// runIDFormat makes a Run's id from its start time.
const runIDFormat = "2006-01-02T15-04-05"

// transientBackoff is the wait before a Step's retry after a transient
// error; tests shorten it.
var transientBackoff = factory.DefaultBackoff

// remote is the git remote branches are cut from and pushed to.
const remote = "origin"

// runFactory runs the Factory over the ready Tickets and prints how the Run
// ended. Only `kind = "none"` exists so far.
func runFactory(values map[string]any, root string, stdout, stderr io.Writer) int {
	if kind := stringAt(values, defaultSandbox, "sandbox", "kind"); kind != "none" {
		_, _ = fmt.Fprintf(stderr, "kraftwerk run: the %s Sandbox isn't available in this kraftwerk yet; run with `--sandbox none --i-accept-unsandboxed-risk` (or set them in .kraftwerk/config.local.toml)\n",
			kind)
		return exitConfigError
	}
	prompts, ok := loadPrompts(root, stderr)
	if !ok {
		_, _ = fmt.Fprintln(stderr, "kraftwerk run: the prompt files are not valid; fix the errors above")
		return exitConfigError
	}
	ctx := context.Background()
	s, code, ok := secretsForRun(values, root, stderr)
	if !ok {
		return code
	}
	stdout, stderr = s.redacted(stdout, stderr)
	if err := s.testHarnessCredential(ctx); err != nil {
		_, _ = fmt.Fprintf(stderr, "kraftwerk run: stopped: %v\n", err)
		return exitStopped
	}
	tr, err := newTracker(values, s.trackerToken)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kraftwerk run: %v\n", err)
		return exitConfigError
	}
	// The Tracker's checks prove its token (or login) works.
	for _, c := range tr.Describe().Checks {
		if err := c.Run(ctx); err != nil {
			_, _ = fmt.Fprintf(stderr, "kraftwerk run: stopped: %s: %v\n", c.Name, err)
			return exitStopped
		}
	}
	tr = tracker.Redacted(tr, s.redactor.String)

	res := factory.Run(ctx, factory.Options{
		Tracker:        tr,
		Root:           root,
		Remote:         remote,
		BaseBranch:     stringAt(values, defaultBaseBranch, "branches", "base_branch"),
		Prefix:         stringAt(values, defaultPrefix, "branches", "prefix"),
		ReadyLabel:     readyLabel(values),
		CannotDoLabel:  stringAt(values, defaultCannotDoLabel, "tracker", "cannot_do_label"),
		MaxParallel:    maxParallel(values),
		MaxIterations:  intAt(values, defaultMaxIterations, "max_iterations"),
		RunStep:        stepRunner(values, s),
		Review:         boolAt(values, true, "steps", "review", "enabled"),
		Verify:         verifyCommands(values),
		VerifyEnv:      s.commandEnv(boolAt(values, true, "sandbox", "scrub_credentials")),
		VerifyTimeout:  step.DefaultTimeout,
		Out:            stdout,
		LinkBlockerFix: tr.Describe().LinkBlockerFix,
		ProtectedPaths: protectedPaths(values),
		Prompts:        prompts,
		Commands:       feedbackCommands(values),
		Standards:      stringAt(values, "", "standards"),
		Backoff:        transientBackoff,
		Redactor:       s.redactor,
		RunID:          time.Now().Format(runIDFormat),
	})
	return printResult(stdout, stderr, res)
}

// secretsForRun resolves every secret a Run needs. ok is false when the
// command must exit with code: 3, since a person must fix the project
// first. (A credential the provider refuses stops the Run later, with 2.)
func secretsForRun(values map[string]any, root string, stderr io.Writer) (s *secrets, code int, ok bool) {
	s, err := resolveSecrets(context.Background(), values, root)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kraftwerk run: %v\n", err)
		return nil, exitConfigError, false
	}
	return s, exitOK, true
}

// stepRunner runs each Step with its own settings and the Harness's
// credentials in its env, rendering a line per tool call and the agent's
// text as verbose lines.
func stepRunner(values map[string]any, sec *secrets) func(context.Context, factory.Step) harness.Outcome {
	scrub := boolAt(values, true, "sandbox", "scrub_credentials")
	env, unset := sec.stepEnv(scrub)
	return func(ctx context.Context, req factory.Step) harness.Outcome {
		settings := settingsOf(values, req.Name)
		r := step.Runner{
			Harness:     settings.harness,
			Env:         env,
			Unset:       unset,
			IdleTimeout: settings.idleTimeout,
			Timeout:     settings.timeout,
			OnEvent: func(e harness.Event) {
				switch e.Kind {
				case harness.ToolCall:
					req.Log(strings.TrimSpace(e.Tool+" "+e.Summary), false)
				case harness.Text:
					req.Log(e.Text, true)
				}
			},
		}
		return r.Run(ctx, harness.Step{
			Prompt:           req.Prompt,
			Model:            settings.model,
			Effort:           settings.effort,
			ReportSchema:     harness.ReportSchema,
			ScrubCredentials: scrub,
		}, req.Dir)
	}
}

// stepSettings are how one Step runs.
type stepSettings struct {
	harness              harness.Harness
	model, effort        string
	idleTimeout, timeout time.Duration
}

// settingsOf resolves a Step's settings: its own keys in [steps.<name>],
// then the top-level ones, then the built-in defaults. An empty model or
// effort leaves the Harness's own default.
func settingsOf(values map[string]any, name string) stepSettings {
	return stepSettings{
		harness:     harnesses[stepSetting(values, name, "harness", defaultHarness)],
		model:       stepSetting(values, name, "model", ""),
		effort:      stepSetting(values, name, "effort", ""),
		idleTimeout: durationAt(values, step.DefaultIdleTimeout, "steps", name, "idle_timeout"),
		timeout:     durationAt(values, step.DefaultTimeout, "steps", name, "timeout"),
	}
}

// stepSetting is a Step's own setting, or else the top-level one, or else
// def.
func stepSetting(values map[string]any, stepName, key, def string) string {
	return stringAt(values, stringAt(values, def, key), "steps", stepName, key)
}

// protectedPaths are the Protected paths the config names, besides
// .kraftwerk/: the compose file, the standards file and protected_paths.
func protectedPaths(values map[string]any) []string {
	var paths []string
	services, _ := valueAt(values, "services").(map[string]any)
	if compose := stringAt(values, "", "services", "compose_file"); compose != "" {
		paths = append(paths, compose)
	} else if len(services) > 0 {
		paths = append(paths, defaultComposeFile)
	}
	if standards := stringAt(values, "", "standards"); standards != "" {
		paths = append(paths, standards)
	}
	extra, _ := valueAt(values, "protected_paths").([]any)
	for _, p := range extra {
		if s, ok := p.(string); ok {
			paths = append(paths, s)
		}
	}
	return paths
}

// verifyCommands are the Feedback commands `verify` names, in its order.
// The config has already checked that each is in [commands].
func verifyCommands(values map[string]any) []verify.Command {
	names, _ := valueAt(values, "verify").([]any)
	cmds := make([]verify.Command, 0, len(names))
	for _, n := range names {
		name, _ := n.(string)
		cmds = append(cmds, verify.Command{Name: name, Line: stringAt(values, "", "commands", name)})
	}
	return cmds
}

// feedbackCommands are every Feedback command in [commands], by name,
// for the prompts' `{{commands}}`.
func feedbackCommands(values map[string]any) []verify.Command {
	m, _ := valueAt(values, "commands").(map[string]any)
	cmds := make([]verify.Command, 0, len(m))
	for name, line := range m {
		s, _ := line.(string)
		cmds = append(cmds, verify.Command{Name: name, Line: s})
	}
	slices.SortFunc(cmds, func(a, b verify.Command) int { return strings.Compare(a.Name, b.Name) })
	return cmds
}

// durationAt is the duration at a key path, def if there is none. The
// config has already checked that it parses.
func durationAt(values map[string]any, def time.Duration, path ...string) time.Duration {
	if d, err := time.ParseDuration(stringAt(values, "", path...)); err == nil {
		return d
	}
	return def
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
