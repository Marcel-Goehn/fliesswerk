package factory

import (
	"fmt"
	"strings"

	"github.com/Marcel-Goehn/kraftwerk/internal/git"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker"
	"github.com/Marcel-Goehn/kraftwerk/internal/verify"
)

// implementPrompt is the minimal built-in implement prompt: the Ticket,
// its Spec, the rules and how to report. A retry after a failed verify
// also gets the failing command's output.
func implementPrompt(tk tracker.Ticket, spec *tracker.Spec, branch string, protected []string, failed *verify.Failure) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Task\n\nImplement Ticket %s: %s. Commit your work on the branch `%s`, which is checked out in the current directory.\n\n", tk.ID, tk.Title, branch)
	ticketAndSpec(&b, tk, spec)
	if failed != nil {
		fence := codeFence(failed.Output)
		fmt.Fprintf(&b, "## The Factory's checks failed\n\nThis is a retry. Your earlier commits are on the branch, but after them the Feedback command `%s` (`%s`) failed (%v). Its last lines:\n\n%s\n%s\n%s\n\nFix the cause, then commit.\n\n",
			failed.Command.Name, failed.Command.Line, failed.Err, fence, failed.Tail(retryOutputLines), fence)
	}
	rules(&b, branch, protected)
	b.WriteString(`## Done

When you are finished, report "done" with a short summary of what you changed.

Report "cannot_do", with the reason, only if:

1. the Ticket is ambiguous, or context is missing
2. it needs a change to a Protected path
3. it needs something from outside: a service, a credential, or another team's change
4. tests fail and you can't fix them, including failures that were already there
5. the Ticket is already done or no longer makes sense

Anything else means try harder: nobody will answer questions.
`)
	return b.String()
}

// rules writes the fixed bottom's reminder and Rules.
func rules(b *strings.Builder, branch string, protected []string) {
	quoted := make([]string, len(protected))
	for i, p := range protected {
		quoted[i] = "`" + p + "`"
	}
	fmt.Fprintf(b, `Do not close the Ticket. The Factory closes it after your commits are merged and pushed.

## Rules

- Commit on %s. Don't push, switch branches, rebase or rewrite history.
- Don't change the Protected paths: %s. A commit that does is rejected, and the Ticket goes back to a person.
- Don't leave commented-out code or TODO comments.

`, "`"+branch+"`", strings.Join(quoted, ", "))
}

// retryOutputLines is how many of a failed Feedback command's last lines
// a retried implement gets.
const retryOutputLines = 100

// codeFence is a run of backticks longer than any in text, so text can't
// end the block it is fenced in.
func codeFence(text string) string {
	longest, run := 0, 0
	for _, c := range text {
		if c == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}

// reviewPrompt is the minimal built-in review prompt: the Ticket, its
// Spec, what changed since base, the rules and how to report.
func reviewPrompt(tk tracker.Ticket, spec *tracker.Spec, branch, base string, protected []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Task\n\nReview the change made for Ticket %s: %s, with fresh eyes. Check that it does what the Ticket asks and that it is well written. Fix what you find and commit the fixes on the branch `%s`, which is checked out in the current directory. If it is fine as it is, commit nothing.\n\n", tk.ID, tk.Title, branch)
	ticketAndSpec(&b, tk, spec)
	fmt.Fprintf(&b, "## What changed\n\nThe change starts at the base commit `%s`. See it with `git log %s..HEAD` and `git diff %s..HEAD`. Review only this change.\n\n", base, base, base)
	rules(&b, branch, protected)
	b.WriteString(`## Done

When you are finished, report "done" with a short summary of what you found and fixed, or that the change is fine as it is.
`)
	return b.String()
}

// ticketAndSpec writes the Ticket's and its Spec's sections.
func ticketAndSpec(b *strings.Builder, tk tracker.Ticket, spec *tracker.Spec) {
	fmt.Fprintf(b, "## Ticket %s: %s\n\n%s\n\n", tk.ID, tk.Title, strings.TrimSpace(tk.Body))
	if spec != nil {
		fmt.Fprintf(b, "## Spec %s: %s\n\n%s\n\n", spec.ID, spec.Title, strings.TrimSpace(spec.Body))
	}
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
