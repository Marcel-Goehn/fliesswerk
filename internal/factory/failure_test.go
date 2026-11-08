package factory_test

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Marcel-Goehn/kraftwerk/internal/factory"
	"github.com/Marcel-Goehn/kraftwerk/internal/harness"
	"github.com/Marcel-Goehn/kraftwerk/internal/redact"
	"github.com/Marcel-Goehn/kraftwerk/internal/runlog"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker/fake"
	"github.com/Marcel-Goehn/kraftwerk/internal/verify"
)

const investigate = "needs-investigation"

// failOptions are options with the failure settings a Run needs.
func failOptions(f fixture, tr *fake.Tracker, runStep func(context.Context, factory.Step) harness.Outcome) factory.Options {
	opts := options(f, tr, runStep)
	opts.CannotDoLabel = investigate
	opts.RunID = "2026-11-07T14-03-12"
	return opts
}

// issue is the fake Tracker's issue n as it is now.
func issue(t *testing.T, tr *fake.Tracker, n int) fake.Issue {
	t.Helper()
	for _, i := range tr.Issues() {
		if i.Number == n {
			return i
		}
	}
	t.Fatalf("no issue #%d", n)
	return fake.Issue{}
}

// handedBack checks that Ticket n came back to a person: open, the ready
// label swapped for the cannot_do label, and one comment holding each of
// want. It returns the comment.
func handedBack(t *testing.T, tr *fake.Tracker, n int, want ...string) string {
	t.Helper()
	i := issue(t, tr, n)
	if i.Closed {
		t.Errorf("#%d was closed", n)
	}
	if slices.Contains(i.Labels, ready) || !slices.Contains(i.Labels, investigate) {
		t.Errorf("#%d labels = %v, want %s instead of %s", n, i.Labels, investigate, ready)
	}
	if len(i.Comments) != 1 {
		t.Fatalf("#%d has %d comments, want one", n, len(i.Comments))
	}
	c := i.Comments[0].Body
	for _, w := range append(want, "2026-11-07T14-03-12", ".kraftwerk/logs/2026-11-07T14-03-12") {
		if !strings.Contains(c, w) {
			t.Errorf("#%d's comment lacks %q:\n%s", n, w, c)
		}
	}
	return c
}

func TestCannotDoHandsTheTicketBackAndTheSpecCarriesOn(t *testing.T) {
	f := newFixture(t)
	issues := append(cartSpec(),
		fake.Issue{Number: 3, Title: "Cart totals", Labels: []string{ready}, Parent: 1, BlockedBy: []int{2}},
		fake.Issue{Number: 4, Title: "Cart page", Labels: []string{ready}, Parent: 1})
	tr := fake.New(ready, issues...)
	calls := map[string]int{}
	implement := func(ctx context.Context, s factory.Step) harness.Outcome {
		calls[s.Ticket.ID.Display]++
		if s.Ticket.ID.Display == "#2" {
			return harness.Outcome{Kind: harness.Reported, Report: harness.Report{Outcome: harness.CannotDo, Reason: "The Ticket doesn't say which currency.", Summary: "Stopped."}}
		}
		return commits(map[string]string{"page.go": "package page\n"}, "Added the page.")(ctx, s)
	}

	res := factory.Run(context.Background(), failOptions(f, tr, implement))

	handedBack(t, tr, 2, "The Ticket doesn't say which currency.")
	if calls["#2"] != 1 {
		t.Errorf("implement ran %d times on #2, want once: cannot_do isn't retried", calls["#2"])
	}
	if !issue(t, tr, 4).Closed {
		t.Error("#4 wasn't closed: the rest of the Spec should carry on")
	}
	if calls["#3"] != 0 || issue(t, tr, 3).Closed {
		t.Error("#3 was worked on, but its Blocker #2 is still open")
	}
	if len(res.Failed) != 1 || res.Stopped != nil {
		t.Errorf("result = %+v, want #2 failed and the Run not stopped", res)
	}
	if gitOK(f.work, "rev-parse", "--verify", "-q", "refs/heads/kraftwerk/ticket-2") {
		t.Error("the work of #2 wasn't discarded: its Ticket branch is left")
	}
}

// sequence is a Step that returns each outcome in turn, then runs last
// for every call after them, recording where each call ran.
type sequence struct {
	outcomes []harness.Outcome
	last     func(context.Context, factory.Step) harness.Outcome
	dirs     []string
}

func (q *sequence) run(ctx context.Context, s factory.Step) harness.Outcome {
	q.dirs = append(q.dirs, s.Dir)
	if n := len(q.dirs); n <= len(q.outcomes) {
		return q.outcomes[n-1]
	}
	return q.last(ctx, s)
}

var (
	errOther    = harness.Outcome{Kind: harness.Failed, Class: harness.Other, Message: "scripted failure"}
	noReport    = harness.Outcome{Kind: harness.NoReport}
	idleTimeout = harness.Outcome{Kind: harness.Failed, Class: harness.Other, Message: "no output for 10m0s (idle timeout)"}
	stepTimeout = harness.Outcome{Kind: harness.Failed, Class: harness.Other, Message: "still running after 1h0m0s (Step timeout)"}
	transient   = harness.Outcome{Kind: harness.Failed, Class: harness.Transient, Message: "Overloaded"}
)

func TestAFailedStepGetsOneFreshRetryInTheSameWorktree(t *testing.T) {
	for name, failed := range map[string]harness.Outcome{
		"error(other)": errOther, "no_report": noReport, "idle timeout": idleTimeout, "Step timeout": stepTimeout,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			tr := fake.New(ready, cartSpec()...)
			q := &sequence{outcomes: []harness.Outcome{failed}, last: commits(map[string]string{"cart.go": "package cart\n"}, "Added the cart.")}

			res := factory.Run(context.Background(), failOptions(f, tr, q.run))

			if len(res.Closed) != 1 || len(res.Failed) != 0 {
				t.Fatalf("result = %+v, want #2 closed after the retry", res)
			}
			if len(q.dirs) != 2 || q.dirs[0] != q.dirs[1] {
				t.Errorf("implement ran in %q, want twice in the same worktree", q.dirs)
			}
		})
	}
}

func TestAStepFailingAgainAfterItsRetryHandsTheTicketBack(t *testing.T) {
	for name, failed := range map[string]harness.Outcome{
		"error(other)": errOther, "no_report": noReport, "idle timeout": idleTimeout, "Step timeout": stepTimeout,
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			tr := fake.New(ready, cartSpec()...)
			q := &sequence{outcomes: []harness.Outcome{failed, failed, failed}}

			res := factory.Run(context.Background(), failOptions(f, tr, q.run))

			if len(q.dirs) != 2 {
				t.Errorf("implement ran %d times, want twice: one retry", len(q.dirs))
			}
			if len(res.Failed) != 1 || res.Stopped != nil {
				t.Fatalf("result = %+v, want #2 failed and the Run not stopped", res)
			}
			handedBack(t, tr, 2, "implement")
		})
	}
}

func TestAFailedReviewGetsOneFreshRetry(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	q := &sequence{outcomes: []harness.Outcome{noReport}, last: reports(harness.Done, "Fine.")}
	steps := &byStep{fns: map[string]func(context.Context, factory.Step) harness.Outcome{
		factory.Implement: commits(map[string]string{"cart.go": "package cart\n"}, "Added the cart."),
		factory.Review:    q.run,
	}}
	opts := failOptions(f, tr, steps.run)
	opts.Review = true

	res := factory.Run(context.Background(), opts)

	if len(res.Closed) != 1 || steps.names() != "implement review review" {
		t.Fatalf("result = %+v, Steps %q; want #2 closed after review ran again alone", res, steps.names())
	}
}

// noBackoff records each transient retry's number and doesn't wait.
type noBackoff struct{ retries []int }

func (n *noBackoff) wait(retry int) time.Duration {
	n.retries = append(n.retries, retry)
	return 0
}

func TestATransientErrorRetriesTheSameStepWithBackoff(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	q := &sequence{outcomes: []harness.Outcome{transient, transient, transient}, last: commits(map[string]string{"cart.go": "package cart\n"}, "Added the cart.")}
	backoff := &noBackoff{}
	opts := failOptions(f, tr, q.run)
	opts.Backoff = backoff.wait

	res := factory.Run(context.Background(), opts)

	if len(res.Closed) != 1 || res.Stopped != nil {
		t.Fatalf("result = %+v, want #2 closed on the third retry", res)
	}
	if fmt.Sprint(backoff.retries) != "[1 2 3]" {
		t.Errorf("backoff for retries %v, want [1 2 3]", backoff.retries)
	}
}

func TestTransientErrorsPastThreeRetriesStopTheRunAndLeaveTheTicketUntouched(t *testing.T) {
	f := newFixture(t)
	issues := append(cartSpec(), fake.Issue{Number: 4, Title: "Cart page", Labels: []string{ready}, Parent: 1})
	tr := fake.New(ready, issues...)
	q := &sequence{outcomes: []harness.Outcome{transient, transient, transient, transient}, last: commits(map[string]string{"x.go": "package x\n"}, "Done.")}
	opts := failOptions(f, tr, q.run)
	opts.Backoff = (&noBackoff{}).wait

	res := factory.Run(context.Background(), opts)

	if res.Stopped == nil || !strings.Contains(res.Stopped.Error(), "Overloaded") {
		t.Fatalf("stopped = %v, want the transient error", res.Stopped)
	}
	if len(q.dirs) != 4 {
		t.Errorf("the Step ran %d times, want 4: three retries", len(q.dirs))
	}
	if w := ticketWrites(tr); len(w) != 0 {
		t.Errorf("tracker writes = %+v, want every Ticket untouched", w)
	}
}

func TestATransientErrorDuringItsBackoffStopsWhenTheRunIsStopped(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	ctx, cancel := context.WithCancel(context.Background())
	q := &sequence{outcomes: []harness.Outcome{transient}}
	opts := failOptions(f, tr, q.run)
	opts.Backoff = func(int) time.Duration { cancel(); return time.Hour }

	done := make(chan factory.Result)
	go func() { done <- factory.Run(ctx, opts) }()

	select {
	case res := <-done:
		if res.Stopped == nil {
			t.Errorf("result = %+v, want the Run stopped", res)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the Run waited out its backoff after being stopped")
	}
}

func TestACommitTouchingAProtectedPathHandsTheTicketBackWithoutARetry(t *testing.T) {
	for name, tc := range map[string]struct {
		files map[string]string
		want  []string
	}{
		"the Factory's own directory": {map[string]string{".kraftwerk/config.toml": "version = 1\n", "cart.go": "package cart\n"}, []string{".kraftwerk/config.toml"}},
		"a configured directory":      {map[string]string{".claude/settings.json": "{}\n"}, []string{".claude/settings.json"}},
		"a configured file":           {map[string]string{"compose.yaml": "services: {}\n"}, []string{"compose.yaml"}},
		"a configured glob":           {map[string]string{"db/migrations/001.sql": "--\n"}, []string{"db/migrations/001.sql"}},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixture(t)
			tr := fake.New(ready, cartSpec()...)
			calls := 0
			implement := func(ctx context.Context, s factory.Step) harness.Outcome {
				calls++
				return commits(tc.files, "Done.")(ctx, s)
			}
			opts := failOptions(f, tr, implement)
			opts.ProtectedPaths = []string{".claude/", "compose.yaml", "db/*/*.sql"}

			res := factory.Run(context.Background(), opts)

			if calls != 1 {
				t.Errorf("implement ran %d times, want once: no retry", calls)
			}
			if len(res.Failed) != 1 {
				t.Fatalf("result = %+v, want #2 failed", res)
			}
			handedBack(t, tr, 2, append(tc.want, "Protected")...)
			if got := f.remoteRef(t, "kraftwerk/spec-1-spec-shopping-cart"); got != "" {
				t.Errorf("the Spec branch was pushed: %s", got)
			}
		})
	}
}

func TestAProtectedPathChangedAndChangedBackIsStillCaught(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	implement := func(ctx context.Context, s factory.Step) harness.Outcome {
		commits(map[string]string{".kraftwerk/.gitignore": "*\n"}, "")(ctx, s)
		return commits(map[string]string{".kraftwerk/.gitignore": "state/\nconfig.local.toml\n", "cart.go": "package cart\n"}, "Done.")(ctx, s)
	}

	res := factory.Run(context.Background(), failOptions(f, tr, implement))

	if len(res.Failed) != 1 {
		t.Fatalf("result = %+v, want #2 failed", res)
	}
	handedBack(t, tr, 2, ".kraftwerk/.gitignore")
}

func TestReviewTouchingAProtectedPathHandsTheTicketBack(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	steps := &byStep{fns: map[string]func(context.Context, factory.Step) harness.Outcome{
		factory.Implement: commits(map[string]string{"cart.go": "package cart\n"}, "Added the cart."),
		factory.Review:    commits(map[string]string{"docs/standards.md": "anything goes\n"}, "Relaxed the standards."),
	}}
	opts := failOptions(f, tr, steps.run)
	opts.Review, opts.ProtectedPaths = true, []string{"docs/standards.md"}

	res := factory.Run(context.Background(), opts)

	if len(res.Failed) != 1 || steps.names() != "implement review" {
		t.Fatalf("result = %+v, Steps %q; want #2 failed after one review", res, steps.names())
	}
	handedBack(t, tr, 2, "docs/standards.md")
}

func TestFilesOutsideTheProtectedPathsPass(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	opts := failOptions(f, tr, commits(map[string]string{".claude.md": "x\n", "composer.yaml": "x\n", "db/seed.sql": "--\n"}, "Done."))
	opts.ProtectedPaths = []string{".claude/", "compose.yaml", "db/*/*.sql"}

	res := factory.Run(context.Background(), opts)

	if len(res.Closed) != 1 {
		t.Fatalf("result = %+v, want #2 closed", res)
	}
}

// chatty is a Step that renders n numbered lines, the odd ones as tool
// calls and the even ones as the agent's text, then ends with outcome.
func chatty(n int, outcome harness.Outcome) func(context.Context, factory.Step) harness.Outcome {
	return func(_ context.Context, s factory.Step) harness.Outcome {
		for i := 1; i <= n; i++ {
			kind := runlog.Tool
			if i%2 == 0 {
				kind = runlog.AgentText
			}
			s.Log(kind, fmt.Sprintf("line %02d", i))
		}
		return outcome
	}
}

// excerpt is the collapsed block of a failure comment, "" if it has none.
func excerpt(comment string) string {
	start := strings.Index(comment, "<details>")
	end := strings.Index(comment, "</details>")
	if start < 0 || end < start {
		return ""
	}
	return comment[start:end]
}

func TestAFailureCommentHasTheLast30RenderedLinesButNeverThePrompt(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	cannotDo := harness.Outcome{Kind: harness.Reported, Report: harness.Report{Outcome: harness.CannotDo, Reason: "No currency given.", Summary: "Stopped."}}
	var prompt string
	implement := func(ctx context.Context, s factory.Step) harness.Outcome {
		prompt = s.Prompt
		s.Log(runlog.Tool, "tool "+strings.Repeat("x", 300))
		return chatty(40, cannotDo)(ctx, s)
	}
	var out strings.Builder
	opts := failOptions(f, tr, implement)
	opts.Log = func(l runlog.Line) {
		if l.Kind != runlog.AgentText {
			fmt.Fprintln(&out, l)
		}
	}

	factory.Run(context.Background(), opts)

	c := handedBack(t, tr, 2, "No currency given.")
	ex := excerpt(c)
	if ex == "" {
		t.Fatalf("the comment has no <details> block:\n%s", c)
	}
	for _, want := range []string{"line 40", "line 39", "line 20"} {
		if !strings.Contains(ex, want) {
			t.Errorf("the excerpt lacks %q:\n%s", want, ex)
		}
	}
	if lines := strings.Count(ex, "line "); lines > 30 {
		t.Errorf("the excerpt has %d of the Step's lines, want at most 30:\n%s", lines, ex)
	}
	for _, unwanted := range []string{"line 01", "line 10", "xxx"} {
		if strings.Contains(ex, unwanted) {
			t.Errorf("the excerpt has %q, older than its last 30 lines:\n%s", unwanted, ex)
		}
	}
	if strings.Contains(c, "Make a cart.") || strings.Contains(c, strings.SplitN(prompt, "\n", 2)[0]) {
		t.Errorf("the comment holds the prompt:\n%s", c)
	}
	if !strings.Contains(out.String(), "line 39") || strings.Contains(out.String(), "line 40") {
		t.Errorf("want tool calls in the Run's output and the agent's text left out:\n%s", out.String())
	}
}

func TestAFailureCommentCutsEachLineTo200Characters(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	long := strings.Repeat("ä", 250)
	implement := func(_ context.Context, s factory.Step) harness.Outcome {
		s.Log(runlog.Tool, long)
		return harness.Outcome{Kind: harness.Reported, Report: harness.Report{Outcome: harness.CannotDo, Reason: "No.", Summary: "No."}}
	}

	factory.Run(context.Background(), failOptions(f, tr, implement))

	ex := excerpt(handedBack(t, tr, 2))
	longest := 0
	for l := range strings.Lines(ex) {
		longest = max(longest, utf8.RuneCountInString(strings.TrimRight(l, "\n")))
	}
	if longest != 200 {
		t.Errorf("the longest line has %d characters, want the long line cut to 200:\n%s", longest, ex)
	}
}

// newFiles is an implement Step that commits a new file each time.
func newFiles() func(context.Context, factory.Step) harness.Outcome {
	n := 0
	return func(ctx context.Context, s factory.Step) harness.Outcome {
		n++
		return commits(map[string]string{fmt.Sprintf("f%d.go", n): "package cart\n"}, "Done.")(ctx, s)
	}
}

func TestAFailedVerifyCommentHasTheCommandsLast30Lines(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	opts := failOptions(f, tr, newFiles())
	opts.Verify, opts.VerifyEnv = []verify.Command{checkFixed}, os.Environ()

	factory.Run(context.Background(), opts)

	ex := excerpt(handedBack(t, tr, 2, "`test`"))
	if !strings.Contains(ex, "checking the cart") || strings.Contains(ex, "implement") {
		t.Errorf("the excerpt isn't the command's output:\n%s", ex)
	}
}

func TestAFailureCommentDropsTheExcerptIfASecretSurvives(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	// A key that spans lines gets past the redaction of each line.
	key := "BEGIN KEY\nc2VjcmV0\nEND KEY"
	opts := failOptions(f, tr, newFiles())
	// No space before &&: cmd.exe's echo would print it.
	opts.Verify = []verify.Command{{Name: "test", Line: "echo BEGIN KEY&& echo c2VjcmV0&& echo END KEY&& git grep -q fixed -- cart.go"}}
	opts.VerifyEnv = os.Environ()
	opts.Redactor = redact.New(key)

	factory.Run(context.Background(), opts)

	c := handedBack(t, tr, 2, "secret")
	if excerpt(c) != "" {
		t.Errorf("the excerpt wasn't dropped:\n%s", c)
	}
}

func TestAFailureCommentRedactsSecretsInTheExcerpt(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	implement := func(_ context.Context, s factory.Step) harness.Outcome {
		s.Log(runlog.Tool, "a token: hunter2")
		return harness.Outcome{Kind: harness.Reported, Report: harness.Report{Outcome: harness.CannotDo, Reason: "No.", Summary: "No."}}
	}
	opts := failOptions(f, tr, implement)
	opts.Redactor = redact.New("hunter2")

	factory.Run(context.Background(), opts)

	if ex := excerpt(handedBack(t, tr, 2)); !strings.Contains(ex, "a token: ***") {
		t.Errorf("the excerpt isn't there, redacted:\n%s", ex)
	}
}

func TestTheImplementPromptListsTheCannotDoCasesAndTheProtectedPaths(t *testing.T) {
	f := newFixture(t)
	steps := &byStep{fns: map[string]func(context.Context, factory.Step) harness.Outcome{
		factory.Implement: commits(map[string]string{"cart.go": "package cart\n"}, "Done."),
	}}
	opts := failOptions(f, fake.New(ready, cartSpec()...), steps.run)
	opts.ProtectedPaths = []string{"compose.yaml"}

	factory.Run(context.Background(), opts)

	p := steps.seen[0].Prompt
	for _, want := range []string{
		"cannot_do", "ambiguous", "Protected path", "credential", "tests fail", "already done",
		"nobody will answer questions", "`.kraftwerk/`", "`compose.yaml`",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("the implement prompt lacks %q:\n%s", want, p)
		}
	}
}
