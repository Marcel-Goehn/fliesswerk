package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/egress"
	"github.com/Marcel-Goehn/kraftwerk/internal/factory"
	"github.com/Marcel-Goehn/kraftwerk/internal/git"
	"github.com/Marcel-Goehn/kraftwerk/internal/sandbox"
)

// probeCleanupTimeout bounds one step of removing what a probe set up.
const probeCleanupTimeout = time.Minute

// harnessEndpoint is where the Harness talks TLS, "" if the Factory doesn't
// know, or it doesn't.
func harnessEndpoint(s *secrets) string {
	if s.auth == nil {
		return ""
	}
	return s.auth.TLSEndpoint()
}

// probeDir is the worktree of the Sandbox that a probe starts.
func probeDir(root string) string { return filepath.Join(root, factory.StateDir, "probe") }

// removeProbeWorktree removes the worktree at dir, and whatever is left of it.
func removeProbeWorktree(ctx context.Context, repo git.Repo, dir string) {
	_ = repo.RemoveWorktree(ctx, dir)
	_ = os.RemoveAll(dir)
	_ = repo.PruneWorktrees(ctx)
}

// withCleanupDeadline runs f on a fresh deadline, for cleaning up after work
// of any length, even when ctx has ended.
func withCleanupDeadline(ctx context.Context, f func(context.Context)) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), probeCleanupTimeout)
	defer cancel()
	f(cleanup)
}

// probeInterception asks from inside a Sandbox whether it trusts the Harness's
// endpoint. The Sandbox has no Services and an empty worktree: it only exists
// to ask. The provider must be prepared.
func probeInterception(ctx context.Context, sandboxes sandboxProvider, values map[string]any, root string, s *secrets) (warning string, err error) {
	host := harnessEndpoint(s)
	if host == "" {
		return "", nil
	}
	repo := git.Repo{Dir: root}
	dir := probeDir(root)
	removeProbeWorktree(ctx, repo, dir) // what a crashed Run left
	if err := repo.AddEmptyWorktree(ctx, dir, "HEAD"); err != nil {
		return fmt.Sprintf("whether the Sandbox trusts %s wasn't checked: %v", host, err), nil
	}
	defer withCleanupDeadline(ctx, func(ctx context.Context) { removeProbeWorktree(ctx, repo, dir) })
	sb, err := sandboxes.Create(ctx, sandbox.Request{Name: "probe", Worktree: dir, NoServices: true})
	if err != nil {
		return "", err
	}
	defer withCleanupDeadline(ctx, func(ctx context.Context) { _ = sb.Destroy(ctx) })
	return askInterception(ctx, sb, values, host)
}

// askInterception has sb ask whether it trusts host.
func askInterception(ctx context.Context, sb sandbox.Sandbox, values map[string]any, host string) (warning string, err error) {
	route, err := egress.OptionsFrom(values, nil).Route()
	if err != nil {
		return "", err
	}
	return sandbox.CheckInterception(ctx, sb, host, sandbox.VerifiedChain(route, nil))
}
