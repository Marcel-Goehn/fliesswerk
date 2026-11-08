package factory

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Marcel-Goehn/kraftwerk/internal/git"
	"github.com/Marcel-Goehn/kraftwerk/internal/harness"
	"github.com/Marcel-Goehn/kraftwerk/internal/prompt"
	"github.com/Marcel-Goehn/kraftwerk/internal/runlog"
)

// mergeQueue lets the Tickets of one Spec merge one at a time, in the
// order they entered it.
type mergeQueue struct {
	mu      sync.Mutex
	busy    bool
	waiting []chan struct{}
}

// enter blocks until it is the caller's turn.
func (q *mergeQueue) enter() {
	q.mu.Lock()
	if !q.busy {
		q.busy = true
		q.mu.Unlock()
		return
	}
	turn := make(chan struct{})
	q.waiting = append(q.waiting, turn)
	q.mu.Unlock()
	<-turn
}

// leave hands the turn to the next Ticket.
func (q *mergeQueue) leave() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.waiting) == 0 {
		q.busy = false
		return
	}
	close(q.waiting[0])
	q.waiting = q.waiting[1:]
}

// land takes a built Ticket through its Spec's merge queue: merge, verify
// on the merged result, push, close. Only an error that stops the whole
// Run is returned.
func (r *run) land(w *ticketWork, summary string) error {
	r.tlogf(w.tk, "queued to merge into %s", w.specBranch)
	w.lane.queue.enter()
	defer w.lane.queue.leave()
	ctx, tk := w.lane.ctx, w.tk
	if ctx.Err() != nil {
		return r.abandon(w)
	}
	// Only the Ticket's own commits count, so this is read before a merge
	// commit joins them.
	commits, err := r.repo.Log(ctx, w.b.base, w.b.branch)
	if err != nil {
		if ctx.Err() != nil {
			return r.abandon(w)
		}
		r.fail(tk, KindFactory, err.Error())
		return nil
	}
	r.setCommits(tk, len(commits))

	failed, stop := r.merge(ctx, w)
	if ctx.Err() != nil {
		return r.abandon(w)
	}
	if stop != nil {
		return stop
	}
	if failed != nil {
		r.mergeFailed(ctx, w, failed)
		return nil
	}

	// From the push on, the Ticket's work lands whole: a kill can't leave
	// commits on the remote with the Ticket still open. Tracker writes
	// keep ctx, since a kill must end their waits; they survive it anyway.
	landing := context.WithoutCancel(ctx)
	// The Ticket's tip goes to the remote Spec branch first, so a rejected
	// push leaves the local Spec branch where it was. Then the local one
	// fast-forwards to it.
	if err := r.repo.Push(landing, r.opts.Remote, w.b.branch, w.specBranch); err != nil {
		if !errors.Is(err, git.ErrPushRejected) {
			r.fail(tk, KindFactory, fmt.Sprintf("pushing %s failed: %v", w.specBranch, err))
			return nil
		}
		r.stopSpec(w)
		r.fail(tk, KindPushRejected, fmt.Sprintf("the push of %s was rejected, so %s stops for this Run: %v", w.specBranch, tk.SpecID(), err))
		return nil
	}
	r.tlogf(tk, "pushed %s", w.specBranch)
	if err := (git.Repo{Dir: w.specDir}).MergeFastForward(landing, w.b.branch); err != nil {
		r.fail(tk, KindFactory, fmt.Sprintf("%s was pushed, but the local branch can't be fast-forwarded to %s: %v", w.specBranch, w.b.branch, err))
		return nil
	}
	w.lane.landed = append(w.lane.landed, landedTicket{tk: tk, commits: commits})
	if w.resolution != "" {
		summary += "\n\nMerge: " + strings.TrimSpace(w.resolution)
	}
	if err := r.opts.Tracker.Close(ctx, tk.ID, closeComment(summary, commits, w.specBranch)); err != nil {
		r.fail(tk, KindFactory, fmt.Sprintf("its commits are on %s, but closing it failed: %v", w.specBranch, err))
		return nil
	}
	r.mu.Lock()
	r.res.Closed = append(r.res.Closed, tk)
	r.mu.Unlock()
	r.line(tk, runlog.Line{Kind: runlog.Success, Text: "closed"})
	r.discard(w.b)
	if r.wasLast(tk, w.spec) {
		r.finishSpec(tk, w.spec, w.specBranch)
		r.release(ctx, tk.SpecID())
		r.specFinished(ctx, tk.SpecID(), w.specBranch, w.specDir)
	}
	return nil
}

// merge brings the Ticket branch up to date with its Spec branch and
// verifies the result. The merge commit is built without a worktree, or by
// the merge Step on a real conflict, and the Ticket branch moves to it, so
// that a failure leaves the Spec branch where it was: it only ever moves
// forward. It returns why the merge failed, or an error that stops the Run.
//
// A Spec branch that hasn't moved since the Ticket was cut from it needs
// no merge: the Ticket's tip is the result, and verify has passed on it.
func (r *run) merge(ctx context.Context, w *ticketWork) (failed *failure, stop error) {
	tk, b := w.tk, w.b
	tip, err := r.repo.Rev(ctx, w.specBranch)
	if err != nil {
		return mergeFailure(err), nil
	}
	if tip == b.base {
		return nil, nil
	}
	message := fmt.Sprintf("Merge %s: %s", tk.ID, tk.Title)
	var merged string
	// From the top of a worktree, so a conflict names files from the top.
	tree, err := (git.Repo{Dir: w.specDir}).MergeTree(ctx, tip, b.branch)
	var conflict *git.ConflictError
	switch {
	case errors.As(err, &conflict):
		if merged, failed, stop = r.resolve(ctx, w, tip, message, conflict); failed != nil || stop != nil {
			return failed, stop
		}
	case err != nil:
		return mergeFailure(err), nil
	default:
		if merged, err = r.repo.CommitTree(ctx, tree, message, tip, b.branch); err != nil {
			return mergeFailure(err), nil
		}
	}
	if err := (git.Repo{Dir: b.worktree}).ResetHard(ctx, merged); err != nil {
		return mergeFailure(err), nil
	}
	r.tlogf(tk, "merged %s", w.specBranch)
	return r.verifyMerged(ctx, b), nil
}

// verifyMerged runs verify on the merged result, in a Sandbox of its own:
// the Ticket's was removed when its build ended.
func (r *run) verifyMerged(ctx context.Context, b build) *failure {
	if len(r.opts.Verify) == 0 {
		return nil
	}
	sb, closeSandbox, failed := r.openSandbox(ctx, b.tk, "verify", b.worktree)
	if failed != nil {
		return failed
	}
	defer closeSandbox()
	b.sandbox = sb
	if f := r.verify(ctx, b); f != nil {
		return &failure{handBack: true, kind: KindVerify, reason: "verify on the merged result: " + f.Error(), verify: f}
	}
	return nil
}

// resolve has the merge Step resolve a real conflict, in a worktree of its
// own on the Spec branch's tip with the merge in progress. The worktree is
// detached, because the Spec branch is checked out in the Spec's worktree,
// and because a commit here must not move it before verify has passed. It
// returns the commit that resolves the merge.
func (r *run) resolve(ctx context.Context, w *ticketWork, tip, message string, conflict *git.ConflictError) (merged string, failed *failure, stop error) {
	tk, b := w.tk, w.b
	dir := filepath.Join(r.worktrees, "merge-"+tk.ID.Branch)
	if err := r.repo.AddDetachedWorktree(ctx, dir, tip); err != nil {
		return "", mergeFailure(err), nil
	}
	defer func() {
		if err := r.repo.RemoveWorktree(context.Background(), dir); err != nil {
			r.warnf("warning: %v", err)
		}
	}()
	sb, closeSandbox, failed := r.openSandbox(ctx, tk, "merge", dir)
	if failed != nil {
		return "", failed, nil
	}
	defer closeSandbox()
	repo, stepDir := git.Repo{Dir: dir}, filepath.Join(dir, r.rel)
	// The Hooks come first: they may install what the project's manifests
	// name, and those can be among the conflicting files.
	inMergeWorktree := b
	inMergeWorktree.dir, inMergeWorktree.sandbox = stepDir, sb
	if failed := r.sandboxCreated(ctx, inMergeWorktree); failed != nil {
		return "", failed, nil
	}
	if err := repo.StartMerge(ctx, b.branch, message); err != nil {
		return "", mergeFailure(err), nil
	}
	others, err := mergedTouching(ctx, repo, w, tip, conflict.Files)
	if err != nil {
		return "", mergeFailure(err), nil
	}
	c := b.prompt
	c.Spec = r.withClosed(c.Spec)
	m := prompt.Merging{SpecBranch: w.specBranch, Files: conflict.Files, Merged: others}
	r.line(tk, runlog.Line{Step: Merge, Text: fmt.Sprintf("started on %v", conflict)})
	out, failed, stop := r.runStep(ctx, Step{Name: Merge, Ticket: tk, Dir: stepDir, Sandbox: sb, Prompt: prompt.Merge(r.part(Merge), c, m), Log: r.stepLog(tk, Merge)})
	if failed != nil || stop != nil {
		return "", failed, stop
	}
	if out.Report.Outcome == harness.CannotDo {
		return "", unresolvedMerge(conflict, "reported cannot_do: "+firstNonEmpty(out.Report.Reason, out.Report.Summary)), nil
	}
	// The Ticket's commits have passed this check, and the Spec branch's are
	// the base of this diff, so whatever it finds is the merge Step's.
	if failed := r.rejectPlantedGit(dir); failed != nil {
		return "", failed, nil
	}
	changed, err := git.Repo{Dir: stepDir}.Changed(ctx, tip, "HEAD")
	if err != nil {
		return "", mergeFailure(err), nil
	}
	if failed := r.rejectProtected(Merge, changed); failed != nil {
		return "", failed, nil
	}
	why, err := unresolved(ctx, repo, tip, b.branch, conflict.Files)
	if err != nil {
		return "", mergeFailure(err), nil
	}
	if why != "" {
		return "", unresolvedMerge(conflict, why), nil
	}
	head, err := repo.Rev(ctx, "HEAD")
	if err != nil {
		return "", mergeFailure(err), nil
	}
	w.resolution = out.Report.Summary
	return head, nil, nil
}

// unresolved says what the merge Step left undone in repo, "" if the merge
// is resolved: a clean tree, HEAD holding both sides, and no conflict
// markers in the files that had conflicts.
func unresolved(ctx context.Context, repo git.Repo, tip, ticketBranch string, files []string) (string, error) {
	clean, err := repo.Clean(ctx)
	if err != nil {
		return "", err
	}
	if !clean {
		return "left uncommitted changes", nil
	}
	for _, side := range []string{tip, ticketBranch} {
		has, err := repo.Contains(ctx, "HEAD", side)
		if err != nil {
			return "", err
		}
		if !has {
			return "didn't commit the merge", nil
		}
	}
	marked, err := repo.ConflictMarkers(ctx, "HEAD", files)
	if err != nil {
		return "", err
	}
	if len(marked) > 0 {
		return "left conflict markers in " + strings.Join(marked, ", "), nil
	}
	return "", nil
}

// unresolvedMerge is a conflict the merge Step didn't resolve.
func unresolvedMerge(conflict *git.ConflictError, why string) *failure {
	return mergeFailure(fmt.Errorf("%w, and the merge Step %s", conflict, why))
}

// mergedTouching are the Tickets merged since this one started whose
// commits touched files: git log finds the commits, and the lane knows
// which Ticket each is from. repo must be at the top of a worktree, where
// files start.
func mergedTouching(ctx context.Context, repo git.Repo, w *ticketWork, tip string, files []string) ([]prompt.MergedTicket, error) {
	touching, err := repo.LogTouching(ctx, w.b.base, tip, files)
	if err != nil {
		return nil, err
	}
	touched := map[string]bool{}
	for _, c := range touching {
		touched[c.SHA] = true
	}
	var merged []prompt.MergedTicket
	for _, l := range w.lane.landed {
		var commits []git.Commit
		for _, c := range l.commits {
			if touched[c.SHA] {
				commits = append(commits, c)
			}
		}
		if len(commits) > 0 {
			merged = append(merged, prompt.MergedTicket{Ticket: l.tk, Commits: commits})
		}
	}
	return merged, nil
}

// mergeFailure is a merge that didn't work out.
func mergeFailure(err error) *failure {
	return &failure{handBack: true, kind: KindMerge, reason: "merge: " + err.Error()}
}

// mergeFailed discards the work. The first time, the Ticket goes back into
// the frontier, to be implemented afresh from the updated Spec branch by
// the next Iteration; the second time, it goes back to a person. A merge
// Step that changed a Protected path goes back to a person at once.
func (r *run) mergeFailed(ctx context.Context, w *ticketWork, f *failure) {
	if f.kind == KindProtectedPath {
		r.failBuild(ctx, w.b, f)
		return
	}
	r.mu.Lock()
	again := r.requeued[w.tk.ID.Key]
	r.requeued[w.tk.ID.Key] = true
	r.mu.Unlock()
	if again {
		f.reason += ", again after it was implemented afresh"
		r.failBuild(ctx, w.b, f)
		return
	}
	r.discard(w.b)
	r.setDone(w.tk, false)
	r.line(w.tk, runlog.Line{Kind: runlog.Warning, Text: f.reason + "; its work is discarded, and it is re-queued for the next Iteration"})
}

// stopSpec stops the Ticket's Spec for this Run, after a rejected push,
// and kills its Tickets in flight.
func (r *run) stopSpec(w *ticketWork) {
	r.mu.Lock()
	r.stoppedSpecs[w.tk.SpecID().Key] = true
	r.res.StoppedSpecs = append(r.res.StoppedSpecs, StoppedSpec{
		ID: w.tk.SpecID(), Branch: w.specBranch,
		Fix: fmt.Sprintf("bring your local branch up to date with `git fetch %s %s:%s`, then start a new Run", r.opts.Remote, w.specBranch, w.specBranch),
	})
	r.mu.Unlock()
	w.lane.kill()
}
