package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcel-Goehn/fliesswerk/internal/config"
	"github.com/Marcel-Goehn/fliesswerk/internal/egress"
	"github.com/Marcel-Goehn/fliesswerk/internal/factory"
	"github.com/Marcel-Goehn/fliesswerk/internal/git"
	"github.com/Marcel-Goehn/fliesswerk/internal/memory"
	"github.com/Marcel-Goehn/fliesswerk/internal/sandbox/container"
	"github.com/Marcel-Goehn/fliesswerk/internal/sandbox/native"
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

// runCheck tells a user exactly where a setup is stuck, before any token is
// spent, and exits 3 if anything fails: the config, the tools and their
// versions, every credential, the remote, and a live Sandbox of the configured
// kind. With --commands it goes on to run the project's commands in it.
func runCheck(args []string, stdout, stderr io.Writer) int {
	var commands bool
	values, root, _, code, ok := loadConfig("fliesswerk check", args, 0, stderr, func(fs *flag.FlagSet) {
		fs.BoolVar(&commands, "commands", false, "also run sandbox_created and every Feedback command once, in a Sandbox on base_branch")
	})
	if !ok {
		return code
	}
	_, _ = fmt.Fprintln(stdout, "fliesswerk check: the config is valid")
	prompts, ok := loadPrompts(root, stderr)
	if !ok {
		_, _ = fmt.Fprintln(stderr, "fliesswerk check: fix the errors above")
		return exitConfigError
	}
	warnAboutPrompts(prompts, stderr)
	ctx := context.Background()
	fail := func(err error) int {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		_, _ = fmt.Fprintln(stderr, "fliesswerk check: fix the errors above")
		return exitConfigError
	}

	if err := git.CheckVersion(ctx); err != nil {
		return fail(err) // everything after this uses git
	}
	_, _ = fmt.Fprintln(stdout, "fliesswerk check: git ok")
	s, err := resolveSecrets(ctx, values, root)
	if err != nil {
		return fail(err)
	}
	stdout, stderr = s.redacted(stdout, stderr)
	if err := s.testHarnessCredential(ctx); err != nil {
		return fail(err)
	}
	switch {
	case s.auth != nil && s.auth.Tested():
		_, _ = fmt.Fprintf(stdout, "fliesswerk check: %s credential (%s) ok\n", s.auth.Harness, s.auth.Method)
	case s.auth != nil:
		_, _ = fmt.Fprintf(stdout, "fliesswerk check: %s credential (%s) found, not tested ahead of time: Claude Code checks it on the first Step\n", s.auth.Harness, s.auth.Method)
	}
	for _, w := range s.authWarnings(ctx) {
		_, _ = fmt.Fprintf(stderr, "warning: %s\n", w)
	}
	if len(s.project) > 0 {
		_, _ = fmt.Fprintf(stdout, "fliesswerk check: %d project %s resolved\n", len(s.project), pluralize(len(s.project), "secret", "secrets"))
		if !boolAt(values, true, "sandbox", "withhold_secrets") {
			_, _ = fmt.Fprintln(stderr, "warning: sandbox.withhold_secrets is false: the agent and verify get the project secrets ([sandbox.secrets]), and the agent can read them")
		}
	}

	refused, warnings := checkSandboxConfig(values, root, s)
	for _, w := range warnings {
		_, _ = fmt.Fprintf(stderr, "warning: %s\n", w)
	}
	if len(refused) > 0 {
		return fail(errors.New(strings.Join(refused, "\n")))
	}

	if err := checkTracker(ctx, values, s, stdout, stderr); err != nil {
		return fail(err)
	}
	if err := checkRemote(ctx, values, root); err != nil {
		return fail(err)
	}
	_, _ = fmt.Fprintf(stdout, "fliesswerk check: a dry-run push to %s ok\n", remote)
	if err := checkSandboxTools(ctx, values, root, stdout); err != nil {
		return fail(err)
	}
	if inSandbox(values) {
		reportMemory(root, stdout, stderr)
	}
	if err := checkLive(ctx, values, root, s, commands, stdout, stderr); err != nil {
		return fail(err)
	}
	return exitOK
}

// checkTracker runs the Tracker's own checks and warns about what other
// people's Runs have claimed.
func checkTracker(ctx context.Context, values map[string]any, s *secrets, stdout, stderr io.Writer) error {
	tr, err := newTracker(values, s.trackerToken)
	if err != nil {
		return err
	}
	for _, c := range tr.Describe().Checks {
		if err := c.Run(ctx); err != nil {
			return fmt.Errorf("%s: %w", c.Name, err) // later checks build on earlier ones
		}
		_, _ = fmt.Fprintf(stdout, "fliesswerk check: %s ok\n", c.Name)
	}
	if err := checkLabels(ctx, tr, values); err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, "fliesswerk check: labels ok")
	others, err := claimedByOthers(ctx, tr)
	if err != nil {
		return err
	}
	for _, c := range others {
		_, _ = fmt.Fprintf(stderr, "warning: %s; your Runs skip it\n", claimLine(c))
	}
	return nil
}

// checkRemote proves that a Run could cut a Spec branch from the base branch
// and push it, without creating anything.
func checkRemote(ctx context.Context, values map[string]any, root string) error {
	repo := git.Repo{Dir: root}
	base := stringAt(values, defaultBaseBranch, "branches", "base_branch")
	if err := repo.Fetch(ctx, remote, base); err != nil {
		return fmt.Errorf("can't fetch the base branch %s from %s: %w\nCheck that the remote `%s` exists and you can read it (`git remote -v`), and that `branches.base_branch` names a branch on it", base, remote, err, remote)
	}
	branch := stringAt(values, defaultPrefix, "branches", "prefix") + "/fliesswerk-check"
	if err := repo.DryRunPush(ctx, remote, "refs/remotes/"+remote+"/"+base, branch); err != nil {
		return fmt.Errorf("a dry-run push to %s failed, so a Run couldn't push its Spec branches: %w\nCheck this machine's git credentials for the remote: `git push --dry-run %s %s:refs/heads/%s`", remote, err, remote, base, branch)
	}
	return nil
}

// checkSandboxTools checks what the configured Sandbox kind is built from,
// without building one.
func checkSandboxTools(ctx context.Context, values map[string]any, root string, stdout io.Writer) error {
	say := func(what string) { _, _ = fmt.Fprintf(stdout, "fliesswerk check: %s ok\n", what) }
	hasServices := len(serviceNames(values)) > 0
	switch stringAt(values, defaultSandbox, "sandbox", "kind") {
	case "container":
		engine, err := container.CheckEngine(ctx, engineOf(values))
		if err != nil {
			return err
		}
		say("container engine " + engine.String())
		if err := refuseLoopbackProxy(values); err != nil {
			return err
		}
		if hasServices {
			if err := container.CheckServices(ctx, engine, root, composeFile(values), ownServices(values), sharedServices(values)); err != nil {
				return err
			}
			say("compose file")
		}
	case "native":
		if hasServices {
			engine, err := container.Resolve(ctx, engineOf(values))
			if err != nil {
				return err
			}
			say("container engine " + engine.String() + " (for the Services)")
			if err := container.CheckPublishedServices(ctx, engineOf(values), root, composeFile(values), ownServices(values), sharedServices(values)); err != nil {
				return err
			}
			say("compose file")
		}
		if _, err := native.Check(ctx); err != nil {
			return err
		}
		say("native Sandbox prerequisites")
	}
	return nil
}

// refuseLoopbackProxy stops a corporate proxy on the host's own localhost: the
// proxy sidecar is a container, where localhost is the sidecar itself.
func refuseLoopbackProxy(values map[string]any) error {
	address, err := egress.OptionsFrom(values, nil).LoopbackUpstream()
	if err != nil || address == "" {
		return err
	}
	return fmt.Errorf("the corporate proxy is at %s, on this machine's own localhost, where a container Sandbox's proxy sidecar can't reach it. Make the proxy listen on an address containers can reach and name that in `upstream_proxy` under [network], or use `kind = \"native\"`", address)
}

// reportMemory says what a Run's memory admission would start from.
func reportMemory(root string, stdout, stderr io.Writer) {
	store, err := memory.Load(filepath.Join(root, factory.MemoryFile))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "warning: %v\n", err)
	}
	how := "measured"
	if !store.Measured() {
		how = "not measured yet"
	}
	waits := store.LastRunWaits()
	_, _ = fmt.Fprintf(stdout, "fliesswerk check: memory: peak estimate %d MB per Sandbox (%s), %d %s for memory in the last Run\n",
		memory.MB(store.Estimate()), how, waits, pluralize(waits, "wait", "waits"))
}

// loadConfig parses the local flags (and any extra ones register adds),
// loads the config of the project around the current directory and prints
// its diagnostics. root is the project root, and rest the up to maxArgs
// arguments after the flags. ok is false when the command must exit with
// code.
func loadConfig(cmd string, args []string, maxArgs int, stderr io.Writer, register func(*flag.FlagSet)) (values map[string]any, root string, rest []string, code int, ok bool) {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var lf localFlags
	lf.register(fs)
	if register != nil {
		register(fs)
	}
	// Flags may come before and after the arguments.
	for {
		if err := fs.Parse(args); err != nil {
			return nil, "", nil, exitConfigError, false
		}
		if fs.NArg() == 0 {
			break
		}
		if len(rest) == maxArgs {
			_, _ = fmt.Fprintf(stderr, "%s: unexpected argument %q\n", cmd, fs.Arg(0))
			return nil, "", nil, exitConfigError, false
		}
		rest, args = append(rest, fs.Arg(0)), fs.Args()[1:]
	}
	cwd, err := os.Getwd()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "%s: can't find the current directory: %v\n", cmd, err)
		return nil, "", nil, exitConfigError, false
	}

	root = projectRoot(cwd)
	if exists(filepath.Join(root, filepath.FromSlash(config.ProjectFile))) && !exists(filepath.Join(root, filepath.FromSlash(config.LocalFile))) {
		_, _ = fmt.Fprintf(stderr, "%s: %s isn't there, and it holds your own settings for this project, such as the Sandbox kind and the container engine; run `fliesswerk init`, which asks for them and leaves the team's files alone\n", cmd, config.LocalFile)
		return nil, "", nil, exitConfigError, false
	}
	r := config.Load(root, lf.overrides(fs))
	r.Diagnostics = append(r.Diagnostics, config.CheckFactory(r.Values, version)...)
	for _, d := range r.Diagnostics {
		_, _ = fmt.Fprintln(stderr, d.String())
	}
	if !r.OK() {
		_, _ = fmt.Fprintf(stderr, "%s: the config is not valid; fix the errors above\n", cmd)
		return nil, "", nil, exitConfigError, false
	}
	noticeNewVersion(r.Values, stderr)
	return r.Values, root, rest, exitOK, true
}

// projectRoot is the nearest directory at or above dir that holds
// .fliesswerk/, looking no further than the enclosing git repository. With
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
