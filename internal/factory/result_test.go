package factory_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/kraftwerk/internal/factory"
	"github.com/Marcel-Goehn/kraftwerk/internal/harness"
	"github.com/Marcel-Goehn/kraftwerk/internal/runlog"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker/fake"
	"github.com/Marcel-Goehn/kraftwerk/internal/verify"
)

func cartCommit() func(context.Context, factory.Step) harness.Outcome {
	return commits(map[string]string{"cart.go": "package cart\n"}, "Done.")
}

// differentEachTime commits something new on every call, as a retry does.
func differentEachTime() func(context.Context, factory.Step) harness.Outcome {
	n := 0
	return func(ctx context.Context, s factory.Step) harness.Outcome {
		n++
		return commits(map[string]string{"cart.go": fmt.Sprintf("package cart // %d\n", n)}, "Done.")(ctx, s)
	}
}

func TestEveryAttemptOfAStepCountsTowardsItsTicketAndTheRun(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready, cartSpec()...)
	attempt := 0
	implement := func(ctx context.Context, s factory.Step) harness.Outcome {
		attempt++
		if attempt == 1 {
			return harness.Outcome{Kind: harness.Failed, Class: harness.Other, Message: "boom", ContextWindow: 900,
				Usage: harness.Usage{Input: 10, Output: 1, Cost: 1, CostUnit: harness.USD}}
		}
		out := cartCommit()(ctx, s)
		out.ContextWindow = 500
		out.Usage = harness.Usage{Input: 20, Output: 2, Cost: 2, CostUnit: harness.USD}
		return out
	}

	res := factory.Run(context.Background(), options(f, tr, implement))

	if len(res.Stats) != 1 {
		t.Fatalf("stats = %+v, want one Ticket", res.Stats)
	}
	s := res.Stats[0]
	if s.Ticket.ID.Display != "#2" || s.Commits != 1 || s.LargestContextWindow != 900 {
		t.Errorf("stats = %+v, want #2 with 1 commit and its largest context window, 900", s)
	}
	if s.Usage.Input != 30 || s.Usage.Output != 3 || s.Usage.CostString() != "$3.00" {
		t.Errorf("Ticket usage = %+v", s.Usage)
	}
	if res.Usage.Input != 30 || res.Usage.CostString() != "$3.00" {
		t.Errorf("Run usage = %+v", res.Usage)
	}
}

func TestAFailedTicketSaysWhatKindOfFailureItWas(t *testing.T) {
	report := func(outcome string) func(context.Context, factory.Step) harness.Outcome {
		return func(context.Context, factory.Step) harness.Outcome {
			return harness.Outcome{Kind: harness.Reported, Report: harness.Report{Outcome: outcome, Reason: "no", Summary: "s"}}
		}
	}
	end := func(o harness.Outcome) func(context.Context, factory.Step) harness.Outcome {
		return func(context.Context, factory.Step) harness.Outcome { return o }
	}
	cases := []struct {
		kind string
		step func(context.Context, factory.Step) harness.Outcome
		opts func(*factory.Options)
	}{
		{factory.KindCannotDo, report(harness.CannotDo), nil},
		{factory.KindNoChanges, report(harness.Done), nil},
		{factory.KindNoReport, end(harness.Outcome{Kind: harness.NoReport}), nil},
		{factory.KindHarnessError, end(harness.Outcome{Kind: harness.Failed, Class: harness.Other, Message: "boom"}), nil},
		{factory.KindVerify, differentEachTime(), func(o *factory.Options) {
			o.Verify = []verify.Command{{Name: "test", Line: "git --no-such-flag"}}
		}},
		{factory.KindProtectedPath, commits(map[string]string{".kraftwerk/x": "x\n"}, "Done."), nil},
	}
	for _, c := range cases {
		t.Run(c.kind, func(t *testing.T) {
			f := newFixture(t)
			opts := options(f, fake.New(ready, cartSpec()...), c.step)
			if c.opts != nil {
				c.opts(&opts)
			}

			res := factory.Run(context.Background(), opts)

			if len(res.Failed) != 1 || res.Failed[0].Kind != c.kind {
				t.Errorf("failed = %+v, want kind %s", res.Failed, c.kind)
			}
		})
	}
}

func TestARejectedPushNamesTheSpecItStoppedAndTheFix(t *testing.T) {
	f := newFixture(t)
	spec := "kraftwerk/spec-1-spec-shopping-cart"
	git(t, f.work, "branch", spec, "main")
	git(t, f.work, "push", "-q", "origin", spec)
	other := t.TempDir() + "/other"
	git(t, "", "clone", "-q", "-b", spec, f.remote, other)
	write(t, other+"/theirs.txt", "theirs\n")
	git(t, other, "add", ".")
	git(t, other, "commit", "-q", "-m", "Human commit")
	git(t, other, "push", "-q", "origin", spec)

	res := factory.Run(context.Background(), options(f, fake.New(ready, cartSpec()...), cartCommit()))

	if len(res.Failed) != 1 || res.Failed[0].Kind != factory.KindPushRejected {
		t.Fatalf("failed = %+v, want the push rejected", res.Failed)
	}
	if len(res.StoppedSpecs) != 1 {
		t.Fatalf("stopped Specs = %+v, want #1", res.StoppedSpecs)
	}
	got := res.StoppedSpecs[0]
	if got.ID.Display != "#1" || got.Branch != spec || !strings.Contains(got.Fix, "git fetch origin "+spec+":"+spec) {
		t.Errorf("stopped Spec = %+v", got)
	}
}

func TestTheTicketsStillReadyAtTheLimitAreCounted(t *testing.T) {
	f := newFixture(t)
	issues := append(cartSpec(), fake.Issue{Number: 3, Title: "Cart totals", Labels: []string{ready}, Parent: 1, BlockedBy: []int{2}})
	opts := options(f, fake.New(ready, issues...), cartCommit())
	opts.MaxIterations = 1

	res := factory.Run(context.Background(), opts)

	if res.StillReady != 1 {
		t.Errorf("still ready = %d, want #3, which closing #2 made ready", res.StillReady)
	}
}

func TestNothingIsStillReadyWithoutALimit(t *testing.T) {
	f := newFixture(t)

	res := factory.Run(context.Background(), options(f, fake.New(ready, cartSpec()...), cartCommit()))

	if res.StillReady != 0 || res.LimitReached {
		t.Errorf("still ready = %d, limit reached = %v", res.StillReady, res.LimitReached)
	}
}

func TestAFinishedSpecIsReported(t *testing.T) {
	f := newFixture(t)

	res := factory.Run(context.Background(), options(f, fake.New(ready, cartSpec()...), cartCommit()))

	if len(res.SpecsFinished) != 1 {
		t.Fatalf("finished = %+v, want #1", res.SpecsFinished)
	}
	got := res.SpecsFinished[0]
	if got.ID.Display != "#1" || got.Title != "Spec: Shopping cart" || got.Branch != "kraftwerk/spec-1-spec-shopping-cart" {
		t.Errorf("finished = %+v", got)
	}
}

func TestLinesAreToldTheirTicketAndStep(t *testing.T) {
	f := newFixture(t)
	opts := options(f, fake.New(ready, cartSpec()...), func(ctx context.Context, s factory.Step) harness.Outcome {
		s.Log(runlog.Tool, "Read x")
		return cartCommit()(ctx, s)
	})
	var lines []string
	opts.Log = func(l runlog.Line) { lines = append(lines, fmt.Sprintf("%s|%s|%s", l.Ticket, l.Step, l.Text)) }

	factory.Run(context.Background(), opts)

	got := strings.Join(lines, "\n")
	for _, want := range []string{"#2|implement|Read x", "#2||closed", "|implement|started on"} {
		if !strings.Contains(got, want) {
			t.Errorf("lines lack %q:\n%s", want, got)
		}
	}
}
