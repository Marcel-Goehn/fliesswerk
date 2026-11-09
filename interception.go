package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/egress"
	"github.com/Marcel-Goehn/kraftwerk/internal/factory"
	"github.com/Marcel-Goehn/kraftwerk/internal/git"
	"github.com/Marcel-Goehn/kraftwerk/internal/runlock"
	"github.com/Marcel-Goehn/kraftwerk/internal/sandbox"
)

// probeCleanupTimeout bounds removing the Sandbox that asks.
const probeCleanupTimeout = time.Minute

// harnessEndpoint is where the Harness talks TLS, "" if the Factory doesn't
// know, or it doesn't.
func harnessEndpoint(s *secrets) string {
	if s.auth == nil {
		return ""
	}
	return s.auth.TLSEndpoint()
}

// probeInterception asks from inside a Sandbox whether it trusts the Harness's
// endpoint. The Sandbox has no Services and an empty worktree: it only exists
// to ask. The provider must be prepared.
func probeInterception(ctx context.Context, sandboxes sandboxProvider, values map[string]any, root string, s *secrets) (warning string, err error) {
	host := harnessEndpoint(s)
	if host == "" {
		return "", nil
	}
	route, err := egress.OptionsFrom(values, nil).Route()
	if err != nil {
		return "", err
	}
	repo := git.Repo{Dir: root}
	dir := filepath.Join(root, factory.StateDir, "probe")
	removeProbe := func(ctx context.Context) {
		_ = repo.RemoveWorktree(ctx, dir)
		_ = os.RemoveAll(dir)
		_ = repo.PruneWorktrees(ctx)
	}
	removeProbe(ctx) // what a crashed Run left
	if err := repo.AddEmptyWorktree(ctx, dir, "HEAD"); err != nil {
		return fmt.Sprintf("whether the Sandbox trusts %s wasn't checked: %v", host, err), nil
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), probeCleanupTimeout)
	defer cancel()
	defer removeProbe(cleanup)
	sb, err := sandboxes.Create(ctx, sandbox.Request{Name: "probe", Worktree: dir, NoServices: true})
	if err != nil {
		return "", err
	}
	defer func() { _ = sb.Destroy(cleanup) }()
	return sandbox.CheckInterception(ctx, sb, host, sandbox.VerifiedChain(route, nil))
}

// checkTrust is `check`'s part of the interception check. It holds the Run's
// lock throughout, since connecting clears what the checkout's Sandboxes left
// and would otherwise pull a running Run's out from under it.
func checkTrust(ctx context.Context, values map[string]any, root string, s *secrets, stdout, stderr io.Writer) error {
	host := harnessEndpoint(s)
	if host == "" {
		return nil
	}
	warn := func(format string, a ...any) { _, _ = fmt.Fprintf(stderr, "warning: "+format+"\n", a...) }
	lock, err := runlock.Acquire(filepath.Join(root, factory.StateDir, "lock"))
	var held *runlock.HeldError
	if errors.As(err, &held) {
		warn("a Run is in progress, so whether a Sandbox trusts %s wasn't checked", host)
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = lock.Release() }()

	kind := stringAt(values, defaultSandbox, "sandbox", "kind")
	sandboxes := newSandboxes(values, root, kind, s, fmt.Sprintf("check-%d", os.Getpid()), func(string) {})
	defer sandboxes.Close()
	if err := sandboxes.Connect(ctx); err != nil {
		warn("whether a Sandbox trusts %s wasn't checked: %v", host, err) // as without an engine, a Run would say so
		return nil
	}
	if err := sandboxes.Prepare(ctx); err != nil {
		return err
	}
	warning, err := probeInterception(ctx, sandboxes, values, root, s)
	if err != nil {
		return err
	}
	if warning != "" {
		warn("%s", warning)
		return nil
	}
	_, _ = fmt.Fprintf(stdout, "kraftwerk check: a Sandbox trusts %s\n", host)
	return nil
}
