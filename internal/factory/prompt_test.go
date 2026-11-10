package factory_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/factory"
	"github.com/Marcel-Goehn/kraftwerk/internal/harness"
	"github.com/Marcel-Goehn/kraftwerk/internal/prompt"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker/fake"
	"github.com/Marcel-Goehn/kraftwerk/internal/verify"
)

// commitsWithBody implements a Ticket in one commit whose message has a
// subject and a body.
func commitsWithBody(file, subject, body string) func(context.Context, factory.Step) harness.Outcome {
	return func(_ context.Context, s factory.Step) harness.Outcome {
		if err := os.WriteFile(filepath.Join(s.Dir, file), []byte(subject+"\n"), 0o600); err != nil {
			return harness.Outcome{Kind: harness.Failed, Class: harness.Other, Message: err.Error()}
		}
		for _, args := range [][]string{{"add", file}, {"commit", "-q", "-m", subject, "-m", body}} {
			cmd := exec.Command("git", args...)
			cmd.Dir = s.Dir
			if out, err := cmd.CombinedOutput(); err != nil {
				return harness.Outcome{Kind: harness.Failed, Class: harness.Other, Message: string(out)}
			}
		}
		return harness.Outcome{Kind: harness.Reported, Report: harness.Report{Outcome: harness.Done, Summary: subject}}
	}
}

func TestEachStepsPromptHasTheTicketsCommentsTheRecentCommitsAndTheProjectsPart(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready,
		fake.Issue{Number: 1, Title: "Spec: Shopping cart", Body: "A cart.", Labels: []string{ready}},
		fake.Issue{Number: 2, Title: "Add the cart", Body: "Make a cart.", Labels: []string{ready}, Parent: 1},
		fake.Issue{Number: 3, Title: "Add checkout", Body: "Check out the cart.", Labels: []string{ready}, Parent: 1, BlockedBy: []int{2},
			Comments: []tracker.Comment{{Author: "alice", CreatedAt: time.Date(2026, 11, 1, 9, 30, 0, 0, time.UTC), Body: "Card payments only."}}},
	)
	steps := &byStep{fns: map[string]func(context.Context, factory.Step) harness.Outcome{
		factory.Implement: func(ctx context.Context, s factory.Step) harness.Outcome {
			if s.Ticket.ID.Key == "2" {
				return commitsWithBody("cart.go", "Add the cart", "The cart is a map of items.")(ctx, s)
			}
			return commitsWithBody("checkout.go", "Add checkout", "Checkout takes cards.")(ctx, s)
		},
		factory.Review: reports(harness.Done, "Fine."),
	}}
	opts := options(f, tr, steps.run)
	opts.Review = true
	opts.Prompts = map[string]prompt.Part{
		factory.Implement: {Text: "# Our way\n\n{{commands}}\n"},
		factory.Review:    {Text: "# Our review\n\n{{standards}}\n\n{{commands}}\n"},
	}
	opts.Commands = []verify.Command{{Name: "test", Line: "go test ./..."}}
	opts.Standards = "docs/standards.md"

	res := factory.Run(context.Background(), opts)

	if len(res.Closed) != 2 {
		t.Fatalf("result = %+v, want #2 and #3 closed", res)
	}
	if got := steps.names(); got != "implement review implement review" {
		t.Fatalf("Steps = %q", got)
	}
	first, second, secondReview := steps.seen[0].Prompt, steps.seen[2].Prompt, steps.seen[3].Prompt

	inOrder(t, first, "- #2 Add the cart (this Ticket)", "- #3 Add checkout (open)",
		"The last commits of `main`", "Initial commit", "# Our way", "- test: `go test ./...`")
	if strings.Contains(first, "docs/standards.md") {
		t.Errorf("implement got the standards:\n%s", first)
	}

	inOrder(t, second, "Check out the cart.", `author="alice"`, "Card payments only.",
		"- #2 Add the cart (closed)", "- #3 Add checkout (this Ticket)",
		"The commits on the Spec branch since `main`", "Add the cart\n\nThe cart is a map of items.")
	if strings.Contains(second, "Initial commit") {
		t.Errorf("the Spec branch has commits, but implement got main's:\n%s", second)
	}

	inOrder(t, secondReview, "Card payments only.", "# What changed", "# Our review",
		"Follow the coding standards in `docs/standards.md`", "After your changes, run these")
}

func TestWithoutProjectFilesTheBuiltInPartsAreUsed(t *testing.T) {
	f := newFixture(t)
	steps := &byStep{fns: map[string]func(context.Context, factory.Step) harness.Outcome{
		factory.Implement: commits(map[string]string{"cart.go": "package cart\n"}, "Done."),
		factory.Review:    reports(harness.Done, "Fine."),
	}}
	opts := options(f, fake.New(ready, cartSpec()...), steps.run)
	opts.Review = true

	factory.Run(context.Background(), opts)

	inOrder(t, steps.seen[0].Prompt, "# Recent commits", "Use test-driven development", "# Do not close the Ticket")
	inOrder(t, steps.seen[1].Prompt, "# What changed", "**Feature Envy**", "# Do not close the Ticket")
}

// inOrder fails unless every part is in p, each after the one before.
func inOrder(t *testing.T, p string, parts ...string) {
	t.Helper()
	rest := p
	for _, part := range parts {
		i := strings.Index(rest, part)
		if i < 0 {
			t.Fatalf("the prompt lacks %q after the parts before it:\n%s", part, p)
		}
		rest = rest[i+len(part):]
	}
}

func TestTheOtherTicketsOfTheIterationAreMarkedInProgress(t *testing.T) {
	f := newFixture(t)
	tr := fake.New(ready,
		fake.Issue{Number: 1, Title: "Spec: Shop", Body: "A shop.", Labels: []string{ready}},
		fake.Issue{Number: 2, Title: "Add the cart", Body: "Make a cart.", Labels: []string{ready}, Parent: 1},
		fake.Issue{Number: 3, Title: "Add the shelf", Body: "Make a shelf.", Labels: []string{ready}, Parent: 1},
	)
	steps := &byStep{fns: map[string]func(context.Context, factory.Step) harness.Outcome{
		factory.Implement: func(ctx context.Context, s factory.Step) harness.Outcome {
			return commitsWithBody("t"+s.Ticket.ID.Key+".go", "Add "+s.Ticket.ID.Key, "Body.")(ctx, s)
		},
	}}
	opts := options(f, tr, steps.run)
	opts.MaxParallel = 2

	res := factory.Run(context.Background(), opts)

	if len(res.Closed) != 2 || res.Iterations != 1 {
		t.Fatalf("result = %+v, want #2 and #3 closed in one Iteration", res)
	}
	for _, seen := range steps.seen {
		if seen.Ticket.ID.Display == "#2" {
			inOrder(t, seen.Prompt, "- #2 Add the cart (this Ticket)", "- #3 Add the shelf (in progress)")
		} else {
			inOrder(t, seen.Prompt, "- #2 Add the cart (in progress)", "- #3 Add the shelf (this Ticket)")
		}
	}
}
