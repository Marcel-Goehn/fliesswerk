// Package prompt renders each Step's prompt from its template,
// templates/<step>.md, which holds the whole prompt. Go only prepares the
// data the template words. Every project gets the same prompts. The agent
// never fetches context itself.
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
	// Commands are the Feedback commands the prompt lists, in order.
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

// Implement is the implement prompt. A retry after a failed verify also gets the failing command's
// output.
func Implement(c Context, recent Recent, failed *verify.Failure) string {
	d := implementData{view: viewOf(c), Recent: recentOf(recent)}
	if failed != nil {
		d.Failed = &failedView{
			Name:  failed.Command.Name,
			Line:  failed.Command.Line,
			Err:   fmt.Sprint(failed.Err),
			Fence: CodeFence(failed.Output),
			Tail:  failed.Tail(RetryOutputLines),
		}
	}
	return mustRender(implementStep, d)
}

// Review is the review prompt of the change made on top
// of the base commit.
func Review(c Context, base string) string {
	return mustRender(reviewStep, reviewData{view: viewOf(c), Standards: c.Standards, Base: base})
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

// Merge is the merge prompt.
func Merge(c Context, m Merging) string {
	d := mergeData{view: viewOf(c), SpecBranch: m.SpecBranch, Files: m.Files}
	for _, t := range m.Merged {
		mt := mergedView{ID: t.Ticket.ID.String(), Title: t.Ticket.Title, Body: strings.TrimSpace(t.Ticket.Body)}
		for _, cm := range t.Commits {
			mt.Commits = append(mt.Commits, mergedCommit{Short: cm.Short(), Subject: cm.Subject})
		}
		d.Merged = append(d.Merged, mt)
	}
	return mustRender(mergeStep, d)
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
