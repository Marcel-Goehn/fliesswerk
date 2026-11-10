package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Marcel-Goehn/fliesswerk/internal/egress"
	"github.com/Marcel-Goehn/fliesswerk/internal/factory"
	"github.com/Marcel-Goehn/fliesswerk/internal/git"
	"github.com/Marcel-Goehn/fliesswerk/internal/hook"
	"github.com/Marcel-Goehn/fliesswerk/internal/runlock"
	"github.com/Marcel-Goehn/fliesswerk/internal/sandbox"
	"github.com/Marcel-Goehn/fliesswerk/internal/step"
	"github.com/Marcel-Goehn/fliesswerk/internal/verify"
)

const (
	// harnessVersionTimeout bounds asking the Harness for its version.
	harnessVersionTimeout = 30 * time.Second
	// failedOutputLines is how much of a failed command `check` shows.
	failedOutputLines = 20
)

// liveCheck is one `check` going beyond what can be read off the machine.
type liveCheck struct {
	values         map[string]any
	root           string
	s              *secrets
	stdout, stderr io.Writer
}

func (c liveCheck) say(format string, a ...any) {
	_, _ = fmt.Fprintf(c.stdout, "fliesswerk check: "+format+"\n", a...)
}

func (c liveCheck) warn(format string, a ...any) {
	_, _ = fmt.Fprintf(c.stderr, "warning: "+format+"\n", a...)
}

// sandboxLog reports a line from what builds the Sandboxes.
func (c liveCheck) sandboxLog(line string) {
	if w, ok := strings.CutPrefix(line, "warning: "); ok {
		c.warn("%s", w)
		return
	}
	c.say("%s", line)
}

// checkLive proves the configured Sandbox kind in a real Sandbox on the base
// branch, and with commands runs the project's commands there too. It holds
// the Run's lock throughout, since connecting clears what the checkout's
// Sandboxes left and would otherwise pull a running Run's out from under it.
func checkLive(ctx context.Context, values map[string]any, root string, s *secrets, commands bool, stdout, stderr io.Writer) error {
	c := liveCheck{values: values, root: root, s: s, stdout: stdout, stderr: stderr}
	if !inSandbox(values) {
		if err := c.checkHarness(ctx, nil, root); err != nil {
			return err
		}
		c.say("no Sandbox to probe (sandbox.kind is none)")
		if !commands {
			return nil
		}
	}
	lock, err := runlock.Acquire(filepath.Join(root, factory.StateDir, "lock"))
	var held *runlock.HeldError
	if errors.As(err, &held) {
		if commands {
			return fmt.Errorf("%w; run `fliesswerk check --commands` once it has finished", err)
		}
		c.warn("a Run is in progress, so no Sandbox was started to probe its network or trust")
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()
	return c.checkInSandbox(ctx, commands)
}

// checkInSandbox sets up the Sandbox on the base branch and checks in it. With no
// Sandbox kind there is only the worktree, for the commands to run in.
func (c liveCheck) checkInSandbox(ctx context.Context, commands bool) error {
	kind := stringAt(c.values, defaultSandbox, "sandbox", "kind")
	sandboxes := newSandboxes(c.values, c.root, kind, c.s, fmt.Sprintf("check-%d", os.Getpid()), c.sandboxLog)
	if sandboxes != nil {
		defer sandboxes.Close()
		if c.s.usesBedrock() {
			// Every Sandbox mounts it, whether or not it holds credentials yet.
			if err := os.MkdirAll(credentialDir(c.root), 0o700); err != nil {
				return err
			}
		}
		if err := sandboxes.Connect(ctx); err != nil {
			return err
		}
		if err := sandboxes.Prepare(ctx); err != nil {
			return err
		}
	}

	repo := git.Repo{Dir: c.root}
	base := stringAt(c.values, defaultBaseBranch, "branches", "base_branch")
	rel, err := repo.Prefix(ctx)
	if err != nil {
		return err
	}
	dir := probeDir(c.root)
	removeProbeWorktree(ctx, repo, dir) // what a crashed `check` left
	if err := repo.AddDetachedWorktree(ctx, dir, remote+"/"+base); err != nil {
		return fmt.Errorf("checking out the base branch %s: %w", base, err)
	}
	defer withCleanupDeadline(ctx, func(ctx context.Context) { removeProbeWorktree(ctx, repo, dir) })

	project := filepath.Join(dir, rel)
	var sb sandbox.Sandbox
	if sandboxes != nil {
		if len(sharedServices(c.values)) > 0 {
			stop, err := sandboxes.StartShared(ctx, sandbox.Request{Name: "shared", Worktree: dir, Rel: rel})
			if stop != nil {
				defer withCleanupDeadline(ctx, func(ctx context.Context) { _ = stop(ctx) })
			}
			if err != nil {
				return err
			}
		}
		sb, err = sandboxes.Create(ctx, sandbox.Request{Name: "probe", Worktree: dir, Rel: rel, Protected: protectedPaths(c.values)})
		if err != nil {
			return err
		}
		defer withCleanupDeadline(ctx, func(ctx context.Context) { _ = sb.Destroy(ctx) })
		if err := c.probe(ctx, sb); err != nil {
			return err
		}
		if err := c.checkHarness(ctx, sb, project); err != nil {
			return err
		}
	}
	if !commands {
		return nil
	}
	return c.runCommands(ctx, sb, project, base)
}

// probe proves the Sandbox's network from inside it, and that it trusts what
// it will talk to.
func (c liveCheck) probe(ctx context.Context, sb sandbox.Sandbox) error {
	report, err := sandbox.ProbeNetwork(ctx, sb, allowedHost(c.values, c.s))
	if err != nil {
		return err
	}
	for _, line := range report.Passed {
		c.say("%s", line)
	}
	for _, w := range report.Warnings {
		c.warn("%s", w)
	}
	if len(report.Problems) > 0 {
		return errors.New(strings.Join(report.Problems, "\n"))
	}
	host := harnessEndpoint(c.s)
	if host == "" {
		return nil
	}
	warning, err := askInterception(ctx, sb, c.values, host)
	if err != nil {
		return err
	}
	if warning != "" {
		c.warn("%s", warning)
		return nil
	}
	c.say("a Sandbox trusts %s", host)
	return nil
}

// allowedHost is a host a Sandbox should reach through the proxy, "" for none.
func allowedHost(values map[string]any, s *secrets) string {
	if host := harnessEndpoint(s); host != "" {
		return host
	}
	for _, host := range egress.OptionsFrom(values, nil).Allow {
		if host != "*" {
			return host
		}
	}
	return ""
}

// checkHarness verifies the Harness at its minimum version, where its Steps
// will run: in sb, or on the host in dir when sb is nil.
func (c liveCheck) checkHarness(ctx context.Context, sb sandbox.Sandbox, dir string) error {
	ctx, cancel := context.WithTimeout(ctx, harnessVersionTimeout)
	defer cancel()
	desc := theHarness.Describe()
	line := strings.Join(append([]string{desc.Binary}, desc.VersionArgs...), " ")
	out, err := sandbox.Shell(ctx, sb, dir, line, nil).CombinedOutput()
	if err == nil {
		err = desc.CheckVersion(string(out))
	} else {
		err = fmt.Errorf("%s doesn't run where Steps run (`%s`: %w)", desc.Binary, line, err)
	}
	if err != nil {
		return fmt.Errorf("%w. %s", err, c.harnessFix(desc.ContainerInstall, desc.HostInstallHint))
	}
	c.say("%s ok", desc.Binary)
	return nil
}

// harnessFix says how to get a suitable Harness for the configured kind.
func (c liveCheck) harnessFix(containerfile string, host map[string]string) string {
	if stringAt(c.values, defaultSandbox, "sandbox", "kind") == "container" {
		return "Install it in .fliesswerk/Containerfile:\n\n" + containerfile
	}
	return "Install it: " + host[runtime.GOOS]
}

// runCommands runs sandbox_created and then every Feedback command once, on
// the base branch. A red command doesn't stop the others: the point is to see
// them all before a Run trips over them.
func (c liveCheck) runCommands(ctx context.Context, sb sandbox.Sandbox, project, base string) error {
	setting := hook.Setting{HostDir: c.root, SandboxDir: project, Sandbox: sb, Env: []string{"FLIESSWERK_SANDBOX=probe"}}
	failed := hook.Run(ctx, setting, hookEntries(c.values, "sandbox_created", c.s), func(o hook.Output) {
		if o.Err == nil {
			c.say("sandbox_created: `%s` worked", o.Command)
			return
		}
		_, _ = fmt.Fprintf(c.stderr, "sandbox_created: `%s` failed: %v\n%s\n", o.Command, o.Err, tail(o.Text))
	})
	if failed != nil {
		return fmt.Errorf("sandbox_created failed, so the Feedback commands weren't run; a Run would hand every Ticket back: %v", failed)
	}

	cmds := feedbackCommands(c.values)
	if len(cmds) == 0 {
		c.say("no Feedback commands to run: [commands] is empty")
		return nil
	}
	env := verifyEnv(c.values, c.s)
	var red []string
	for _, cmd := range cmds {
		if f := verify.Run(ctx, sb, project, env, step.DefaultTimeout, []verify.Command{cmd}); f != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			_, _ = fmt.Fprintf(c.stderr, "`%s` is already failing on %s (%s): %v\n%s\n", cmd.Name, base, cmd.Line, f.Err, tail(f.Output))
			red = append(red, "`"+cmd.Name+"`")
			continue
		}
		c.say("`%s` passes on %s", cmd.Name, base)
	}
	if len(red) > 0 {
		return fmt.Errorf("%s already %s on %s, before any agent changed anything; fix %s there, or correct [commands]", strings.Join(red, ", "), pluralize(len(red), "fails", "fail"), base, pluralize(len(red), "it", "them"))
	}
	return nil
}

// tail is the end of what a failed command printed.
func tail(output string) string {
	lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
	return "  " + strings.Join(lines[max(0, len(lines)-failedOutputLines):], "\n  ")
}
