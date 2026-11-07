package factory_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/factory"
	"github.com/Marcel-Goehn/kraftwerk/internal/harness"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker/fake"
	"github.com/Marcel-Goehn/kraftwerk/internal/verify"
)

const ready = "ready-for-agent"

// isolateGit keeps the user's own git config out of the test and gives
// every commit a fixed identity.
func isolateGit(t *testing.T) {
	t.Helper()
	empty := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func gitOK(dir string, args ...string) bool {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	return cmd.Run() == nil
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// fixture is a user's checkout and the bare remote it was cloned from.
type fixture struct {
	remote string
	work   string
}

// newFixture makes a bare remote whose main holds one commit that ignores
// the runtime state, and a clone of it as the user's checkout.
func newFixture(t *testing.T) fixture {
	t.Helper()
	isolateGit(t)
	dir := t.TempDir()
	f := fixture{remote: filepath.Join(dir, "remote.git"), work: filepath.Join(dir, "work")}
	git(t, dir, "init", "-q", "--bare", "-b", "main", f.remote)
	git(t, dir, "clone", "-q", f.remote, f.work)
	git(t, f.work, "symbolic-ref", "HEAD", "refs/heads/main")
	write(t, filepath.Join(f.work, ".kraftwerk", ".gitignore"), "state/\nconfig.local.toml\n")
	write(t, filepath.Join(f.work, "README.md"), "hello\n")
	git(t, f.work, "add", ".")
	git(t, f.work, "commit", "-q", "-m", "Initial commit")
	git(t, f.work, "push", "-q", "origin", "main")
	return f
}

// remoteRef is the SHA a branch has on the remote, "" if it has none.
func (f fixture) remoteRef(t *testing.T, branch string) string {
	t.Helper()
	out := git(t, f.remote, "for-each-ref", "--format=%(objectname)", "refs/heads/"+branch)
	return out
}

// commits implements a Ticket by writing each file and committing it.
func commits(files map[string]string, summary string) func(context.Context, factory.Step) harness.Outcome {
	return func(_ context.Context, s factory.Step) harness.Outcome {
		for name, content := range files {
			path := filepath.Join(s.Dir, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
				return harness.Outcome{Kind: harness.Failed, Class: harness.Other, Message: err.Error()}
			}
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				return harness.Outcome{Kind: harness.Failed, Class: harness.Other, Message: err.Error()}
			}
			for _, args := range [][]string{{"add", name}, {"commit", "-q", "-m", "Add " + name}} {
				cmd := exec.Command("git", args...)
				cmd.Dir = s.Dir
				if out, err := cmd.CombinedOutput(); err != nil {
					return harness.Outcome{Kind: harness.Failed, Class: harness.Other, Message: string(out)}
				}
			}
		}
		return harness.Outcome{Kind: harness.Reported, Report: harness.Report{Outcome: harness.Done, Summary: summary}}
	}
}

func options(f fixture, tr tracker.Tracker, runStep func(context.Context, factory.Step) harness.Outcome) factory.Options {
	return factory.Options{
		Tracker:       tr,
		Root:          f.work,
		Remote:        "origin",
		BaseBranch:    "main",
		Prefix:        "kraftwerk",
		ReadyLabel:    ready,
		MaxParallel:   1,
		MaxIterations: 10,
		RunStep:       runStep,
		VerifyTimeout: time.Minute,
		Out:           io.Discard,
	}
}

func cartSpec() []fake.Issue {
	return []fake.Issue{
		{Number: 1, Title: "Spec: Shopping cart", Body: "A cart.", Labels: []string{ready}},
		{Number: 2, Title: "Add the cart", Body: "Make a cart.", Labels: []string{ready}, Parent: 1},
	}
}

func TestATicketOfASpecIsImplementedPushedAndClosed(t *testing.T) {
	f := newFixture(t)
	base := git(t, f.work, "rev-parse", "HEAD")
	// The user is busy on a branch of their own, with a dirty tree.
	git(t, f.work, "checkout", "-q", "-b", "my-work")
	write(t, filepath.Join(f.work, "README.md"), "edited by the user\n")
	statusBefore := git(t, f.work, "status", "--porcelain")
	tr := fake.New(ready, cartSpec()...)

	res := factory.Run(context.Background(), options(f, tr, commits(map[string]string{"cart.go": "package cart\n"}, "Added the cart.")))

	if res.Stopped != nil || len(res.Failed) != 0 {
		t.Fatalf("result = %+v, want no failures", res)
	}
	spec := "kraftwerk/spec-1-spec-shopping-cart"
	tip := f.remoteRef(t, spec)
	if tip == "" {
		t.Fatalf("the remote has no %s; it has:\n%s", spec, git(t, f.remote, "branch", "-a"))
	}
	if got := git(t, f.remote, "log", "--format=%s", base+".."+tip); got != "Add cart.go" {
		t.Errorf("the Spec branch has commits %q on top of main, want just %q", got, "Add cart.go")
	}
	if got := f.remoteRef(t, "main"); got != base {
		t.Errorf("main moved on the remote: %s, want %s", got, base)
	}
	if got := git(t, f.remote, "for-each-ref", "refs/heads/kraftwerk/ticket-2"); got != "" {
		t.Errorf("the internal Ticket branch was pushed: %s", got)
	}

	writes := tr.Writes()
	if len(writes) != 1 || writes[0].Op != "close" || writes[0].Ticket != 2 {
		t.Fatalf("tracker writes = %+v, want #2 closed", writes)
	}
	for _, want := range []string{"Added the cart.", tip[:7], "Add cart.go", spec} {
		if !strings.Contains(writes[0].Text, want) {
			t.Errorf("close comment lacks %q:\n%s", want, writes[0].Text)
		}
	}

	if got := git(t, f.work, "rev-parse", "--abbrev-ref", "HEAD"); got != "my-work" {
		t.Errorf("the user's checkout is on %s, want my-work", got)
	}
	if got := git(t, f.work, "status", "--porcelain"); got != statusBefore {
		t.Errorf("the user's tree changed:\nbefore %q\nafter  %q", statusBefore, got)
	}
	if gitOK(f.work, "rev-parse", "--verify", "-q", "refs/heads/kraftwerk/ticket-2") {
		t.Error("the Ticket branch was kept after success")
	}
	if got := git(t, f.work, "worktree", "list", "--porcelain"); strings.Count(got, "worktree ") != 1 {
		t.Errorf("Factory worktrees are left:\n%s", got)
	}
}

func TestDoneWithoutCommitsHandsTheTicketBackWithoutARetry(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	calls := 0
	nothing := func(context.Context, factory.Step) harness.Outcome {
		calls++
		return harness.Outcome{Kind: harness.Reported, Report: harness.Report{Outcome: harness.Done, Summary: "All good."}}
	}

	res := factory.Run(context.Background(), failOptions(f, tr, nothing))

	if len(res.Failed) != 1 || res.Failed[0].Reason != "implement reported done without changes" {
		t.Fatalf("failed = %+v, want #2 reported done without changes", res.Failed)
	}
	if calls != 1 {
		t.Errorf("implement ran %d times, want once: a failed Ticket isn't taken again in the same Run", calls)
	}
	handedBack(t, tr, 2, "reported done without changes")
	if gitOK(f.work, "rev-parse", "--verify", "-q", "refs/heads/kraftwerk/ticket-2") {
		t.Error("the Ticket branch was kept after the Ticket was handed back")
	}
	if got := git(t, f.work, "worktree", "list", "--porcelain"); strings.Count(got, "worktree ") != 1 {
		t.Errorf("Factory worktrees are left:\n%s", got)
	}
	if got := f.remoteRef(t, "kraftwerk/spec-1-spec-shopping-cart"); got != "" {
		t.Errorf("the Spec branch was pushed without any work: %s", got)
	}
}

// advanceMain pushes a new commit to the remote's main from a second
// clone, as a teammate would.
func (f fixture) advanceMain(t *testing.T) string {
	t.Helper()
	other := filepath.Join(t.TempDir(), "other")
	git(t, "", "clone", "-q", f.remote, other)
	write(t, filepath.Join(other, "later.txt"), "later\n")
	git(t, other, "add", ".")
	git(t, other, "commit", "-q", "-m", "Later on main")
	git(t, other, "push", "-q", "origin", "main")
	return git(t, other, "rev-parse", "HEAD")
}

func TestAnExistingSpecBranchIsFoundByNumberAndContinued(t *testing.T) {
	for _, where := range []string{"locally", "only on the remote"} {
		t.Run(where, func(t *testing.T) {
			f := newFixture(t)
			// The Spec was worked on under an older title, then main moved on.
			old := "kraftwerk/spec-1-old-title"
			git(t, f.work, "branch", old, "main")
			wt := filepath.Join(t.TempDir(), "wt")
			git(t, f.work, "worktree", "add", "-q", wt, old)
			write(t, filepath.Join(wt, "first.go"), "package first\n")
			git(t, wt, "add", ".")
			git(t, wt, "commit", "-q", "-m", "Earlier Ticket")
			git(t, f.work, "push", "-q", "origin", old)
			git(t, f.work, "worktree", "remove", wt)
			if where == "only on the remote" {
				git(t, f.work, "branch", "-D", old)
			}
			f.advanceMain(t)
			tr := fake.New(ready, cartSpec()...)

			res := factory.Run(context.Background(), options(f, tr, commits(map[string]string{"cart.go": "package cart\n"}, "Added the cart.")))

			if len(res.Closed) != 1 || res.Stopped != nil {
				t.Fatalf("result = %+v, want #2 closed", res)
			}
			tip := f.remoteRef(t, old)
			if got := git(t, f.remote, "log", "--format=%s", "--first-parent", tip); got != "Add cart.go\nEarlier Ticket\nInitial commit" {
				t.Errorf("history of %s = %q, want the new commit on top of the old tip, not rebased", old, got)
			}
			if got := git(t, f.remote, "for-each-ref", "--format=%(refname)", "refs/heads/kraftwerk/"); got != "refs/heads/kraftwerk/spec-1-old-title" {
				t.Errorf("branches on the remote = %q, want only the existing Spec branch", got)
			}
		})
	}
}

func TestAStandaloneTicketGetsABranchOfItsOwn(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, fake.Issue{Number: 7, Title: "Fix the typo in README", Labels: []string{ready}})

	res := factory.Run(context.Background(), options(f, tr, commits(map[string]string{"typo.txt": "fixed\n"}, "Fixed it.")))

	if len(res.Closed) != 1 {
		t.Fatalf("result = %+v, want #7 closed", res)
	}
	if got := git(t, f.remote, "for-each-ref", "--format=%(refname:short)", "refs/heads/kraftwerk/"); got != "kraftwerk/ticket-7-fix-the-typo-in-readme" {
		t.Errorf("branches on the remote = %q, want only the standalone Ticket branch", got)
	}
}

func TestTheRunEndsAtMaxIterations(t *testing.T) {
	f := newFixture(t)
	issues := append(cartSpec(), fake.Issue{Number: 3, Title: "Cart totals", Labels: []string{ready}, Parent: 1, BlockedBy: []int{2}})
	tr := fake.New(ready, issues...)
	opts := options(f, tr, commits(map[string]string{"cart.go": "package cart\n"}, "Done."))
	opts.MaxIterations = 1

	res := factory.Run(context.Background(), opts)

	if res.Iterations != 1 || !res.LimitReached || len(res.Closed) != 1 || res.Closed[0].ID.Display != "#2" {
		t.Fatalf("result = %+v, want one Iteration closing #2, ending at the limit", res)
	}
}

func TestTheNextIterationTakesTheUnblockedTicket(t *testing.T) {
	f := newFixture(t)
	issues := append(cartSpec(), fake.Issue{Number: 3, Title: "Cart totals", Labels: []string{ready}, Parent: 1, BlockedBy: []int{2}})
	tr := fake.New(ready, issues...)
	n := 0
	implement := func(ctx context.Context, s factory.Step) harness.Outcome {
		n++
		return commits(map[string]string{fmt.Sprintf("f%d.go", n): "package f\n"}, "Done.")(ctx, s)
	}

	res := factory.Run(context.Background(), options(f, tr, implement))

	if res.Iterations != 2 || res.LimitReached || len(res.Closed) != 2 {
		t.Fatalf("result = %+v, want two Iterations closing #2 then #3", res)
	}
	if got := git(t, f.remote, "log", "--format=%s", "main..kraftwerk/spec-1-spec-shopping-cart"); got != "Add f2.go\nAdd f1.go" {
		t.Errorf("Spec branch commits = %q, want both Tickets' work", got)
	}
}

func TestAnAuthErrorStopsTheRun(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	auth := func(context.Context, factory.Step) harness.Outcome {
		return harness.Outcome{Kind: harness.Failed, Class: harness.Auth, Message: "Invalid API key"}
	}

	res := factory.Run(context.Background(), options(f, tr, auth))

	if res.Stopped == nil || !strings.Contains(res.Stopped.Error(), "Invalid API key") {
		t.Fatalf("stopped = %v, want the auth error", res.Stopped)
	}
	if w := tr.Writes(); len(w) != 0 {
		t.Errorf("tracker writes = %+v, want the Ticket untouched", w)
	}
}

func TestARunNeedsTheRuntimeStateIgnored(t *testing.T) {
	f := newFixture(t)
	write(t, filepath.Join(f.work, ".kraftwerk", ".gitignore"), "config.local.toml\n")

	res := factory.Run(context.Background(), options(f, fake.New(ready, cartSpec()...), nil))

	var setup *factory.SetupError
	if !errors.As(res.Stopped, &setup) || !strings.Contains(setup.Error(), "state/") {
		t.Fatalf("stopped = %v, want a SetupError naming state/", res.Stopped)
	}
}

func TestARejectedPushLeavesTheSpecBranchAndTheTicketAsTheyWere(t *testing.T) {
	f := newFixture(t)
	spec := "kraftwerk/spec-1-spec-shopping-cart"
	git(t, f.work, "branch", spec, "main")
	git(t, f.work, "push", "-q", "origin", spec)
	localTip := git(t, f.work, "rev-parse", spec)
	// Someone else pushes to the Spec branch.
	other := filepath.Join(t.TempDir(), "other")
	git(t, "", "clone", "-q", "-b", spec, f.remote, other)
	write(t, filepath.Join(other, "theirs.txt"), "theirs\n")
	git(t, other, "add", ".")
	git(t, other, "commit", "-q", "-m", "Human commit")
	git(t, other, "push", "-q", "origin", spec)
	theirs := git(t, other, "rev-parse", "HEAD")
	tr := fake.New(ready, cartSpec()...)

	res := factory.Run(context.Background(), options(f, tr, commits(map[string]string{"cart.go": "package cart\n"}, "Done.")))

	if len(res.Failed) != 1 || !strings.Contains(res.Failed[0].Reason, "push") {
		t.Fatalf("failed = %+v, want #2 failed on the push", res.Failed)
	}
	if got := git(t, f.work, "rev-parse", spec); got != localTip {
		t.Errorf("the local Spec branch moved to %s, want it left at %s", got, localTip)
	}
	if got := f.remoteRef(t, spec); got != theirs {
		t.Errorf("the remote Spec branch is %s, want the human's commit %s kept", got, theirs)
	}
	if w := tr.Writes(); len(w) != 0 {
		t.Errorf("tracker writes = %+v, want #2 left open", w)
	}
	if !gitOK(f.work, "rev-parse", "--verify", "-q", "refs/heads/kraftwerk/ticket-2") {
		t.Error("the Ticket branch was deleted after the failed push")
	}
}

// TestARunWorksWhenTheRootIsNotGitsCanonicalPath covers a project root
// that names the checkout differently than git does: through a symlink,
// like macOS's /var → /private/var, or Windows' 8.3 short names.
func TestARunWorksWhenTheRootIsNotGitsCanonicalPath(t *testing.T) {
	f := newFixture(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(f.work, link); err != nil {
		t.Skipf("can't make a symlink: %v", err)
	}
	opts := options(f, fake.New(ready, cartSpec()...), commits(map[string]string{"cart.go": "package cart\n"}, "Done."))
	opts.Root = link

	res := factory.Run(context.Background(), opts)

	if len(res.Closed) != 1 {
		t.Fatalf("result = %+v, want #2 closed", res)
	}
}

// byStep runs each Step's function by name, recording the Steps it saw.
type byStep struct {
	fns  map[string]func(context.Context, factory.Step) harness.Outcome
	seen []factory.Step
}

// run is the RunStep that dispatches by name.
func (b *byStep) run(ctx context.Context, s factory.Step) harness.Outcome {
	b.seen = append(b.seen, s)
	fn, ok := b.fns[s.Name]
	if !ok {
		return harness.Outcome{Kind: harness.Failed, Class: harness.Other, Message: "no " + s.Name + " in the test"}
	}
	return fn(ctx, s)
}

// names lists the Steps seen, in order.
func (b *byStep) names() string {
	var n []string
	for _, s := range b.seen {
		n = append(n, s.Name)
	}
	return strings.Join(n, " ")
}

// reports is a Step that commits nothing and reports outcome.
func reports(outcome, summary string) func(context.Context, factory.Step) harness.Outcome {
	return func(context.Context, factory.Step) harness.Outcome {
		return harness.Outcome{Kind: harness.Reported, Report: harness.Report{Outcome: outcome, Summary: summary, Reason: summary}}
	}
}

func TestReviewApprovesWithoutCommitsInAFreshStepOnTheSameWorktree(t *testing.T) {
	f := newFixture(t)
	base := git(t, f.work, "rev-parse", "HEAD")
	tr := fake.New(ready, cartSpec()...)
	steps := &byStep{fns: map[string]func(context.Context, factory.Step) harness.Outcome{
		factory.Implement: commits(map[string]string{"cart.go": "package cart\n"}, "Added the cart."),
		factory.Review:    reports(harness.Done, "Looks good."),
	}}
	opts := options(f, tr, steps.run)
	opts.Review = true

	res := factory.Run(context.Background(), opts)

	if len(res.Closed) != 1 || len(res.Failed) != 0 {
		t.Fatalf("result = %+v, want #2 closed", res)
	}
	if got := steps.names(); got != "implement review" {
		t.Fatalf("Steps = %q, want implement then review", got)
	}
	impl, rev := steps.seen[0], steps.seen[1]
	if rev.Dir != impl.Dir {
		t.Errorf("review ran in %s, want implement's worktree %s", rev.Dir, impl.Dir)
	}
	for _, want := range []string{"Review", "#2", base} {
		if !strings.Contains(rev.Prompt, want) {
			t.Errorf("review prompt lacks %q:\n%s", want, rev.Prompt)
		}
	}
	if got := git(t, f.remote, "log", "--format=%s", "main..kraftwerk/spec-1-spec-shopping-cart"); got != "Add cart.go" {
		t.Errorf("Spec branch commits = %q, want implement's only", got)
	}
}

func TestReviewThatCommitsFixesTheChangeAndItsFixesArePushed(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	var out strings.Builder
	steps := &byStep{fns: map[string]func(context.Context, factory.Step) harness.Outcome{
		factory.Implement: commits(map[string]string{"cart.go": "package cart\n"}, "Added the cart."),
		factory.Review:    commits(map[string]string{"cart_test.go": "package cart\n"}, "Added the missing test."),
	}}
	opts := options(f, tr, steps.run)
	opts.Review, opts.Out = true, &out

	res := factory.Run(context.Background(), opts)

	if len(res.Closed) != 1 {
		t.Fatalf("result = %+v, want #2 closed", res)
	}
	if got := git(t, f.remote, "log", "--format=%s", "main..kraftwerk/spec-1-spec-shopping-cart"); got != "Add cart_test.go\nAdd cart.go" {
		t.Errorf("Spec branch commits = %q, want implement's and review's", got)
	}
	if !strings.Contains(out.String(), "#2 review: fixed it in 1 commit") {
		t.Errorf("the log doesn't say review fixed it:\n%s", out.String())
	}
	if c := tr.Writes()[0].Text; !strings.Contains(c, "Added the cart.") || !strings.Contains(c, "Added the missing test.") {
		t.Errorf("close comment lacks implement's or review's summary:\n%s", c)
	}
}

func TestAReviewCannotDoCountsAsDoneWithAWarning(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	var out strings.Builder
	steps := &byStep{fns: map[string]func(context.Context, factory.Step) harness.Outcome{
		factory.Implement: commits(map[string]string{"cart.go": "package cart\n"}, "Added the cart."),
		factory.Review:    reports(harness.CannotDo, "I'd rather not."),
	}}
	opts := options(f, tr, steps.run)
	opts.Review, opts.Out = true, &out

	res := factory.Run(context.Background(), opts)

	if len(res.Closed) != 1 || len(res.Failed) != 0 {
		t.Fatalf("result = %+v, want #2 closed", res)
	}
	if !strings.Contains(out.String(), "#2 review: warning: reported cannot_do (I'd rather not.)") {
		t.Errorf("the log lacks the warning:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "#2 review: approved") {
		t.Errorf("a review without commits isn't logged as approved:\n%s", out.String())
	}
}

func TestReviewDisabledIsSkipped(t *testing.T) {
	f := newFixture(t)
	steps := &byStep{fns: map[string]func(context.Context, factory.Step) harness.Outcome{
		factory.Implement: commits(map[string]string{"cart.go": "package cart\n"}, "Added the cart."),
	}}
	opts := options(f, fake.New(ready, cartSpec()...), steps.run)
	opts.Review = false

	res := factory.Run(context.Background(), opts)

	if len(res.Closed) != 1 || steps.names() != "implement" {
		t.Fatalf("result = %+v, Steps %q; want #2 closed after implement alone", res, steps.names())
	}
}

// checkFixed is a Feedback command, portable between sh and cmd.exe, that
// prints a line and then passes only if cart.go says "fixed".
var checkFixed = verify.Command{Name: "test", Line: "echo checking the cart && git grep -q fixed -- cart.go"}

func TestAFailedVerifyRetriesImplementOnceWithTheFailingOutput(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	n := 0
	implement := func(ctx context.Context, s factory.Step) harness.Outcome {
		n++
		content := "package cart\n"
		if n == 2 {
			content = "package cart // fixed\n"
		}
		return commits(map[string]string{"cart.go": content}, fmt.Sprintf("Attempt %d.", n))(ctx, s)
	}
	steps := &byStep{fns: map[string]func(context.Context, factory.Step) harness.Outcome{
		factory.Implement: implement,
		factory.Review:    reports(harness.Done, "Fine."),
	}}
	opts := options(f, tr, steps.run)
	opts.Review, opts.Verify, opts.VerifyEnv = true, []verify.Command{checkFixed}, os.Environ()

	res := factory.Run(context.Background(), opts)

	if len(res.Closed) != 1 || len(res.Failed) != 0 {
		t.Fatalf("result = %+v, want #2 closed after the retry", res)
	}
	if got := steps.names(); got != "implement review implement review" {
		t.Fatalf("Steps = %q, want implement and review twice", got)
	}
	retry := steps.seen[2].Prompt
	for _, want := range []string{"`test`", "checking the cart"} {
		if !strings.Contains(retry, want) {
			t.Errorf("the retried implement prompt lacks %q:\n%s", want, retry)
		}
	}
	if strings.Contains(steps.seen[0].Prompt, "checking the cart") {
		t.Error("the first implement prompt already has verify's output")
	}
	if c := tr.Writes()[0].Text; !strings.Contains(c, "Attempt 2.") {
		t.Errorf("close comment lacks the last implement's summary:\n%s", c)
	}
}

func TestVerifyFailingAgainAfterTheRetryHandsTheTicketBack(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	n := 0
	implement := func(ctx context.Context, s factory.Step) harness.Outcome {
		n++
		return commits(map[string]string{fmt.Sprintf("f%d.go", n): "package cart\n"}, "Done.")(ctx, s)
	}
	opts := failOptions(f, tr, implement)
	opts.Verify, opts.VerifyEnv = []verify.Command{checkFixed}, os.Environ()

	res := factory.Run(context.Background(), opts)

	if len(res.Failed) != 1 || !strings.Contains(res.Failed[0].Reason, "`test`") {
		t.Fatalf("failed = %+v, want #2 failed naming `test`", res.Failed)
	}
	if n != 2 {
		t.Errorf("implement ran %d times, want 2: one retry", n)
	}
	handedBack(t, tr, 2, "`test`")
	if got := f.remoteRef(t, "kraftwerk/spec-1-spec-shopping-cart"); got != "" {
		t.Errorf("the Spec branch was pushed with failing checks: %s", got)
	}
}

func TestVerifyRunsAfterReviewsFix(t *testing.T) {
	f := newFixture(t)
	steps := &byStep{fns: map[string]func(context.Context, factory.Step) harness.Outcome{
		factory.Implement: commits(map[string]string{"cart.go": "package cart\n"}, "Added the cart."),
		factory.Review:    commits(map[string]string{"cart.go": "package cart // fixed\n"}, "Fixed it."),
	}}
	opts := options(f, fake.New(ready, cartSpec()...), steps.run)
	opts.Review, opts.Verify, opts.VerifyEnv = true, []verify.Command{checkFixed}, os.Environ()

	res := factory.Run(context.Background(), opts)

	if len(res.Closed) != 1 || steps.names() != "implement review" {
		t.Fatalf("result = %+v, Steps %q; want #2 closed with no retry", res, steps.names())
	}
}
