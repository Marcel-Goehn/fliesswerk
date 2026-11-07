package factory

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/Marcel-Goehn/kraftwerk/internal/prompt"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker"
	"github.com/Marcel-Goehn/kraftwerk/internal/verify"
)

// failure is why a Ticket didn't make it.
type failure struct {
	reason string
	// handBack means the Ticket goes back to a person: the ready label
	// gives way to the cannot_do label, a comment says why, and the work
	// is discarded. Otherwise the Factory itself failed, and the Ticket is
	// left as it was.
	handBack bool
	// cannotDo means the agent reported cannot_do, with reason.
	cannotDo bool
	// protected are the Protected paths the agent changed, if that is
	// why.
	protected []string
	// verify is the Feedback command that failed, if that is why.
	verify *verify.Failure
}

// factoryFailure is a failure of the Factory's own, not the Ticket's.
func factoryFailure(err error) *failure {
	return &failure{reason: err.Error()}
}

// failBuild ends a Ticket whose build failed.
func (r *run) failBuild(ctx context.Context, b build, f *failure) {
	if !f.handBack {
		r.fail(b.tk, f.reason)
		return
	}
	tk := b.tk
	// The comment is made first, so its excerpt doesn't end in the reason
	// it already gives.
	comment := r.failureComment(tk, f)
	r.fail(tk, f.reason)
	if err := r.opts.Tracker.Comment(ctx, tk.ID, comment); err != nil {
		r.tlogf(tk, "warning: commenting failed: %v", err)
	}
	if err := r.opts.Tracker.SetLabels(ctx, tk.ID, []string{r.opts.CannotDoLabel}, []string{r.opts.ReadyLabel}); err != nil {
		r.tlogf(tk, "warning: swapping %s for %s failed: %v", r.opts.ReadyLabel, r.opts.CannotDoLabel, err)
	} else {
		r.tlogf(tk, "handed back with %s", r.opts.CannotDoLabel)
	}
	r.discard(ctx, b)
}

// LogDir is where a Run's logs go, relative to the project root, with
// forward slashes.
func LogDir(runID string) string {
	return factoryDir + "logs/" + runID
}

// discard removes the Ticket's worktree and branch.
func (r *run) discard(ctx context.Context, b build) {
	if err := r.repo.RemoveWorktree(ctx, b.worktree); err != nil {
		r.logf("warning: %v", err)
	}
	if err := r.repo.DeleteBranch(ctx, b.branch); err != nil {
		r.logf("warning: %v", err)
	}
}

// failureComment is the comment on a Ticket handed back to a person.
func (r *run) failureComment(tk tracker.Ticket, f *failure) string {
	var b strings.Builder
	if f.cannotDo {
		fmt.Fprintf(&b, "The agent can't do this Ticket: %s\n\n", strings.TrimSpace(f.reason))
	} else {
		fmt.Fprintf(&b, "The Factory gave up on this Ticket: %s\n\n", strings.TrimSpace(f.reason))
	}
	if len(f.protected) > 0 {
		b.WriteString("Protected paths changed:\n\n")
		for _, p := range f.protected {
			fmt.Fprintf(&b, "- `%s`\n", p)
		}
		b.WriteString("\n")
	}
	r.writeExcerpt(&b, tk, f)
	fmt.Fprintf(&b, "Run `%s`. Its full log is in `%s` on the machine that ran it.\n\n", r.opts.RunID, LogDir(r.opts.RunID))
	fmt.Fprintf(&b, "The label `%s` replaced `%s`, and the work was discarded. Once the Ticket is fixed, put `%s` back.\n",
		r.opts.CannotDoLabel, r.opts.ReadyLabel, r.opts.ReadyLabel)
	return b.String()
}

// Excerpt limits.
const (
	// excerptLines is how many of the last lines a failure comment shows.
	excerptLines = 30
	// excerptWidth is how many characters of each line it shows.
	excerptWidth = 200
)

// writeExcerpt writes the failure comment's collapsed excerpt: the
// Ticket's last rendered lines, or the failing Feedback command's last
// lines, each redacted and cut. A final scan of the whole excerpt drops
// it if a secret got through anyway, such as one spanning lines.
func (r *run) writeExcerpt(b *strings.Builder, tk tracker.Ticket, f *failure) {
	summary, lines := "The last lines of the log", r.ticketLog(tk).last()
	if f.verify != nil {
		summary = fmt.Sprintf("The last lines of `%s`", f.verify.Command.Name)
		lines = strings.Split(f.verify.Tail(excerptLines), "\n")
	}
	if len(lines) == 0 {
		return
	}
	cut := make([]string, len(lines))
	for i, l := range lines {
		cut[i] = cutRunes(r.redact(strings.TrimRight(l, "\r")), excerptWidth)
	}
	text := strings.Join(cut, "\n")
	if r.opts.Redactor != nil && r.opts.Redactor.Leaks(text) {
		b.WriteString("The log excerpt was left out: a secret survived its redaction.\n\n")
		return
	}
	fence := prompt.CodeFence(text)
	fmt.Fprintf(b, "<details><summary>%s</summary>\n\n%s\n%s\n%s\n\n</details>\n\n", summary, fence, text, fence)
}

// redact is s with every known secret masked.
func (r *run) redact(s string) string {
	if r.opts.Redactor == nil {
		return s
	}
	return r.opts.Redactor.String(s)
}

// cutRunes is s cut to at most n characters.
func cutRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// lastLines keeps the last excerptLines lines added. It is safe for
// concurrent use, since a Step logs from its reading goroutines.
type lastLines struct {
	mu    sync.Mutex
	lines []string
}

// add keeps line, dropping the oldest beyond excerptLines.
func (l *lastLines) add(line string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, line)
	if len(l.lines) > excerptLines {
		l.lines = slices.Delete(l.lines, 0, len(l.lines)-excerptLines)
	}
}

// last is the lines kept, oldest first.
func (l *lastLines) last() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.lines)
}
