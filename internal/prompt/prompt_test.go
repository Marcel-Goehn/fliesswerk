package prompt_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Marcel-Goehn/fliesswerk/internal/git"
	"github.com/Marcel-Goehn/fliesswerk/internal/prompt"
	"github.com/Marcel-Goehn/fliesswerk/internal/sandbox"
	"github.com/Marcel-Goehn/fliesswerk/internal/tracker"
	"github.com/Marcel-Goehn/fliesswerk/internal/verify"
)

func id(n string) tracker.ID {
	return tracker.ID{Key: n, Display: "#" + n, Branch: n}
}

// inSpec is Ticket #2 of Spec #1, with two comments, while #4 is in
// progress elsewhere.
func inSpec() prompt.Context {
	parent := id("1")
	return prompt.Context{
		Ticket: tracker.Ticket{ID: id("2"), Title: "Add the cart", Body: "Make a cart.", Parent: &parent},
		Comments: []tracker.Comment{
			{Author: "alice", CreatedAt: time.Date(2026, 11, 1, 9, 30, 0, 0, time.UTC), Body: "Use a map."},
			{Author: "bob", CreatedAt: time.Date(2026, 11, 2, 14, 0, 0, 0, time.UTC), Body: "Agreed."},
		},
		Spec: &tracker.Spec{ID: parent, Title: "Spec: Shopping cart", Body: "A cart for the shop.", Tickets: []tracker.SpecTicket{
			{ID: id("3"), Title: "Add the shop", State: tracker.Closed},
			{ID: id("2"), Title: "Add the cart", State: tracker.Open},
			{ID: id("4"), Title: "Add checkout", State: tracker.Open},
			{ID: id("5"), Title: "Add payment", State: tracker.Open},
		}},
		InProgress: map[string]bool{"4": true},
		Branch:     "fliesswerk/ticket-2",
		Protected:  []string{".fliesswerk/", "compose.yaml"},
	}
}

// standalone is Ticket #7, with no Spec and no comments.
func standalone() prompt.Context {
	return prompt.Context{
		Ticket:    tracker.Ticket{ID: id("7"), Title: "Fix the typo", Body: "It says teh."},
		Branch:    "fliesswerk/ticket-7",
		Protected: []string{".fliesswerk/"},
	}
}

var commands = []verify.Command{{Name: "test", Line: "go test ./..."}, {Name: "vet", Line: "go vet ./..."}}

var specCommits = prompt.Recent{BaseBranch: "main", OnSpecBranch: true, Commits: []git.Message{
	{SHA: "bbbbbbbbbbbbbbbbbbbb", Text: "Add the shop\n\nThe shop lists products."},
}}

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

func TestTheImplementPromptOfATicketInASpecHasTheTicketItsSpecTheRecentCommitsAndTheRules(t *testing.T) {
	p := prompt.Implement(inSpec(), specCommits, nil)

	inOrder(t, p,
		"# Task", "Implement Ticket #2: Add the cart",
		"<ticket>\nMake a cart.\n</ticket>",
		"<ticket-comments>", `author="alice" date="2026-11-01 09:30 UTC"`, "Use a map.", `author="bob"`, "Agreed.", "</ticket-comments>",
		"# The Spec", "Spec #1: Spec: Shopping cart", "<spec>\nA cart for the shop.\n</spec>",
		"## Tickets in this Spec",
		"- #3 Add the shop (closed)", "- #2 Add the cart (this Ticket)", "- #4 Add checkout (in progress)", "- #5 Add payment (open)",
		"# Recent commits", "since `main`", "<recent-commits>", "Add the shop\n\nThe shop lists products.", "</recent-commits>",
		"# Explore", "# Implement", "test-driven development", "# Commit",
		"# Do not close the Ticket",
		"# Rules", "`fliesswerk/ticket-2`", "`.fliesswerk/`, `compose.yaml`", "commented-out code",
		"# Done", "cannot_do", "nobody will answer questions",
	)
	if strings.Contains(p, "`gh`") {
		t.Errorf("the prompt still mentions gh, which the agent doesn't get:\n%s", p)
	}
}

func TestAStandaloneTicketGetsNoSpecSectionAndTheBaseBranchsLastCommits(t *testing.T) {
	recent := prompt.Recent{BaseBranch: "main", Commits: []git.Message{
		{SHA: "cccccccccc", Text: "Second"}, {SHA: "aaaaaaaaaa", Text: "First"},
	}}

	p := prompt.Implement(standalone(), recent, nil)

	inOrder(t, p, "Implement Ticket #7: Fix the typo", "Its full text and its comments are below",
		"<ticket>\nIt says teh.\n</ticket>",
		"# Recent commits", "The last commits of `main`", "commit ccccccc\nSecond\n\ncommit aaaaaaa\nFirst\n</recent-commits>",
		"# Explore", "# Rules")
	for _, absent := range []string{"# The Spec", "Spec it belongs to", "<ticket-comments>", "in progress"} {
		if strings.Contains(p, absent) {
			t.Errorf("a standalone Ticket's prompt has %q:\n%s", absent, p)
		}
	}
}

func TestCommandsRendersTheWholeFeedbackSectionWordedPerStep(t *testing.T) {
	c := inSpec()
	c.Commands = commands

	impl := prompt.Implement(c, specCommits, nil)
	inOrder(t, impl, "REFACTOR the code.\n\n# Feedback commands\n\nRun these before every commit and make them pass:\n\n- test: `go test ./...`\n- vet: `go vet ./...`\n\n# Commit")

	rev := prompt.Review(c, "abc123")
	inOrder(t, rev, "# Feedback commands\n\nAfter your changes, run these and make them pass:\n\n- test: `go test ./...`")

	merge := prompt.Merge(c, prompt.Merging{SpecBranch: "s", Files: []string{"a"}})
	inOrder(t, merge, "# Feedback commands\n\nAfter resolving, run these and make them pass:\n\n- test: `go test ./...`")
}

func TestWithoutCommandsThereIsNoFeedbackSection(t *testing.T) {
	p := prompt.Implement(inSpec(), specCommits, nil)

	inOrder(t, p, "REFACTOR the code.\n\n# Commit")
	if strings.Contains(p, "{{") || strings.Contains(p, "Feedback commands") {
		t.Errorf("the missing commands left something behind:\n%s", p)
	}
}

func TestStandardsReachesReviewOnly(t *testing.T) {
	c := inSpec()
	c.Standards = "docs/coding-standards.md"

	rev := prompt.Review(c, "abc123")
	inOrder(t, rev, "## 2. Is it well written?\n\nFollow the coding standards in `docs/coding-standards.md`. Read that file first. Where they disagree with the smells below, the standards win.\n\nCheck the change for these code smells.")

	for name, p := range map[string]string{
		"implement": prompt.Implement(c, specCommits, nil),
		"merge":     prompt.Merge(c, prompt.Merging{SpecBranch: "s", Files: []string{"a"}}),
	} {
		if strings.Contains(p, "coding-standards") || strings.Contains(p, "{{") {
			t.Errorf("%s got the standards:\n%s", name, p)
		}
	}

	c.Standards = ""
	rev = prompt.Review(c, "abc123")
	inOrder(t, rev, "## 2. Is it well written?\n\nCheck the change for these code smells.")
}

func TestTheReviewPromptHasTheSameContextAndWhatChanged(t *testing.T) {
	p := prompt.Review(inSpec(), "abc123")

	inOrder(t, p,
		"# Task", "Review the code changes for Ticket #2: Add the cart",
		"The Ticket's full text, its comments and the Spec it belongs to are below",
		"<ticket>\nMake a cart.\n</ticket>", "Use a map.",
		"# The Spec", "review only what this Ticket asks for", "- #4 Add checkout (in progress)",
		"# What changed", "since `abc123`", "`git log abc123..HEAD`", "`git diff abc123..HEAD`",
		"# Review", "## 1. Does it do what the Ticket asks?",
		"# Do not close the Ticket", "# Rules", "`fliesswerk/ticket-2`",
		"# Done", `report "done"`,
	)
	if strings.Contains(p, "Recent commits") || strings.Contains(p, "cannot_do") {
		t.Errorf("review got implement's sections:\n%s", p)
	}

	alone := prompt.Review(standalone(), "abc123")
	if strings.Contains(alone, "# The Spec") {
		t.Errorf("a standalone Ticket's review has a Spec section:\n%s", alone)
	}
	inOrder(t, alone, "The Ticket's full text and its comments are below")
}

func TestEveryStepIsToldWhichServicesItCanReach(t *testing.T) {
	c := standalone()
	c.Services = []sandbox.Service{
		{Name: "postgres", Addresses: []string{"postgres:5432", "localhost:5432"}},
		{Name: "worker", Addresses: []string{"worker"}},
	}
	for name, p := range map[string]string{
		"implement": prompt.Implement(c, prompt.Recent{}, nil),
		"review":    prompt.Review(c, "abc"),
		"merge":     prompt.Merge(c, prompt.Merging{SpecBranch: "s", Files: []string{"a"}}),
	} {
		for _, want := range []string{"# Services", "- postgres: `postgres:5432`, `localhost:5432`", "- worker: `worker`", "FLIESSWERK_SERVICE_<NAME>_HOST"} {
			if !strings.Contains(p, want) {
				t.Errorf("%s doesn't say %q:\n%s", name, want, p)
			}
		}
	}
}

func TestAProjectWithoutServicesGetsNoServicesSection(t *testing.T) {
	if p := prompt.Implement(standalone(), prompt.Recent{}, nil); strings.Contains(p, "# Services") {
		t.Errorf("the prompt has a Services section:\n%s", p)
	}
}
