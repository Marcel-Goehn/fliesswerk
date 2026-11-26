package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/fliesswerk/internal/tracker/fake"
)

// optedOut gives issue n of issues the no-pr label.
func optedOut(issues []fake.Issue, n int) []fake.Issue {
	for i := range issues {
		if issues[i].Number == n {
			issues[i].Labels = append(issues[i].Labels, "no-pr")
		}
	}
	return issues
}

func TestASpecWithTheNoPRLabelFinishesWithoutAPullRequest(t *testing.T) {
	p := newRunProject(t, buildTestFliesswerk(t), optedOut(cartIssues(), 1), cartScript())
	p.withHooks(t, "[pull_request]\nopen = true\n[[hooks.spec_finished]]\nrun = 'echo hook-ran'\n")
	// An open one would be reused, if the Factory looked it up.
	p.withOpenedPullRequests(t, fake.PullRequest{Number: 9, URL: fake.PullURL(9), Head: cartSpecBranch, Base: "main", State: fake.PullOpen})

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	if prs := p.openedPullRequests(t); len(prs) != 0 {
		t.Errorf("opened %+v, want none", prs)
	}
	mustNotContain(t, "stdout", p.stdout, "pull request", fake.PullURL(9), "in review")
	if spec := p.issues(t)[1]; inReview(spec) || inProgress(spec) {
		t.Errorf("Spec #1 = %+v, want it released, and not in-review", spec)
	}
	mustContain(t, "stdout", p.stdout, "hook-ran")
	if got := p.finishedSpecs(t); len(got) != 1 || got[0]["pull_request"] != "" {
		t.Errorf("summary.json's specs_finished = %v, want #1 without a pull request", got)
	}
}

func TestAStandaloneTicketWithTheNoPRLabelClosesOnLanding(t *testing.T) {
	issues := []fake.Issue{{Number: 5, Title: "Fix the typo", Labels: []string{"ready-for-agent", "no-pr"}}}
	p := newRunProject(t, buildTestFliesswerk(t), issues, `{"sessions": [`+approves+`, `+
		implements("Implement Ticket #5", "typo.go", "Fixed the typo.")+`]}`)
	p.withPullRequests(t, "open = true\n")

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	if prs := p.openedPullRequests(t); len(prs) != 0 {
		t.Errorf("opened %+v, want none", prs)
	}
	five := p.issues(t)[5]
	if !five.Closed || inReview(five) {
		t.Errorf("#5 = %+v, want it closed on landing, and not in-review", five)
	}
	mustContain(t, "#5's last comment", five.Comments[len(five.Comments)-1].Body, "Fixed the typo.", "Closed by the Factory")
}

func TestTheNoPRLabelGoesByItsConfiguredName(t *testing.T) {
	issues := []fake.Issue{{Number: 5, Title: "Fix the typo", Labels: []string{"ready-for-agent", "quick-fix"}}}
	p := newRunProject(t, buildTestFliesswerk(t), issues, `{"sessions": [`+approves+`, `+
		implements("Implement Ticket #5", "typo.go", "Fixed the typo.")+`]}`)
	p.withPullRequests(t, "open = true\n[tracker]\nno_pr_label = \"quick-fix\"\n")

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	if prs := p.openedPullRequests(t); len(prs) != 0 {
		t.Errorf("opened %+v, want none", prs)
	}
	if five := p.issues(t)[5]; !five.Closed {
		t.Errorf("#5 is open, want it closed on landing")
	}
}

func TestWithoutOpenTheNoPRLabelChangesNothing(t *testing.T) {
	issues := []fake.Issue{{Number: 5, Title: "Fix the typo", Labels: []string{"ready-for-agent", "no-pr"}}}
	p := newRunProject(t, buildTestFliesswerk(t), issues, `{"sessions": [`+approves+`, `+
		implements("Implement Ticket #5", "typo.go", "Fixed the typo.")+`]}`)
	p.withPullRequests(t, "open = false\n")
	p.withLabels(t, "ready-for-agent", "in-progress", "needs-investigation")

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	if five := p.issues(t)[5]; !five.Closed {
		t.Errorf("#5 is open, want it closed on landing")
	}
	mustNotContain(t, "stdout and stderr", p.stdout+p.stderr, "no-pr", "pull request")
}

func TestADryRunSaysNoPullRequestForAStandaloneTicketWithTheNoPRLabel(t *testing.T) {
	p := newRunProject(t, buildTestFliesswerk(t), optedOut(dryRunIssues(), 5), `{"sessions": []}`)
	p.withPullRequests(t, "open = true\n")

	if code := p.run(t, "--dry-run"); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	mustContain(t, "stdout", p.stdout, "#1  Spec: Shopping cart: would open a pull request into main")
	mustNotContain(t, "stdout", p.stdout, "Fix the typo: would")
}

func TestADryRunSaysNoPullRequestForASpecWithTheNoPRLabel(t *testing.T) {
	p := newRunProject(t, buildTestFliesswerk(t), optedOut(dryRunIssues(), 1), `{"sessions": []}`)
	p.withPullRequests(t, "open = true\n")

	if code := p.run(t, "--dry-run"); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	mustContain(t, "stdout", p.stdout, "#5  Fix the typo: would open a pull request into main")
	mustNotContain(t, "stdout", p.stdout, "Shopping cart: would")
}

func TestCheckWithPullRequestsNeedsTheNoPRLabel(t *testing.T) {
	p := checkProject(t, "[pull_request]\nopen = true\n")
	p.withLabels(t, "ready-for-agent", "in-progress", "needs-investigation", "in-review")

	if code := p.fwk(t, "check"); code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, exitConfigError, p.stdout, p.stderr)
	}
	mustContain(t, "stderr", p.stderr, "no-pr", "fliesswerk init",
		"create the label no-pr (Fliesswerk opens no pull request for it)")
}

func TestCheckWithoutPullRequestsDoesntNeedTheNoPRLabel(t *testing.T) {
	p := checkProject(t, "[pull_request]\nopen = false\n")
	p.withLabels(t, "ready-for-agent", "in-progress", "needs-investigation")

	if code := p.fwk(t, "check"); code != exitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	if strings.Contains(p.stdout+p.stderr, "no-pr") {
		t.Errorf("check names no-pr:\nstdout: %s\nstderr: %s", p.stdout, p.stderr)
	}
}

func TestARunWithPullRequestsAndNoNoPRLabelStopsBeforeClaimingAnything(t *testing.T) {
	p := newRunProject(t, buildTestFliesswerk(t), cartIssues(), cartScript())
	p.withPullRequests(t, "open = true\n")
	p.withLabels(t, "ready-for-agent", "in-progress", "needs-investigation", "in-review")

	if code := p.run(t); code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, exitConfigError, p.stdout, p.stderr)
	}
	if w := p.writes(t); len(w) > 0 {
		t.Errorf("writes = %+v, want none", w)
	}
	mustContain(t, "stderr", p.stderr, "create the label no-pr (Fliesswerk opens no pull request for it)")
}

func TestInitCreatesTheNoPRLabelByItsConfiguredName(t *testing.T) {
	config := teamConfig + "no_pr_label = \"quick-fix\"\n\n[pull_request]\nopen = true\n"
	p := teamProject(t, map[string]string{"config.toml": config, "config.local.toml": myDocker})
	p.withLabels(t, "ready-for-agent", "in-progress", "needs-investigation", "in-review")

	p.init(t, p.initEnv(t))

	if got, want := p.createdLabels(t), []string{"quick-fix: Fliesswerk opens no pull request for it"}; !slices.Equal(got, want) {
		t.Errorf("created labels = %q, want %q", got, want)
	}
}
