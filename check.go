package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Marcel-Goehn/kraftwerk/internal/config"
)

// localFlags are the CLI flags that override the config files. They count
// as a local layer, so they may hold local-only keys.
type localFlags struct {
	sandbox     string
	acceptRisk  bool
	allow       []string
	model       string
	maxParallel int
}

// register adds the flags to fs.
func (f *localFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&f.sandbox, "sandbox", "", "Sandbox kind: container, native or none (sandbox.kind)")
	fs.BoolVar(&f.acceptRisk, "i-accept-unsandboxed-risk", false, "allow --sandbox none (sandbox.i_accept_unsandboxed_risk)")
	fs.Func("allow", "a host the Sandbox may reach; repeat for more, \"*\" for any (sandbox.network.allow)", func(s string) error {
		f.allow = append(f.allow, s)
		return nil
	})
	fs.StringVar(&f.model, "model", "", "the model every Step uses (model)")
	fs.IntVar(&f.maxParallel, "max-parallel", 0, "how many Tickets an Iteration works on at once (max_parallel)")
}

// overrides turns the flags that were set into config overrides.
func (f *localFlags) overrides(fs *flag.FlagSet) []config.Override {
	var out []config.Override
	fs.Visit(func(fl *flag.Flag) {
		switch fl.Name {
		case "sandbox":
			out = append(out, config.Override{Key: "sandbox.kind", Value: f.sandbox})
		case "i-accept-unsandboxed-risk":
			out = append(out, config.Override{Key: "sandbox.i_accept_unsandboxed_risk", Value: f.acceptRisk})
		case "allow":
			hosts := make([]any, len(f.allow))
			for i, h := range f.allow {
				hosts[i] = h
			}
			out = append(out, config.Override{Key: "sandbox.network.allow", Value: hosts})
		case "model":
			out = append(out, config.Override{Key: "model", Value: f.model})
		case "max-parallel":
			out = append(out, config.Override{Key: "max_parallel", Value: int64(f.maxParallel)})
		}
	})
	return out
}

// runCheck validates the config, resolves and tests every credential and
// checks the Tracker's tools, and exits 3 if anything fails.
func runCheck(args []string, stdout, stderr io.Writer) int {
	values, root, code, ok := loadConfig("kraftwerk check", args, stderr, nil)
	if !ok {
		return code
	}
	_, _ = fmt.Fprintln(stdout, "kraftwerk check: the config is valid")
	ctx := context.Background()
	fail := func(err error) int {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		_, _ = fmt.Fprintln(stderr, "kraftwerk check: fix the errors above")
		return exitConfigError
	}

	s, err := resolveSecrets(ctx, values, root)
	if err != nil {
		return fail(err)
	}
	stdout, stderr = s.redacted(stdout, stderr)
	if err := s.testHarnessCredential(ctx); err != nil {
		return fail(err)
	}
	if s.auth != nil {
		_, _ = fmt.Fprintf(stdout, "kraftwerk check: %s credential (%s) ok\n", s.auth.Harness, s.auth.Method)
	}
	if len(s.project) > 0 {
		_, _ = fmt.Fprintf(stdout, "kraftwerk check: %d project %s resolved\n", len(s.project), pluralize(len(s.project), "secret", "secrets"))
		if !boolAt(values, true, "sandbox", "scrub_credentials") {
			_, _ = fmt.Fprintln(stderr, "warning: sandbox.scrub_credentials: with scrubbing off, the agent gets the project secrets ([sandbox.secrets]) and can read them")
		}
	}

	tr, err := newTracker(values, s.trackerToken)
	if err != nil {
		return fail(err)
	}
	for _, c := range tr.Describe().Checks {
		if err := c.Run(ctx); err != nil {
			return fail(fmt.Errorf("%s: %w", c.Name, err)) // later checks build on earlier ones
		}
		_, _ = fmt.Fprintf(stdout, "kraftwerk check: %s ok\n", c.Name)
	}
	return exitOK
}

// loadConfig parses the local flags (and any extra ones register adds),
// loads the config of the project around the current directory and prints
// its diagnostics. root is the project root. ok is false when the command
// must exit with code.
func loadConfig(cmd string, args []string, stderr io.Writer, register func(*flag.FlagSet)) (values map[string]any, root string, code int, ok bool) {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var lf localFlags
	lf.register(fs)
	if register != nil {
		register(fs)
	}
	if err := fs.Parse(args); err != nil {
		return nil, "", exitConfigError, false
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "%s: unexpected argument %q\n", cmd, fs.Arg(0))
		return nil, "", exitConfigError, false
	}
	cwd, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: can't find the current directory: %v\n", cmd, err)
		return nil, "", exitConfigError, false
	}

	root = projectRoot(cwd)
	r := config.Load(root, lf.overrides(fs))
	for _, d := range r.Diagnostics {
		_, _ = fmt.Fprintln(stderr, d.String())
	}
	if !r.OK() {
		_, _ = fmt.Fprintf(stderr, "%s: the config is not valid; fix the errors above\n", cmd)
		return nil, "", exitConfigError, false
	}
	return r.Values, root, exitOK, true
}

// projectRoot is the nearest directory at or above dir that holds
// .kraftwerk/, looking no further than the enclosing git repository. With
// none found, it is dir itself, where Load then reports the missing config.
func projectRoot(dir string) string {
	for d := dir; ; {
		if isDir(filepath.Join(d, config.Dir)) {
			return d
		}
		if exists(filepath.Join(d, ".git")) {
			return dir
		}
		parent := filepath.Dir(d)
		if parent == d {
			return dir
		}
		d = parent
	}
}

// isDir reports whether p is a directory.
func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// exists reports whether anything is at p.
func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
