package factory

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Marcel-Goehn/kraftwerk/internal/hook"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker"
)

// Hooks are the project's Hooks, by the moment they run at.
type Hooks struct {
	RunStart, SandboxCreated, SpecFinished, RunEnd []hook.Entry
}

// runHooks runs a hook point's entries. Each command's output goes
// through log, redacted as a whole, so that a secret spanning lines can't
// get through.
func (r *run) runHooks(ctx context.Context, point string, m hook.Setting, entries []hook.Entry, log func(string)) *hook.Failure {
	if len(entries) == 0 {
		return nil
	}
	log(point + ": started")
	return hook.Run(ctx, m, entries, func(o hook.Output) {
		status := "done"
		switch {
		case o.Stopped:
			status = "stopped: another command of its group failed first"
		case o.Err != nil:
			status = "failed: " + o.Err.Error()
		}
		log(fmt.Sprintf("%s: `%s` %s", point, o.Command, status))
		for l := range strings.Lines(r.redact(o.Text)) {
			log(point + ":   " + strings.TrimRight(l, "\r\n"))
		}
	})
}

// logLine logs one line to the Run's output.
func (r *run) logLine(line string) { r.logf("%s", line) }

// runStart runs the run_start Hooks, whose failure stops the Run.
func (r *run) runStart(ctx context.Context) error {
	if f := r.runHooks(ctx, "run_start", hook.Setting{HostDir: r.opts.Root}, r.opts.Hooks.RunStart, r.logLine); f != nil {
		return fmt.Errorf("the run_start Hook failed: %w", f)
	}
	return nil
}

// runEnd runs the run_end Hooks, whatever stopped the Run. Their failure
// only warns.
func (r *run) runEnd(ctx context.Context) {
	ctx = context.WithoutCancel(ctx)
	if f := r.runHooks(ctx, "run_end", hook.Setting{HostDir: r.opts.Root}, r.opts.Hooks.RunEnd, r.logLine); f != nil {
		r.warnf("warning: the run_end Hook failed: %v", f)
	}
}

// sandboxCreated runs the sandbox_created Hooks of a Ticket's Sandbox. A
// failure is a Step failure: the Hooks get one fresh retry in the same
// worktree. After that a build hands the Ticket back to a person, and a
// merge re-queues it.
func (r *run) sandboxCreated(ctx context.Context, b build) *failure {
	m := hook.Setting{HostDir: r.opts.Root, SandboxDir: b.dir, Sandbox: b.sandbox}
	log := func(line string) { r.tlogf(b.tk, "%s", line) }
	for fresh := 0; ; fresh++ {
		f := r.runHooks(ctx, "sandbox_created", m, r.opts.Hooks.SandboxCreated, log)
		if f == nil {
			return nil
		}
		if fresh == freshRetries {
			return &failure{handBack: true, kind: KindHook, reason: "sandbox_created: " + f.Error() + ", again after a retry"}
		}
		r.tlogf(b.tk, "sandbox_created: retrying after: %v", f)
	}
}

// specFinished runs the spec_finished Hooks on the Spec branch's worktree,
// so that a command working on the current branch (`gh pr create`) finds
// it. Their failure only warns.
func (r *run) specFinished(ctx context.Context, id tracker.ID, branch, specDir string) {
	m := hook.Setting{
		HostDir: filepath.Join(specDir, r.rel),
		Env:     []string{"KRAFTWERK_SPEC=" + id.Display, "KRAFTWERK_BRANCH=" + branch},
	}
	if f := r.runHooks(ctx, "spec_finished", m, r.opts.Hooks.SpecFinished, r.logLine); f != nil {
		r.warnf("%s: warning: the spec_finished Hook failed: %v", id, f)
	}
}
