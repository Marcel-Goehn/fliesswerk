package factory

import (
	"context"
	"fmt"
	"strings"

	"github.com/Marcel-Goehn/fliesswerk/internal/tracker"
)

// PullRequestSetting is whether, and how, a finished Spec gets a pull
// request: `[pull_request]` in the config.
type PullRequestSetting struct {
	// Open opens a pull request from the Spec branch into BaseBranch, or
	// reuses the open one.
	Open bool
	// Draft opens it as a draft.
	Draft bool
}

// wasLast reports whether the Ticket that just closed was its Spec's last;
// a standalone Ticket's closing is its last.
func (r *run) wasLast(tk tracker.Ticket, spec *tracker.Spec) bool {
	if spec == nil {
		return tk.Parent == nil // without the Spec's Ticket list, it can't be known
	}
	for _, st := range spec.Tickets {
		if st.State == tracker.Open && !r.closed(st.ID) {
			return false
		}
	}
	return true
}

// finishSpec finishes the Spec (or standalone Ticket) whose last Ticket w
// just landed: its pull request is opened or found, its Claim released,
// and then the spec_finished Hooks run, so they find the pull request.
func (r *run) finishSpec(ctx context.Context, w *ticketWork) {
	id := w.tk.SpecID()
	f := FinishedSpec{ID: id, Title: w.tk.Title, Branch: w.specBranch}
	if w.spec != nil {
		f.Title = w.spec.Title
	}
	if r.opts.PullRequest.Open {
		f.PullRequest = r.pullRequest(ctx, w, f)
	}
	r.release(ctx, id)
	r.specFinished(ctx, id, w.specBranch, w.specDir)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.res.SpecsFinished = append(r.res.SpecsFinished, f)
}

// pullRequest reuses the open pull request of the finished Spec's branch
// as it is, or opens one into the base branch. It returns its URL, "" if
// there is none.
func (r *run) pullRequest(ctx context.Context, w *ticketWork, f FinishedSpec) string {
	url, err := r.opts.Tracker.FindPullRequest(ctx, f.Branch)
	if err != nil {
		r.warnf("%s: warning: its last Ticket landed, but looking up its pull request failed: %v", f.ID, err)
		return ""
	}
	if url != "" {
		r.logf("%s: reused the open pull request %s", f.ID, url)
		return url
	}
	url, err = r.opts.Tracker.OpenPullRequest(ctx, tracker.PullRequest{
		Head: f.Branch, Base: r.opts.BaseBranch, Title: f.Title,
		Body: r.pullRequestBody(ctx, w, f), Draft: r.opts.PullRequest.Draft,
	})
	if err != nil {
		r.warnf("%s: warning: its last Ticket landed, but opening its pull request failed: %v", f.ID, err)
		return ""
	}
	r.logf("%s: opened the pull request %s", f.ID, url)
	return url
}

// pullRequestBody is a short header, `Closes` for the Spec, and a line
// for each of its closed Tickets with what the agent did. A standalone
// Ticket's body has just its own summary.
func (r *run) pullRequestBody(ctx context.Context, w *ticketWork, f FinishedSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Opened by Fliesswerk: the work on %s, from `%s`.\n\nCloses %s\n", f.ID, f.Branch, f.ID)
	if w.spec == nil {
		if s := r.summaryOf(ctx, w.tk.ID); s != "" {
			fmt.Fprintf(&b, "\n%s\n", s)
		}
		return b.String()
	}
	b.WriteString("\nTickets:\n\n")
	for _, st := range w.spec.Tickets {
		if st.State != tracker.Closed && !r.closed(st.ID) {
			continue
		}
		line := st.ID.Display + " " + st.Title
		if s := r.summaryOf(ctx, st.ID); s != "" {
			line += ": " + s
		}
		fmt.Fprintf(&b, "- %s\n", line)
	}
	return b.String()
}

// summaryOf is the summary, on one line, the Factory closed the Ticket
// with: from this Run, or else from its last close comment. It is "" for
// a Ticket the Factory didn't close.
func (r *run) summaryOf(ctx context.Context, id tracker.ID) string {
	r.mu.Lock()
	s, ok := r.summaries[id.Key]
	r.mu.Unlock()
	if !ok {
		comments, err := r.opts.Tracker.Comments(ctx, id)
		if err != nil {
			r.warnf("%s: warning: its summary is left out of the pull request, as reading its comments failed: %v", id, err)
		}
		for _, c := range comments {
			if cs, ok := summaryIn(c.Body); ok {
				s = cs
			}
		}
	}
	return strings.Join(strings.Fields(s), " ")
}

// release releases the Claim on a finished Spec. Its last Ticket is closed
// already, so a failed release only warns.
func (r *run) release(ctx context.Context, id tracker.ID) {
	if err := r.opts.Tracker.Release(ctx, id, r.user); err != nil {
		r.warnf("%s: warning: its last Ticket closed, but releasing its claim failed: %v", id, err)
		return
	}
	r.logf("%s: released (its last Ticket closed)", id)
}
