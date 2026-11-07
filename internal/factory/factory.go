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
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/frontier"
	"github.com/Marcel-Goehn/kraftwerk/internal/git"
	"github.com/Marcel-Goehn/kraftwerk/internal/harness"
	"github.com/Marcel-Goehn/kraftwerk/internal/prompt"
	"github.com/Marcel-Goehn/kraftwerk/internal/redact"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker"
	"github.com/Marcel-Goehn/kraftwerk/internal/verify"
)

// Options shape a Run.
type Options struct {
	Tracker tracker.Tracker
	// Root is the project root, the directory that holds .kraftwerk/. It
	// is inside the user's checkout, which the Run never touches.
	Root string
	// Remote is the git remote branches are cut from and pushed to.
	Remote     string
	BaseBranch string
	Prefix     string
	ReadyLabel string
	// CannotDoLabel replaces ReadyLabel on a Ticket handed back to a
	// person.
	CannotDoLabel string
	MaxParallel   int
	MaxIterations int
	// RunStep runs a Step in Step.Dir and says how it ended.
	RunStep func(ctx context.Context, s Step) harness.Outcome
	// Review is whether the review Step runs after an implement Step that
	// committed.
	Review bool
	// Verify are the Feedback commands the Factory runs itself after the
	// last Step that changed anything; none means no checks.
	Verify []verify.Command
	// VerifyEnv is the env Verify runs with.
	VerifyEnv []string
	// VerifyTimeout stops a Feedback command that runs longer.
	VerifyTimeout time.Duration
	// Out gets a line for everything the Run does.
	Out io.Writer
	// LinkBlockerFix comes from the Tracker's Description.
	LinkBlockerFix func(ticket, blocker tracker.ID) string
	// ProtectedPaths are the Protected paths besides .kraftwerk/, which
	// always is one: files, directories (ending in "/") or globs, relative
	// to the project root.
	ProtectedPaths []string
	// Prompts are the project's editable parts of the prompts, by Step;
	// a Step without one gets the built-in part.
	Prompts map[string]prompt.Part
	// Commands are the Feedback commands the prompts' `{{commands}}`
	// lists.
	Commands []verify.Command
	// Standards is the Coding standards file review is told to follow, ""
	// for none.
	Standards string
	// Backoff is how long to wait before a Step's nth retry after a
	// transient error; nil is DefaultBackoff.
	Backoff func(retry int) time.Duration
	// Redactor knows the secrets a failure comment's excerpt is scanned
	// for; nil knows none.
	Redactor *redact.Redactor
	// RunID names the Run. A failure comment names it and the Run's log
	// directory.
	RunID string
	// Only, if set, limits the Run to the one Spec or standalone Ticket it
	// names, as a person typed it.
	Only string
}

// Names of the Steps.
const (
	Implement = "implement"
	Review    = "review"
)

// Step is one Step the Run hands to RunStep.
type Step struct {
	// Name is Implement or Review.
	Name   string
	Ticket tracker.Ticket
	// Dir is where the Step runs: the project root inside the Ticket's
	// worktree.
	Dir    string
	Prompt string
	// Log renders a line of the Step's output, such as a tool call, to
	// the Run's output and the Ticket's log. A verbose line (the agent's
	// text) goes only to the Ticket's log.
	Log func(line string, verbose bool)
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
	// ClaimedByOthers are the Specs with ready Tickets the Run skipped
	// because someone else claimed them, as the last Iteration saw them.
	ClaimedByOthers []tracker.Claimed
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
	// logs holds each Ticket's last rendered lines, by ID.Key.
	logs map[string]*lastLines
	// user is the login the Tracker acts as, whose Claims are this Run's.
	user string
	// claimed holds the Specs this Run claimed, by ID.Key.
	claimed map[string]bool
}

// Run runs Iterations until the frontier is empty or MaxIterations is
// reached.
func Run(ctx context.Context, opts Options) Result {
	r := &run{
		opts: opts, repo: git.Repo{Dir: opts.Root},
		worktrees: filepath.Join(opts.Root, StateDir, "worktrees"),
		specDirs:  map[string]string{}, done: map[string]bool{}, stoppedSpecs: map[string]bool{},
		logs: map[string]*lastLines{}, claimed: map[string]bool{},
	}
	if err := r.prepare(ctx); err != nil {
		r.res.Stopped = err
		return r.res
	}
	user, err := opts.Tracker.User(ctx)
	if err != nil {
		r.res.Stopped = fmt.Errorf("finding out whose claims are yours: %w", err)
		return r.res
	}
	r.user = user
	defer r.removeSpecWorktrees()

	for i := 1; i <= opts.MaxIterations; i++ {
		snap, err := opts.Tracker.Snapshot(ctx)
		if err != nil {
			r.res.Stopped = err
			return r.res
		}
		plan := frontier.Compute(r.workable(snap), frontier.Options{
			ReadyLabel: opts.ReadyLabel, MaxParallel: opts.MaxParallel, LinkBlockerFix: opts.LinkBlockerFix,
			User: r.user, Only: opts.Only,
		})
		r.res.ClaimedByOthers = plan.ClaimedByOthers
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
	rel, err := r.repo.Prefix(ctx)
	if err != nil {
		return &SetupError{Msg: fmt.Sprintf("%s is not in a git checkout: %v", r.opts.Root, err)}
	}
	r.rel = rel
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
		if r.done[tk.ID.Key] || r.stoppedSpecs[tk.SpecID().Key] {
			continue
		}
		out.Tickets = append(out.Tickets, tk)
	}
	return out
}

// work takes one Ticket through implement, the fast-forward, the push and
// the close. Only an error that stops the whole Run is returned; anything
// else fails the Ticket alone.
func (r *run) work(ctx context.Context, tk tracker.Ticket, snap tracker.Snapshot) error {
	if r.stoppedSpecs[tk.SpecID().Key] {
		return nil
	}
	r.done[tk.ID.Key] = true
	r.tlogf(tk, "%s (%s)", tk.Title, where(tk))

	var spec *tracker.Spec
	if tk.Parent != nil {
		if s, ok := snap.Specs[tk.Parent.Key]; ok {
			spec = &s
		}
	}
	if err := r.claim(ctx, tk, snap); err != nil {
		r.fail(tk, err.Error())
		return nil
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

	pc, err := r.promptContext(ctx, tk, spec, ticketBranch)
	if err != nil {
		r.fail(tk, err.Error())
		return nil
	}
	recent, err := r.recentCommits(ctx, specTip)
	if err != nil {
		r.fail(tk, err.Error())
		return nil
	}
	b := build{tk: tk, prompt: pc, recent: recent, branch: ticketBranch, worktree: ticketDir, dir: filepath.Join(ticketDir, r.rel), base: specTip}
	summary, failed, stop := r.build(ctx, b)
	if stop != nil {
		return stop
	}
	if failed != nil {
		r.failBuild(ctx, b, failed)
		return nil
	}
	commits, err := r.repo.Log(ctx, specTip, ticketBranch)
	if err != nil {
		r.fail(tk, err.Error())
		return nil
	}
	// The Ticket's tip goes to the remote Spec branch first, so a rejected
	// push leaves the local Spec branch where it was. Then the local one
	// fast-forwards to it.
	if err := r.repo.Push(ctx, r.opts.Remote, ticketBranch, specBranch); err != nil {
		r.stoppedSpecs[tk.SpecID().Key] = true
		r.fail(tk, fmt.Sprintf("the push of %s was rejected, so %s stops for this Run: %v", specBranch, tk.SpecID(), err))
		return nil
	}
	r.tlogf(tk, "pushed %s", specBranch)
	if err := (git.Repo{Dir: specDir}).MergeFastForward(ctx, ticketBranch); err != nil {
		r.fail(tk, fmt.Sprintf("%s was pushed, but the local branch can't be fast-forwarded to %s: %v", specBranch, ticketBranch, err))
		return nil
	}
	if err := r.opts.Tracker.Close(ctx, tk.ID, closeComment(summary, commits, specBranch)); err != nil {
		r.fail(tk, fmt.Sprintf("its commits are on %s, but closing it failed: %v", specBranch, err))
		return nil
	}
	r.res.Closed = append(r.res.Closed, tk)
	r.tlogf(tk, "closed")
	r.discard(ctx, b)
	r.releaseIfFinished(ctx, tk, spec)
	return nil
}

// claim claims the Ticket's Spec (or the Ticket, if it stands alone) the
// first time a Run takes a Ticket from it. A Spec the user claimed before
// keeps its Claim.
func (r *run) claim(ctx context.Context, tk tracker.Ticket, snap tracker.Snapshot) error {
	id := tk.SpecID()
	if snap.ClaimOf(tk) != nil || r.claimed[id.Key] {
		return nil
	}
	if err := r.opts.Tracker.Claim(ctx, id); err != nil {
		return err
	}
	r.claimed[id.Key] = true
	r.logf("%s: claimed", id)
	return nil
}

// releaseIfFinished releases the Claim on the Ticket's Spec once its last
// Ticket closed; a standalone Ticket's closing is its last. The Ticket is
// closed already, so a failed release only warns.
func (r *run) releaseIfFinished(ctx context.Context, tk tracker.Ticket, spec *tracker.Spec) {
	if spec != nil {
		for _, st := range spec.Tickets {
			if st.State == tracker.Open && !r.closed(st.ID) {
				return
			}
		}
	} else if tk.Parent != nil {
		return // without the Spec's Ticket list, it can't be known
	}
	id := tk.SpecID()
	if err := r.opts.Tracker.Release(ctx, id, r.user); err != nil {
		r.logf("%s: warning: its last Ticket closed, but releasing its claim failed: %v", id, err)
		return
	}
	r.logf("%s: released (its last Ticket closed)", id)
}

// closed reports whether this Run closed the Ticket.
func (r *run) closed(id tracker.ID) bool {
	for _, tk := range r.res.Closed {
		if tk.ID.Key == id.Key {
			return true
		}
	}
	return false
}

// build is one Ticket's work on its Ticket branch.
type build struct {
	tk tracker.Ticket
	// prompt and recent are what its Steps are told.
	prompt prompt.Context
	recent prompt.Recent
	// branch is the Ticket branch, checked out in worktree; dir is the
	// project root inside it.
	branch, worktree, dir string
	// base is the Spec branch tip the Ticket branch was cut from.
	base string
}

// verifyAttempts is how often implement → review → verify may run: a
// failed verify gets one retry.
const verifyAttempts = 2

// build runs implement, review and verify, retrying them once after a
// failed verify with the failing output in implement's prompt. It returns
// the summary for the close comment, or why the Ticket failed, or an
// error that stops the Run.
func (r *run) build(ctx context.Context, b build) (summary string, failed *failure, stop error) {
	var verifyFailed *verify.Failure
	for attempt := 1; ; attempt++ {
		summary, failed, stop = r.implementAndReview(ctx, b, verifyFailed)
		if failed != nil || stop != nil {
			return "", failed, stop
		}
		if verifyFailed = r.verify(ctx, b); verifyFailed == nil {
			return summary, nil, nil
		}
		if attempt == verifyAttempts {
			return "", &failure{handBack: true, reason: "verify: " + verifyFailed.Error() + ", again after a retry", verify: verifyFailed}, nil
		}
		r.tlogf(b.tk, "verify: %v; implement retries with its output", verifyFailed)
	}
}

// verify runs the Feedback commands in the Ticket's worktree.
func (r *run) verify(ctx context.Context, b build) *verify.Failure {
	if len(r.opts.Verify) == 0 {
		return nil
	}
	r.tlogf(b.tk, "verify: started")
	f := verify.Run(ctx, b.dir, r.opts.VerifyEnv, r.opts.VerifyTimeout, r.opts.Verify)
	if f == nil {
		r.tlogf(b.tk, "verify: passed")
		return nil
	}
	for l := range strings.Lines(f.Tail(verifyLogLines)) {
		r.tlogf(b.tk, "verify:   %s", strings.TrimRight(l, "\r\n"))
	}
	return f
}

// verifyLogLines is how many of a failed Feedback command's last lines
// go to the Run's output.
const verifyLogLines = 10

// implementAndReview runs implement and then, if implement committed and
// review is enabled, review. verifyFailed is the verify failure implement
// retries after, nil on the first attempt.
func (r *run) implementAndReview(ctx context.Context, b build, verifyFailed *verify.Failure) (summary string, failed *failure, stop error) {
	tk := b.tk
	start, err := r.repo.Rev(ctx, b.branch)
	if err != nil {
		return "", factoryFailure(err), nil
	}
	r.tlogf(tk, "implement: started on %s", b.branch)
	out, failed, stop := r.runStep(ctx, Step{Name: Implement, Ticket: tk, Dir: b.dir, Prompt: prompt.Implement(r.part(Implement), b.prompt, b.recent, verifyFailed), Log: r.stepLog(tk, Implement)})
	if failed != nil || stop != nil {
		return "", failed, stop
	}
	if out.Report.Outcome == harness.CannotDo {
		return "", &failure{handBack: true, cannotDo: true, reason: out.Report.Reason}, nil
	}
	summary = out.Report.Summary
	if n, err := r.countCommits(ctx, b.base, b.branch); err != nil {
		return "", factoryFailure(err), nil
	} else if n == 0 {
		return "", &failure{handBack: true, reason: "implement reported done without changes"}, nil
	}
	implemented, err := r.countCommits(ctx, start, b.branch)
	if err != nil {
		return "", factoryFailure(err), nil
	}
	if failed := r.checkProtected(ctx, b, Implement, start); failed != nil {
		return "", failed, nil
	}
	if !r.opts.Review || implemented == 0 {
		return summary, nil, nil
	}

	before, err := r.repo.Rev(ctx, b.branch)
	if err != nil {
		return "", factoryFailure(err), nil
	}
	r.tlogf(tk, "review: started")
	out, failed, stop = r.runStep(ctx, Step{Name: Review, Ticket: tk, Dir: b.dir, Prompt: prompt.Review(r.part(Review), b.prompt, b.base), Log: r.stepLog(tk, Review)})
	if failed != nil || stop != nil {
		return "", failed, stop
	}
	if out.Report.Outcome == harness.CannotDo {
		r.tlogf(tk, "review: warning: reported cannot_do (%s); review can only approve or fix, so it counts as done",
			firstNonEmpty(out.Report.Reason, out.Report.Summary))
	}
	fixes, err := r.countCommits(ctx, before, b.branch)
	if err != nil {
		return "", factoryFailure(err), nil
	}
	if failed := r.checkProtected(ctx, b, Review, before); failed != nil {
		return "", failed, nil
	}
	if fixes == 0 {
		r.tlogf(tk, "review: approved")
		return summary, nil, nil
	}
	r.tlogf(tk, "review: fixed it in %d %s", fixes, plural(fixes, "commit", "commits"))
	return summary + "\n\nReview: " + strings.TrimSpace(out.Report.Summary), nil, nil
}

// transientRetries is how often a Step that hit a transient error (a
// rate limit, an overload, the network) is run again before the Run
// stops.
const transientRetries = 3

// DefaultBackoff waits 30s before the first retry after a transient
// error, and twice as long before each next one.
func DefaultBackoff(retry int) time.Duration {
	return 30 * time.Second << (retry - 1)
}

// freshRetries is how often a Step that failed (an error of class other,
// no report, a timeout) is run again in a fresh session.
const freshRetries = 1

// runStep runs one Step until it ends with a report, or why the Ticket
// failed, or the error that stops the Run. A failed Step gets a fresh
// retry in the same worktree. The counters live only as long as this
// call, so only in this Run's memory.
func (r *run) runStep(ctx context.Context, s Step) (out harness.Outcome, failed *failure, stop error) {
	fresh, transient := 0, 0
	for {
		out = r.opts.RunStep(ctx, s)
		var why string
		switch {
		case out.Kind == harness.Failed && out.Class == harness.Auth:
			return out, nil, fmt.Errorf("%s %s: the Harness reported an auth error: %s", s.Ticket.ID, s.Name, out.Message)
		case out.Kind == harness.Failed && out.Class == harness.Transient:
			if transient == transientRetries {
				return out, nil, fmt.Errorf("%s %s: the Harness reported a transient error, again after %d retries: %s",
					s.Ticket.ID, s.Name, transientRetries, out.Message)
			}
			transient++
			wait := r.backoff(transient)
			r.tlogf(s.Ticket, "%s: transient error (%s); retry %d of %d in %s", s.Name, out.Message, transient, transientRetries, wait)
			if err := sleep(ctx, wait); err != nil {
				return out, nil, fmt.Errorf("%s %s: stopped while waiting to retry: %w", s.Ticket.ID, s.Name, err)
			}
			continue
		case out.Kind == harness.Failed:
			why = "the Harness failed: " + out.Message
		case out.Kind == harness.NoReport:
			why = "the agent ended without a report"
		default:
			return out, nil, nil
		}
		if fresh == freshRetries {
			return out, &failure{handBack: true, reason: s.Name + ": " + why + ", again after a retry"}, nil
		}
		fresh++
		r.tlogf(s.Ticket, "%s: %s; retrying in a fresh session", s.Name, why)
	}
}

// backoff is the wait before a transient error's nth retry.
func (r *run) backoff(retry int) time.Duration {
	if r.opts.Backoff != nil {
		return r.opts.Backoff(retry)
	}
	return DefaultBackoff(retry)
}

// sleep waits for d, or until ctx ends.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// countCommits is how many commits branch has on top of from.
func (r *run) countCommits(ctx context.Context, from, branch string) (int, error) {
	commits, err := r.repo.Log(ctx, from, branch)
	return len(commits), err
}

func (r *run) fail(tk tracker.Ticket, reason string) {
	r.res.Failed = append(r.res.Failed, Failure{Ticket: tk, Reason: reason})
	r.tlogf(tk, "failed: %s", reason)
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

// tlogf logs a line about a Ticket, after its ID, which also goes to
// the Ticket's log.
func (r *run) tlogf(tk tracker.Ticket, format string, a ...any) {
	line := tk.ID.String() + " " + fmt.Sprintf(format, a...)
	r.logf("%s", line)
	r.ticketLog(tk).add(line)
}

// ticketLog is the Ticket's log of its last rendered lines.
func (r *run) ticketLog(tk tracker.Ticket) *lastLines {
	l, ok := r.logs[tk.ID.Key]
	if !ok {
		l = &lastLines{}
		r.logs[tk.ID.Key] = l
	}
	return l
}

// stepLog is a Step's Log: each line, prefixed with the Ticket and the
// Step, goes to the Ticket's log and, unless verbose, the Run's output.
func (r *run) stepLog(tk tracker.Ticket, step string) func(string, bool) {
	l := r.ticketLog(tk)
	return func(text string, verbose bool) {
		for line := range strings.Lines(text) {
			line = fmt.Sprintf("%s %s: %s", tk.ID, step, strings.TrimRight(line, "\r\n"))
			if !verbose {
				r.logf("%s", line)
			}
			l.add(line)
		}
	}
}

// logf logs a line to the Run's output.
func (r *run) logf(format string, a ...any) {
	_, _ = fmt.Fprintf(r.opts.Out, format+"\n", a...)
}

// firstNonEmpty is a, or b when a is empty.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// plural is one if n is 1, else many.
func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// where names a Ticket's Spec, or says it stands alone.
func where(tk tracker.Ticket) string {
	if tk.Parent == nil {
		return "standalone"
	}
	return "Spec " + tk.Parent.Display
}
