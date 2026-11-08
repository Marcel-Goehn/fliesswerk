package factory

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/Marcel-Goehn/kraftwerk/internal/git"
	"github.com/Marcel-Goehn/kraftwerk/internal/prompt"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker"
)

// promptContext is what every Step of the Ticket is told, read once
// before implement: its comments and its Spec, whose Ticket list counts
// the Tickets this Run closed as closed. working holds, by ID.Key, the
// Tickets of this Iteration, which are in progress.
func (r *run) promptContext(ctx context.Context, tk tracker.Ticket, spec *tracker.Spec, branch string, working map[string]bool) (prompt.Context, error) {
	comments, err := r.opts.Tracker.Comments(ctx, tk.ID)
	if err != nil {
		return prompt.Context{}, fmt.Errorf("reading its comments: %w", err)
	}
	return prompt.Context{
		Ticket: tk, Comments: comments, Spec: r.withClosed(spec), InProgress: working,
		Branch: branch, Protected: r.protectedPaths(), Commands: r.opts.Commands, Standards: r.opts.Standards,
	}, nil
}

// withClosed is spec, with the Tickets this Run closed counted as closed.
func (r *run) withClosed(spec *tracker.Spec) *tracker.Spec {
	if spec == nil {
		return nil
	}
	s := *spec
	s.Tickets = slices.Clone(spec.Tickets)
	for i, st := range s.Tickets {
		if r.closed(st.ID) {
			s.Tickets[i].State = tracker.Closed
		}
	}
	return &s
}

// recentCommits are the Spec branch's commits since the base branch, or,
// while it has none, the base branch's last ones.
func (r *run) recentCommits(ctx context.Context, specTip string) (prompt.Recent, error) {
	base := r.opts.Remote + "/" + r.opts.BaseBranch
	recent := prompt.Recent{BaseBranch: r.opts.BaseBranch, OnSpecBranch: true}
	var err error
	if recent.Commits, err = r.repo.Messages(ctx, base+".."+specTip); err != nil || len(recent.Commits) > 0 {
		return recent, err
	}
	recent.OnSpecBranch = false
	recent.Commits, err = r.repo.Messages(ctx, "-n", strconv.Itoa(prompt.RecentCommits), base)
	return recent, err
}

// part is the project's editable part of step's prompt, or the built-in
// one.
func (r *run) part(step string) string {
	if p, ok := r.opts.Prompts[step]; ok {
		return p.Text
	}
	return prompt.Builtin(step, prompt.Latest(step))
}

// closeComment is the last comment on a Ticket the Factory closes.
func closeComment(summary string, commits []git.Commit, branch string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\nCommits on `%s`:\n\n", strings.TrimSpace(summary), branch)
	for _, c := range commits {
		fmt.Fprintf(&b, "- %s %s\n", c.Short(), c.Subject)
	}
	fmt.Fprintf(&b, "\nClosed by the Factory after pushing `%s`.\n", branch)
	return b.String()
}

// maxSlug bounds a slug's length, so branch names stay readable.
const maxSlug = 40

// slug turns a title into the branch-safe slug of a branch name: lower
// case ASCII letters and digits, runs of anything else as one dash, cut
// at a word boundary. A title with nothing usable gives fallback.
func slug(title, fallback string) string {
	var b strings.Builder
	dash := false
	for _, c := range strings.ToLower(title) {
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(c)
			dash = false
			continue
		}
		dash = true
	}
	s := b.String()
	if len(s) > maxSlug {
		s = s[:maxSlug]
		if i := strings.LastIndexByte(s, '-'); i > 0 {
			s = s[:i]
		}
	}
	if s == "" {
		return fallback
	}
	return s
}
