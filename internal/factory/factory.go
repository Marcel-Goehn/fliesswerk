// Package factory is the Factory's loop: it takes the ready Tickets an
// Iteration may work on, implements each on a short-lived Ticket branch in
// a worktree it owns, fast-forwards the Spec branch, pushes it and closes
// the Ticket. Code decides all of it; the agent only writes code and
// reports.
package factory

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Marcel-Goehn/kraftwerk/internal/frontier"
	"github.com/Marcel-Goehn/kraftwerk/internal/git"
	"github.com/Marcel-Goehn/kraftwerk/internal/harness"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker"
)

// Options shape a Run.
type Options struct {
	Tracker tracker.Tracker
	// Root is the project root, the directory that holds .kraftwerk/. It
	// is inside the user's checkout, which the Run never touches.
	Root string
	// Remote is the git remote branches are cut from and pushed to.
	Remote        string
	BaseBranch    string
	Prefix        string
	ReadyLabel    string
	MaxParallel   int
	MaxIterations int
	// Implement runs the implement Step in Step.Dir and says how it ended.
	Implement func(ctx context.Context, s Step) harness.Outcome
	// Out gets a line for everything the Run does.
	Out io.Writer
	// LinkBlockerFix comes from the Tracker's Description.
	LinkBlockerFix func(ticket, blocker tracker.ID) string
}

// Step is one Step the Run hands to Implement.
type Step struct {
	Ticket tracker.Ticket
	// Dir is where the Step runs: the project root inside the Ticket's
	// worktree.
	Dir    string
	Prompt string
}

// Failure is a Ticket that didn't make it, and why.
type Failure struct {
	Ticket tracker.Ticket
	Reason string
}

// Result is how a Run ended.
type Result struct {
	Closed []tracker.Ticket
	Failed []Failure
	// Iterations is how many Iterations took Tickets.
	Iterations int
	// LimitReached reports that the Run ended at max_iterations rather
	// than with an empty frontier.
	LimitReached bool
	// NeedsFix reports that a Ticket was skipped until a person fixes it.
	NeedsFix bool
	// Stopped is why the Run stopped before its end, nil if it didn't. A
	// *SetupError means the project isn't set up for a Run.
	Stopped error
}

// SetupError is a problem with the project that a person must fix before
// any Run can start.
type SetupError struct{ Msg string }

func (e *SetupError) Error() string { return e.Msg }

// StateDir is the runtime state, relative to the project root. It must be
// git-ignored.
var StateDir = filepath.Join(".kraftwerk", "state")

type run struct {
	opts Options
	res  Result
	repo git.Repo
	// rel is the project root relative to the top of the checkout, so a
	// Step runs in the same place inside its worktree.
	rel       string
	worktrees string
	// specDirs holds the worktree of every Spec branch worked on so far.
	specDirs map[string]string
	// done holds the Tickets this Run is finished with, closed or failed,
	// and stoppedSpecs the Specs it may no longer push, by ID.Key.
	done         map[string]bool
	stoppedSpecs map[string]bool
}

// Run runs Iterations until the frontier is empty or MaxIterations is
// reached.
func Run(ctx context.Context, opts Options) Result {
	r := &run{
		opts: opts, repo: git.Repo{Dir: opts.Root},
		worktrees: filepath.Join(opts.Root, StateDir, "worktrees"),
		specDirs:  map[string]string{}, done: map[string]bool{}, stoppedSpecs: map[string]bool{},
	}
	if err := r.prepare(ctx); err != nil {
		r.res.Stopped = err
		return r.res
	}
	defer r.removeSpecWorktrees()

	for i := 1; i <= opts.MaxIterations; i++ {
		snap, err := opts.Tracker.Snapshot(ctx)
		if err != nil {
			r.res.Stopped = err
			return r.res
		}
		plan := frontier.Compute(r.workable(snap), frontier.Options{
			ReadyLabel: opts.ReadyLabel, MaxParallel: opts.MaxParallel, LinkBlockerFix: opts.LinkBlockerFix,
		})
		for _, s := range plan.Skipped {
			r.res.NeedsFix = r.res.NeedsFix || s.NeedsFix
		}
		if len(plan.Take) == 0 {
			r.logf("The frontier is empty.")
			return r.res
		}
		r.res.Iterations = i
		noun := "Tickets"
		if len(plan.Take) == 1 {
			noun = "Ticket"
		}
		r.logf("Iteration %d: taking %d %s", i, len(plan.Take), noun)
		for _, tk := range plan.Take {
			if err := r.work(ctx, tk, snap); err != nil {
				r.res.Stopped = err
				return r.res
			}
		}
	}
	r.res.LimitReached = true
	r.logf("max_iterations (%d) reached.", opts.MaxIterations)
	return r.res
}

// prepare checks that the runtime state is git-ignored and fetches the
// base branch.
func (r *run) prepare(ctx context.Context) error {
	top, err := r.repo.TopLevel(ctx)
	if err != nil {
		return &SetupError{Msg: fmt.Sprintf("%s is not in a git checkout: %v", r.opts.Root, err)}
	}
	if r.rel, err = filepath.Rel(top, r.opts.Root); err != nil {
		return err
	}
	probe := filepath.ToSlash(filepath.Join(StateDir, "worktrees", "probe"))
	ignored, err := r.repo.IsIgnored(ctx, probe)
	if err != nil {
		return err
	}
	if !ignored {
		return &SetupError{Msg: fmt.Sprintf("%s/ is not git-ignored, and the Factory keeps its worktrees there; add `state/` to .kraftwerk/.gitignore",
			filepath.ToSlash(StateDir))}
	}
	if err := r.repo.Fetch(ctx, r.opts.Remote, r.opts.BaseBranch); err != nil {
		return fmt.Errorf("fetching the base branch %s from %s: %w", r.opts.BaseBranch, r.opts.Remote, err)
	}
	return nil
}

// workable leaves out the Tickets this Run is finished with and those of
// Specs it stopped.
func (r *run) workable(snap tracker.Snapshot) tracker.Snapshot {
	out := snap
	out.Tickets = nil
	for _, tk := range snap.Tickets {
		if r.done[tk.ID.Key] || r.stoppedSpecs[specOf(tk).Key] {
			continue
		}
		out.Tickets = append(out.Tickets, tk)
	}
	return out
}

// specOf is the Spec a Ticket belongs to; a standalone Ticket is a Spec of
// one.
func specOf(tk tracker.Ticket) tracker.ID {
	if tk.Parent != nil {
		return *tk.Parent
	}
	return tk.ID
}

// work takes one Ticket through implement, the fast-forward, the push and
// the close. Only an error that stops the whole Run is returned; anything
// else fails the Ticket alone.
func (r *run) work(ctx context.Context, tk tracker.Ticket, snap tracker.Snapshot) error {
	if r.stoppedSpecs[specOf(tk).Key] {
		return nil
	}
	r.done[tk.ID.Key] = true
	r.logf("%s %s (%s)", tk.ID, tk.Title, where(tk))

	var spec *tracker.Spec
	if tk.Parent != nil {
		if s, ok := snap.Specs[tk.Parent.Key]; ok {
			spec = &s
		}
	}
	specBranch, err := r.specBranch(ctx, tk, spec)
	if err != nil {
		r.fail(tk, err.Error())
		return nil
	}
	specDir, err := r.specWorktree(ctx, specBranch)
	if err != nil {
		r.fail(tk, err.Error())
		return nil
	}
	specTip, err := r.repo.Rev(ctx, specBranch)
	if err != nil {
		r.fail(tk, err.Error())
		return nil
	}

	ticketBranch := r.opts.Prefix + "/ticket-" + tk.ID.Branch
	ticketDir := filepath.Join(r.worktrees, "ticket-"+tk.ID.Branch)
	if err := r.freshTicketBranch(ctx, ticketBranch, ticketDir, specTip); err != nil {
		r.fail(tk, err.Error())
		return nil
	}

	r.logf("%s implement: started on %s", tk.ID, ticketBranch)
	out := r.opts.Implement(ctx, Step{
		Ticket: tk, Dir: filepath.Join(ticketDir, r.rel),
		Prompt: implementPrompt(tk, spec, ticketBranch),
	})
	switch {
	case out.Kind == harness.Failed && (out.Class == harness.Auth || out.Class == harness.Transient):
		return fmt.Errorf("%s: the Harness reported a %s error: %s", tk.ID, out.Class, out.Message)
	case out.Kind == harness.Failed:
		r.fail(tk, "the Harness failed: "+out.Message)
		return nil
	case out.Kind == harness.NoReport:
		r.fail(tk, "the agent ended without a report")
		return nil
	case out.Report.Outcome == harness.CannotDo:
		r.fail(tk, "the agent can't do it: "+out.Report.Reason)
		return nil
	}

	commits, err := r.repo.Log(ctx, specTip, ticketBranch)
	if err != nil {
		r.fail(tk, err.Error())
		return nil
	}
	if len(commits) == 0 {
		r.fail(tk, "reported done without changes")
		return nil
	}
	// The Ticket's tip goes to the remote Spec branch first, so a rejected
	// push leaves the local Spec branch where it was. Then the local one
	// fast-forwards to it.
	if err := r.repo.Push(ctx, r.opts.Remote, ticketBranch, specBranch); err != nil {
		r.stoppedSpecs[specOf(tk).Key] = true
		r.fail(tk, fmt.Sprintf("the push of %s was rejected, so %s stops for this Run: %v", specBranch, specOf(tk), err))
		return nil
	}
	r.logf("%s: pushed %s", tk.ID, specBranch)
	if err := (git.Repo{Dir: specDir}).MergeFastForward(ctx, ticketBranch); err != nil {
		r.fail(tk, fmt.Sprintf("%s was pushed, but the local branch can't be fast-forwarded to %s: %v", specBranch, ticketBranch, err))
		return nil
	}
	if err := r.opts.Tracker.Close(ctx, tk.ID, closeComment(out.Report.Summary, commits, specBranch)); err != nil {
		r.fail(tk, fmt.Sprintf("its commits are on %s, but closing it failed: %v", specBranch, err))
		return nil
	}
	r.res.Closed = append(r.res.Closed, tk)
	r.logf("%s: closed", tk.ID)
	if err := r.repo.RemoveWorktree(ctx, ticketDir); err != nil {
		r.logf("warning: %v", err)
	}
	if err := r.repo.DeleteBranch(ctx, ticketBranch); err != nil {
		r.logf("warning: %v", err)
	}
	return nil
}

func (r *run) fail(tk tracker.Ticket, reason string) {
	r.res.Failed = append(r.res.Failed, Failure{Ticket: tk, Reason: reason})
	r.logf("%s failed: %s", tk.ID, reason)
}

// specBranch finds the branch of the Ticket's Spec by number, locally and
// then on the remote, or cuts it from the remote base branch. A standalone
// Ticket's branch takes the Spec branch's place.
func (r *run) specBranch(ctx context.Context, tk tracker.Ticket, spec *tracker.Spec) (string, error) {
	kind, id, title := "ticket", tk.ID, tk.Title
	if tk.Parent != nil {
		kind, id, title = "spec", *tk.Parent, ""
		if spec != nil {
			title = spec.Title
		}
	}
	prefix := r.opts.Prefix + "/" + kind + "-" + id.Branch + "-"
	local, err := r.repo.LocalBranches(ctx, prefix)
	if err != nil {
		return "", err
	}
	if b, err := one(local, id, "locally"); err != nil || b != "" {
		return b, err
	}
	remote, err := r.repo.RemoteBranches(ctx, r.opts.Remote, prefix)
	if err != nil {
		return "", err
	}
	b, err := one(remote, id, "on "+r.opts.Remote)
	if err != nil {
		return "", err
	}
	if b != "" {
		if err := r.repo.Fetch(ctx, r.opts.Remote, b); err != nil {
			return "", err
		}
		r.logf("%s: continuing %s from %s", id, b, r.opts.Remote)
		return b, r.repo.CreateBranch(ctx, b, r.opts.Remote+"/"+b)
	}
	b = prefix + slug(title, kind)
	r.logf("%s: cutting %s from %s/%s", id, b, r.opts.Remote, r.opts.BaseBranch)
	return b, r.repo.CreateBranch(ctx, b, r.opts.Remote+"/"+r.opts.BaseBranch)
}

// one is the single branch of branches, "" if there is none.
func one(branches []string, id tracker.ID, where string) (string, error) {
	switch len(branches) {
	case 0:
		return "", nil
	case 1:
		return branches[0], nil
	}
	return "", fmt.Errorf("%s has more than one branch %s (%s); delete all but one", id, where, strings.Join(branches, ", "))
}

// specWorktree checks the Spec branch out in a Factory worktree, once per
// Run.
func (r *run) specWorktree(ctx context.Context, branch string) (string, error) {
	if dir, ok := r.specDirs[branch]; ok {
		return dir, nil
	}
	dir := filepath.Join(r.worktrees, filepath.Base(branch))
	if err := r.clearWorktree(ctx, dir); err != nil {
		return "", err
	}
	if err := r.repo.AddWorktree(ctx, dir, branch); err != nil {
		return "", err
	}
	r.specDirs[branch] = dir
	return dir, nil
}

// freshTicketBranch cuts the Ticket branch from the Spec branch's tip in
// a new worktree, dropping what a failed attempt left.
func (r *run) freshTicketBranch(ctx context.Context, branch, dir, tip string) error {
	if err := r.clearWorktree(ctx, dir); err != nil {
		return err
	}
	if r.repo.HasBranch(ctx, branch) {
		if err := r.repo.DeleteBranch(ctx, branch); err != nil {
			return err
		}
	}
	if err := r.repo.CreateBranch(ctx, branch, tip); err != nil {
		return err
	}
	return r.repo.AddWorktree(ctx, dir, branch)
}

// clearWorktree removes whatever an earlier Run left at dir, which is
// always inside the runtime state.
func (r *run) clearWorktree(ctx context.Context, dir string) error {
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	_ = r.repo.RemoveWorktree(ctx, dir)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	return r.repo.PruneWorktrees(ctx)
}

// removeSpecWorktrees removes every Spec worktree at the end of the Run.
// Spec branches are always kept.
func (r *run) removeSpecWorktrees() {
	for _, dir := range r.specDirs {
		if err := r.repo.RemoveWorktree(context.Background(), dir); err != nil {
			r.logf("warning: %v", err)
		}
	}
}

func (r *run) logf(format string, a ...any) {
	_, _ = fmt.Fprintf(r.opts.Out, format+"\n", a...)
}

// where names a Ticket's Spec, or says it stands alone.
func where(tk tracker.Ticket) string {
	if tk.Parent == nil {
		return "standalone"
	}
	return "Spec " + tk.Parent.Display
}
