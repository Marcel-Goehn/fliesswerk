package factory

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Marcel-Goehn/kraftwerk/internal/git"
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

	failed := r.merge(ctx, w)
	if ctx.Err() != nil {
		return r.abandon(w)
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
// verifies the result. The merge commit is built without a worktree, and
// the Ticket branch moves to it, so that a failure leaves the Spec branch
// where it was: it only ever moves forward.
//
// A Spec branch that hasn't moved since the Ticket was cut from it needs
// no merge: the Ticket's tip is the result, and verify has passed on it.
func (r *run) merge(ctx context.Context, w *ticketWork) *failure {
	tk, b := w.tk, w.b
	tip, err := r.repo.Rev(ctx, w.specBranch)
	if err != nil {
		return mergeFailure(err)
	}
	if tip == b.base {
		return nil
	}
	tree, err := r.repo.MergeTree(ctx, tip, b.branch)
	if err != nil {
		return mergeFailure(err)
	}
	merged, err := r.repo.CommitTree(ctx, tree, fmt.Sprintf("Merge %s: %s", tk.ID, tk.Title), tip, b.branch)
	if err != nil {
		return mergeFailure(err)
	}
	if err := (git.Repo{Dir: b.worktree}).ResetHard(ctx, merged); err != nil {
		return mergeFailure(err)
	}
	r.tlogf(tk, "merged %s", w.specBranch)
	if f := r.verify(ctx, b); f != nil {
		return &failure{handBack: true, kind: KindVerify, reason: "verify on the merged result: " + f.Error(), verify: f}
	}
	return nil
}

// mergeFailure is a merge that didn't work out.
func mergeFailure(err error) *failure {
	return &failure{handBack: true, kind: KindMerge, reason: "merge: " + err.Error()}
}

// mergeFailed discards the work. The first time, the Ticket goes back into
// the frontier, to be implemented afresh from the updated Spec branch by
// the next Iteration; the second time, it goes back to a person.
func (r *run) mergeFailed(ctx context.Context, w *ticketWork, f *failure) {
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
