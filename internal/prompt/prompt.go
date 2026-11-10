// Package prompt assembles each Step's prompt: the Factory's fixed
// contract, which code fills in with everything the agent needs, wrapped
// around the project's editable part from .fliesswerk/prompts/<step>.md.
// The agent never fetches context itself.
package prompt

import (
	"fmt"
	"strings"

	"github.com/Marcel-Goehn/fliesswerk/internal/git"
	"github.com/Marcel-Goehn/fliesswerk/internal/sandbox"
	"github.com/Marcel-Goehn/fliesswerk/internal/tracker"
	"github.com/Marcel-Goehn/fliesswerk/internal/verify"
)

// Context is what every Step of a Ticket is told: one snapshot, taken
// before implement, so review sees what implement saw.
type Context struct {
	Ticket tracker.Ticket
	// Comments are the Ticket's comments, oldest first.
	Comments []tracker.Comment
	// Spec is the Ticket's Spec, nil for a standalone Ticket.
	Spec *tracker.Spec
	// InProgress holds, by ID.Key, the Spec's other Tickets being worked
	// on now.
	InProgress map[string]bool
	// Branch is the Ticket branch the Step commits on.
	Branch string
	// Protected are every Protected path.
	Protected []string
	// Commands are the Feedback commands `{{commands}}` lists, in order.
	Commands []verify.Command
	// Standards is the Coding standards file, "" for none. Only review
	// gets it.
	Standards string
	// Services are the Services the Step's Sandbox can reach.
	Services []sandbox.Service
}

// Recent is what implement is told about the commits before its own.
type Recent struct {
	BaseBranch string
	// OnSpecBranch reports that Commits are the Spec branch's commits
	// since BaseBranch. Without any, they are BaseBranch's last ones.
	OnSpecBranch bool
	// Commits are newest first.
	Commits []git.Message
}

// RecentCommits is how many of the base branch's commits implement gets
// when the Spec branch has none of its own yet.
const RecentCommits = 10

// RetryOutputLines is how many of a failed Feedback command's last lines
// a retried implement gets.
const RetryOutputLines = 100

// Implement is the implement prompt, around part, the project's editable
// part. A retry after a failed verify also gets the failing command's
// output.
func Implement(part string, c Context, recent Recent, failed *verify.Failure) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Task\n\nImplement Ticket %s: %s\n\nWork only on this Ticket. %s\n\n", c.Ticket.ID, c.Ticket.Title, provided(c.Spec))
	ticket(&b, c)
	spec(&b, c, "Your Ticket is part of Spec %s: %s. The Spec describes the whole feature. Use it to understand why your Ticket exists and how it fits in, but implement only what your Ticket asks for. The rest of the Spec is other Tickets' work.",
		"Closed Tickets are finished: don't redo them. In progress and open Tickets are someone else's work: don't do them, even if they look easy to include.")
	recentCommits(&b, recent, c.Spec != nil)
	if failed != nil {
		fence := CodeFence(failed.Output)
		fmt.Fprintf(&b, "# The Factory's checks failed\n\nThis is a retry. Your earlier commits are on the branch, but after them the Feedback command `%s` (`%s`) failed (%v). Its last lines:\n\n%s\n%s\n%s\n\nFix the cause, then commit.\n\n",
			failed.Command.Name, failed.Command.Line, failed.Err, fence, failed.Tail(RetryOutputLines), fence)
	}
	editable(&b, fill(part, implementStep, c))
	bottom(&b, c, "The Factory closes it after your work has been merged.", onBranch(c))
	b.WriteString(`# Done

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

// Review is the review prompt, around part, of the change made on top
// of the base commit.
func Review(part string, c Context, base string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Task\n\nReview the code changes for Ticket %s: %s. Check that they do what the Ticket asks, and improve how the code is written. Fix what you find yourself and commit it.\n\nThe Ticket's %s\n\n",
		c.Ticket.ID, c.Ticket.Title, strings.TrimPrefix(provided(c.Spec), "Its "))
	ticket(&b, c)
	spec(&b, c, "The Ticket is part of Spec %s: %s. The Spec describes the whole feature. Use it to judge whether the change fits, but review only what this Ticket asks for. The rest of the Spec is other Tickets' work.",
		"Closed Tickets are finished. In progress and open Tickets are someone else's work: don't add them, even if they look easy to include.")
	fmt.Fprintf(&b, "# What changed\n\nThe work to review is every commit on this branch since `%s`. Read it with:\n\n- `git log %s..HEAD`\n- `git diff %s..HEAD`\n\n", base, base, base)
	editable(&b, fill(part, reviewStep, c))
	bottom(&b, c, "The Factory closes it after the change has been merged.", onBranch(c))
	b.WriteString(`# Done

When you are finished, report "done" with a short summary of what you found and fixed, or that the change is fine as it is.
`)
	return b.String()
}

// Merging is what only the merge Step is told.
type Merging struct {
	// SpecBranch is the branch the Ticket is being merged into.
	SpecBranch string
	// Files are the conflicting files.
	Files []string
	// Merged are the Tickets merged since this one started whose commits
	// touched Files, in the order they landed.
	Merged []MergedTicket
}

// MergedTicket is a Ticket on the other side of a conflict.
type MergedTicket struct {
	Ticket tracker.Ticket
	// Commits are its commits that touched the conflicting files.
	Commits []git.Commit
}

// Merge is the merge prompt, around part.
func Merge(part string, c Context, m Merging) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Task\n\nResolve the merge of Ticket %s: %s\n\nThis Ticket's work is being merged into `%s`, and other Tickets changed the same files after it started. The merge is in progress in this worktree and has stopped on its conflicts. The Ticket's %s\n\n",
		c.Ticket.ID, c.Ticket.Title, m.SpecBranch, strings.TrimPrefix(provided(c.Spec), "Its "))
	ticket(&b, c)
	spec(&b, c, "The Ticket is part of Spec %s: %s. The Spec describes the whole feature. Use it to understand what each side of the conflict is for.",
		"Resolve only this conflict. The other Tickets' work is not yours to add or change.")
	b.WriteString("# Conflicting files\n\nTheir paths start at the top of the repository.\n\n")
	for _, f := range m.Files {
		fmt.Fprintf(&b, "- `%s`\n", f)
	}
	b.WriteString("\n")
	if len(m.Merged) > 0 {
		b.WriteString("# Tickets merged since this Ticket started\n\nThese Tickets' commits touched the conflicting files. They are the other side of the conflict.\n\n")
	}
	for _, t := range m.Merged {
		fmt.Fprintf(&b, "## %s %s\n\n<ticket>\n%s\n</ticket>\n\n", t.Ticket.ID, t.Ticket.Title, strings.TrimSpace(t.Ticket.Body))
		for _, cm := range t.Commits {
			fmt.Fprintf(&b, "- %s %s\n", cm.Short(), cm.Subject)
		}
		b.WriteString("\n")
	}
	editable(&b, fill(part, mergeStep, c))
	bottom(&b, c, "The Factory closes it after the merge has been verified and pushed.",
		fmt.Sprintf("You are in a worktree made for this merge, at the tip of `%s`, on no branch. Commit the merge here. Don't push, switch branches, rebase, rewrite existing commits or abort the merge.", m.SpecBranch))
	b.WriteString(`# Done

When the merge is committed and nothing is left uncommitted, report "done" with a short summary of how you resolved it.

Report "cannot_do", with the reason, only if the two sides can't both be kept, or the merged code fails and you can't fix it. The Ticket is then implemented afresh on top of the other Tickets' work.
`)
	return b.String()
}

// provided says what the prompt holds, so the agent doesn't fetch it.
func provided(s *tracker.Spec) string {
	if s == nil {
		return "Its full text and its comments are below, so you don't need to fetch them."
	}
	return "Its full text, its comments and the Spec it belongs to are below, so you don't need to fetch them."
}

// ticket writes the Ticket's body and its comments.
func ticket(b *strings.Builder, c Context) {
	fmt.Fprintf(b, "<ticket>\n%s\n</ticket>\n\n", strings.TrimSpace(c.Ticket.Body))
	if len(c.Comments) == 0 {
		return
	}
	b.WriteString("<ticket-comments>\n")
	for _, cm := range c.Comments {
		fmt.Fprintf(b, "<comment author=%q date=%q>\n%s\n</comment>\n", cm.Author, cm.CreatedAt.UTC().Format("2006-01-02 15:04 UTC"), strings.TrimSpace(cm.Body))
	}
	b.WriteString("</ticket-comments>\n\n")
}

// spec writes the Spec section, with intro (formatted with the Spec's ID
// and title) before its body and outro after its Ticket list. A
// standalone Ticket has none.
func spec(b *strings.Builder, c Context, intro, outro string) {
	s := c.Spec
	if s == nil {
		return
	}
	fmt.Fprintf(b, "# The Spec\n\n"+intro+"\n\n<spec>\n%s\n</spec>\n\n## Tickets in this Spec\n\n", s.ID, s.Title, strings.TrimSpace(s.Body))
	for _, tk := range s.Tickets {
		fmt.Fprintf(b, "- %s %s (%s)\n", tk.ID, tk.Title, mark(tk, c))
	}
	fmt.Fprintf(b, "\n%s\n\n", outro)
}

// mark is how the Spec's Ticket list marks tk.
func mark(tk tracker.SpecTicket, c Context) string {
	switch {
	case tk.ID.Key == c.Ticket.ID.Key:
		return "this Ticket"
	case tk.State == tracker.Closed:
		return "closed"
	case c.InProgress[tk.ID.Key]:
		return "in progress"
	}
	return "open"
}

// recentCommits writes the commits implement builds on. A standalone
// Ticket's own branch takes the Spec branch's place.
func recentCommits(b *strings.Builder, r Recent, inSpec bool) {
	b.WriteString("# Recent commits\n\n")
	switch {
	case r.OnSpecBranch && inSpec:
		fmt.Fprintf(b, "The commits on the Spec branch since `%s`, newest first:\n\n", r.BaseBranch)
	case r.OnSpecBranch:
		fmt.Fprintf(b, "The commits made for this Ticket since `%s`, newest first:\n\n", r.BaseBranch)
	case inSpec:
		fmt.Fprintf(b, "Nothing has been committed for this Spec yet. The last commits of `%s`, newest first:\n\n", r.BaseBranch)
	default:
		fmt.Fprintf(b, "The last commits of `%s`, newest first:\n\n", r.BaseBranch)
	}
	b.WriteString("<recent-commits>\n")
	for i, m := range r.Commits {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(b, "commit %s\n%s\n", m.Short(), m.Text)
	}
	b.WriteString("</recent-commits>\n\n")
}

// editable writes the project's part.
func editable(b *strings.Builder, part string) {
	if part = strings.TrimSpace(part); part != "" {
		b.WriteString(part + "\n\n")
	}
}

// onBranch is the Rule that says where a Step on the Ticket branch works.
func onBranch(c Context) string {
	return fmt.Sprintf("You are on the branch `%s`, in a worktree made for this Ticket. Commit your work here. Don't push, switch branches, rebase or rewrite existing commits.", c.Branch)
}

// services writes where the Step can reach its Sandbox's Services.
func services(b *strings.Builder, c Context) {
	if len(c.Services) == 0 {
		return
	}
	b.WriteString("# Services\n\nThese Services run for this Sandbox alone, with fresh data, and are ready now:\n\n")
	for _, s := range c.Services {
		quoted := make([]string, len(s.Addresses))
		for i, a := range s.Addresses {
			quoted[i] = "`" + a + "`"
		}
		fmt.Fprintf(b, "- %s: %s\n", s.Name, strings.Join(quoted, ", "))
	}
	b.WriteString("\nEach one's host and first port are also in `FLIESSWERK_SERVICE_<NAME>_HOST` and `FLIESSWERK_SERVICE_<NAME>_PORT`.\n\n")
}

// bottom writes the fixed bottom's reminder and Rules; closes says when
// the Factory closes the Ticket, and whereRule is the Rule on where the
// Step works.
func bottom(b *strings.Builder, c Context, closes, whereRule string) {
	services(b, c)
	quoted := make([]string, len(c.Protected))
	for i, p := range c.Protected {
		quoted[i] = "`" + p + "`"
	}
	fmt.Fprintf(b, `# Do not close the Ticket

%s

# Rules

- %s
- Never change these Protected paths: %s. Work that changes them is rejected, and the Ticket goes back to a person.
- Do not leave commented-out code or TODO comments in committed code.

`, closes, whereRule, strings.Join(quoted, ", "))
}

// CodeFence is a run of backticks longer than any in text, so text can't
// end the block it is fenced in.
func CodeFence(text string) string {
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
