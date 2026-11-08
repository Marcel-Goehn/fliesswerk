package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/factory"
	"github.com/Marcel-Goehn/kraftwerk/internal/harness"
	"github.com/Marcel-Goehn/kraftwerk/internal/hook"
	"github.com/Marcel-Goehn/kraftwerk/internal/prompt"
	"github.com/Marcel-Goehn/kraftwerk/internal/runlock"
	"github.com/Marcel-Goehn/kraftwerk/internal/runlog"
	"github.com/Marcel-Goehn/kraftwerk/internal/step"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker"
	"github.com/Marcel-Goehn/kraftwerk/internal/verify"
	"golang.org/x/term"
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

// transientBackoff is the wait before a Step's retry after a transient
// error; tests shorten it.
var transientBackoff = factory.DefaultBackoff

// writeBackoff is the wait before a failed Tracker write's retry; tests
// shorten it.
var writeBackoff = factory.DefaultWriteBackoff

// remote is the git remote branches are cut from and pushed to.
const remote = "origin"

// runView is how a Run shows itself.
type runView struct {
	// json prints the summary to stdout and the live lines to stderr.
	json bool
	// verbose shows the agent's text too.
	verbose bool
}

// defaultKeepDays is how long a Run's logs are kept without [logs] keep_days.
const defaultKeepDays = 30

// runFactory runs the Factory over the ready Tickets (of only the Spec or
// standalone Ticket only names, if set), recording the Run in its log
// directory, and prints how it ended. Only `kind = "none"` exists so far.
func runFactory(values map[string]any, root, only string, view runView, stdout, stderr io.Writer) int {
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
	s, code, ok := secretsForRun(values, root, stderr)
	if !ok {
		return code
	}
	lock, err := runlock.Acquire(filepath.Join(root, factory.StateDir, "lock"))
	var held *runlock.HeldError
	if errors.As(err, &held) {
		_, _ = fmt.Fprintf(stderr, "kraftwerk run: %v\n", err)
		return exitStopped
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kraftwerk run: can't take the Run's lock: %v\n", err)
		return exitConfigError
	}
	defer func() {
		if err := lock.Release(); err != nil {
			_, _ = fmt.Fprintf(stderr, "kraftwerk run: warning: releasing the Run's lock: %v\n", err)
		}
	}()

	live := stdout
	if view.json {
		live = stderr
	}
	colour := runlog.UseColour(isTerminal(live), os.Getenv("NO_COLOR"))
	live = s.redactor.Writer(live)
	stdout, stderr = s.redacted(stdout, stderr)

	started := time.Now()
	log, err := runlog.Open(runlog.Options{Root: root, Start: started, Terminal: live, Verbose: view.verbose, Colour: colour, Redactor: s.redactor})
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "kraftwerk run: can't create the Run's log directory: %v\n", err)
		return exitConfigError
	}
	if err := runlog.Prune(root, keepDays(values), started); err != nil {
		log.Write(runlog.Line{Kind: runlog.Warning, Text: fmt.Sprintf("warning: pruning old logs: %v", err)})
	}
	writeBanner(log, values, s)
	if lock.StalePID != 0 {
		log.Write(runlog.Line{Kind: runlog.Warning, Text: fmt.Sprintf("took over the lock of PID %d, which is no longer running", lock.StalePID)})
	}

	ctx, drain, stopWatching := watchCtrlC(log)
	defer stopWatching()
	var res factory.Result
	if tr, err := newTracker(values, s.trackerToken); err != nil {
		res.Stopped = &factory.SetupError{Msg: err.Error()}
	} else {
		opts := factoryOptions(values, root, only, tracker.Redacted(tr, s.redactor.String), s, prompts, log)
		opts.Drain = drain
		opts.Preflight = func(ctx context.Context) error { return preflight(ctx, tr, s) }
		res = factory.Run(ctx, opts)
	}
	code = exitCode(res)
	summary := log.Finish(summaryOf(res, log.ID(), started, code))
	if view.json {
		_, _ = stdout.Write(summary)
	}
	printStop(stderr, res)
	return code
}

// isTerminal reports whether w is a terminal.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(int(f.Fd())) //nolint:gosec // a file descriptor fits an int
}

// keepDays is `[logs] keep_days`, runlog.Forever for "forever".
func keepDays(values map[string]any) int {
	if stringAt(values, "", "logs", "keep_days") == "forever" {
		return runlog.Forever
	}
	return intAt(values, defaultKeepDays, "logs", "keep_days")
}

// writeBanner opens a Run's log with what the Run is and where it records
// itself.
func writeBanner(log *runlog.Log, values map[string]any, s *secrets) {
	auth := "its own login"
	if s.auth != nil {
		auth = string(s.auth.Method)
	}
	limits := settingsOf(values, factory.Implement)
	for _, text := range []string{
		"kraftwerk " + versionString(),
		fmt.Sprintf("Run %s, logs in %s", log.ID(), runlog.RelDir(log.ID())),
		fmt.Sprintf("Harness %s, auth %s", theHarness.Describe().Name, auth),
		"Sandbox " + stringAt(values, defaultSandbox, "sandbox", "kind"),
		fmt.Sprintf("Limits: max_iterations %d, max_parallel %d, idle %s, Step %s",
			intAt(values, defaultMaxIterations, "max_iterations"), maxParallel(values), limits.idleTimeout, limits.timeout),
	} {
		log.Write(runlog.Line{Text: text})
	}
}

// preflight proves the Harness's credential and the Tracker work before
// any Iteration starts.
func preflight(ctx context.Context, tr tracker.Tracker, s *secrets) error {
	if err := s.testHarnessCredential(ctx); err != nil {
		return err
	}
	// The Tracker's checks prove its token (or login) works.
	for _, c := range tr.Describe().Checks {
		if err := c.Run(ctx); err != nil {
			return fmt.Errorf("%s: %w", c.Name, err)
		}
	}
	return nil
}

// factoryOptions are the Options of a Run with this config.
func factoryOptions(values map[string]any, root, only string, tr tracker.Tracker, s *secrets, prompts map[string]prompt.Part, log *runlog.Log) factory.Options {
	return factory.Options{
		Tracker:        tr,
		Root:           root,
		Remote:         remote,
		BaseBranch:     stringAt(values, defaultBaseBranch, "branches", "base_branch"),
		Prefix:         stringAt(values, defaultPrefix, "branches", "prefix"),
		ReadyLabel:     readyLabel(values),
		CannotDoLabel:  stringAt(values, defaultCannotDoLabel, "tracker", "cannot_do_label"),
		MaxParallel:    maxParallel(values),
		MaxIterations:  intAt(values, defaultMaxIterations, "max_iterations"),
		RunStep:        stepRunner(values, s, log),
		Review:         boolAt(values, true, "steps", "review", "enabled"),
		Verify:         verifyCommands(values),
		VerifyEnv:      s.commandEnv(boolAt(values, true, "sandbox", "scrub_credentials")),
		VerifyTimeout:  step.DefaultTimeout,
		Log:            log.Write,
		LinkBlockerFix: tr.Describe().LinkBlockerFix,
		ProtectedPaths: protectedPaths(values),
		Prompts:        prompts,
		Commands:       feedbackCommands(values),
		Standards:      stringAt(values, "", "standards"),
		Backoff:        transientBackoff,
		WriteBackoff:   writeBackoff,
		Redactor:       s.redactor,
		RunID:          log.ID(),
		Only:           only,
		Hooks: factory.Hooks{
			RunStart:       hookEntries(values, "run_start", s),
			SandboxCreated: hookEntries(values, "sandbox_created", s),
			SpecFinished:   hookEntries(values, "spec_finished", s),
			RunEnd:         hookEntries(values, "run_end", s),
		},
	}
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
// credentials in its env. It logs a line per tool call, the agent's text,
// a warning at half the idle timeout and the Step's end line, and records
// the attempt's prompt and raw output.
func stepRunner(values map[string]any, sec *secrets, log *runlog.Log) func(context.Context, factory.Step) harness.Outcome {
	scrub := boolAt(values, true, "sandbox", "scrub_credentials")
	env, unset := sec.stepEnv(scrub)
	return func(ctx context.Context, req factory.Step) harness.Outcome {
		settings := settingsOf(values, req.Name)
		attempt := log.Attempt(req.Ticket.ID.Branch, req.Name, req.Prompt)
		defer attempt.Close()
		r := step.Runner{
			Harness:     settings.harness,
			Env:         env,
			Unset:       unset,
			IdleTimeout: settings.idleTimeout,
			Timeout:     settings.timeout,
			OnRaw:       attempt.Raw,
			OnIdleWarning: func(silent time.Duration) {
				req.Log(runlog.Warning, fmt.Sprintf("warning: no output for %s; the Step is stopped at %s (idle timeout)", silent, settings.idleTimeout))
			},
			OnEvent: func(e harness.Event) {
				switch e.Kind {
				case harness.ToolCall:
					req.Log(runlog.Tool, strings.TrimSpace(e.Tool+" "+e.Summary))
				case harness.Text:
					req.Log(runlog.AgentText, e.Text)
				}
			},
		}
		begin := time.Now()
		out := r.Run(ctx, harness.Step{
			Prompt:           req.Prompt,
			Model:            settings.model,
			Effort:           settings.effort,
			ReportSchema:     harness.ReportSchema,
			ScrubCredentials: scrub,
		}, req.Dir)
		req.Log(runlog.Event, runlog.StepEnd(time.Since(begin), out.Usage, out.ContextWindow))
		return out
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
	return append(paths, stringsOf(valueAt(values, "protected_paths"))...)
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

// hookEntries are the entries of a hook point, in declared order. A
// sandbox_created Hook runs in the Sandbox unless it says otherwise; every
// other point has only the host. The config has already checked each
// entry's shape.
func hookEntries(values map[string]any, point string, s *secrets) []hook.Entry {
	list, _ := valueAt(values, "hooks", point).([]any)
	entries := make([]hook.Entry, 0, len(list))
	for _, v := range list {
		table, _ := v.(map[string]any)
		where := hook.Host
		if point == "sandbox_created" {
			where = hook.Sandbox
		}
		where = stringAt(table, where, "where")
		entries = append(entries, hook.Entry{
			Commands: hookCommands(table),
			Where:    where,
			Timeout:  durationAt(table, 0, "timeout"),
			Env:      s.hookEnv(where, stringsOf(table["secrets"])),
		})
	}
	return entries
}

// hookCommands are the command lines of a Hook entry: its `run`, or its
// `parallel` group.
func hookCommands(table map[string]any) []string {
	if line, ok := table["run"].(string); ok {
		return []string{line}
	}
	return stringsOf(table["parallel"])
}

// stringsOf is v as a list of strings, none if it isn't one.
func stringsOf(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, e := range list {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// durationAt is the duration at a key path, def if there is none. The
// config has already checked that it parses.
func durationAt(values map[string]any, def time.Duration, path ...string) time.Duration {
	if d, err := time.ParseDuration(stringAt(values, "", path...)); err == nil {
		return d
	}
	return def
}

// exitCode is the exit code of a Run that ended with res.
func exitCode(res factory.Result) int {
	var setup *factory.SetupError
	switch {
	case errors.As(res.Stopped, &setup):
		return exitConfigError
	case res.Stopped != nil:
		return exitStopped
	case len(res.Failed) > 0 || res.NeedsFix:
		return exitFailures
	}
	return exitOK
}

// printStop says on stderr why the Run stopped, if it did.
func printStop(stderr io.Writer, res factory.Result) {
	var setup *factory.SetupError
	switch {
	case errors.As(res.Stopped, &setup):
		_, _ = fmt.Fprintf(stderr, "kraftwerk run: %v\n", res.Stopped)
	case res.Stopped != nil:
		_, _ = fmt.Fprintf(stderr, "kraftwerk run: stopped: %v\n", res.Stopped)
	}
}

// pluralize is one if n is 1, else many.
func pluralize(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
