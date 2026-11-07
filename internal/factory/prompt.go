package factory

import (
	"fmt"
	"strings"

	"github.com/Marcel-Goehn/kraftwerk/internal/git"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker"
)

// implementPrompt is the minimal built-in implement prompt: the Ticket,
// its Spec, the rules and how to report.
func implementPrompt(tk tracker.Ticket, spec *tracker.Spec, branch string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Task\n\nImplement Ticket %s: %s. Commit your work on the branch `%s`, which is checked out in the current directory.\n\n", tk.ID, tk.Title, branch)
	fmt.Fprintf(&b, "## Ticket %s: %s\n\n%s\n\n", tk.ID, tk.Title, strings.TrimSpace(tk.Body))
	if spec != nil {
		fmt.Fprintf(&b, "## Spec %s: %s\n\n%s\n\n", spec.ID, spec.Title, strings.TrimSpace(spec.Body))
	}
	fmt.Fprintf(&b, `Do not close the Ticket. The Factory closes it after your commits are merged and pushed.

## Rules

- Commit on %s. Don't push, switch branches, rebase or rewrite history.
- Don't leave commented-out code or TODO comments.

## Done

When you are finished, report "done" with a short summary of what you changed. Report "cannot_do" with the reason only if the Ticket is ambiguous or lacks context, needs something from outside (a service, a credential, another team's change), has tests failing that you can't fix, or is already done. Anything else means try harder: nobody will answer questions.
`, "`"+branch+"`")
	return b.String()
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
