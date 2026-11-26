package main

import (
	"slices"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/fliesswerk/internal/tracker/fake"
)

// handsBack is a fake Harness session that can't do the Ticket.
func handsBack(match string) string {
	return `{"match": "` + match + `", "report": {"outcome": "cannot_do", "reason": "No idea.", "summary": "Stopped."}}`
}

func inReview(i fake.Issue) bool { return slices.Contains(i.Labels, "in-review") }

func inProgress(i fake.Issue) bool { return slices.Contains(i.Labels, "in-progress") }

func TestAFinishedSpecWithAPullRequestGoesInReview(t *testing.T) {
	p := newRunProject(t, buildTestFliesswerk(t), cartIssues(), cartScript())
	p.withPullRequests(t, "open = true\n")

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	spec := p.issues(t)[1]
	if !inReview(spec) || inProgress(spec) || len(spec.Assignees) != 0 {
		t.Errorf("Spec #1 = %+v, want it in-review, without its Claim", spec)
	}
	mustContain(t, "stdout", p.stdout, "#1: in review")
}

func TestAFinishedStandaloneTicketWithAPullRequestGoesInReview(t *testing.T) {
	issues := []fake.Issue{{Number: 5, Title: "Fix the typo", Labels: []string{"ready-for-agent"}}}
	p := newRunProject(t, buildTestFliesswerk(t), issues, `{"sessions": [`+approves+`, `+
		implements("Implement Ticket #5", "typo.go", "Fixed the typo.")+`]}`)
	p.withPullRequests(t, "open = true\n")

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	if tk := p.issues(t)[5]; !inReview(tk) || inProgress(tk) {
		t.Errorf("#5 = %+v, want it in-review, without its Claim", tk)
	}
}

func TestAReusedPullRequestAlsoPutsTheSpecInReview(t *testing.T) {
	p := newRunProject(t, buildTestFliesswerk(t), cartIssues(), cartScript())
	p.withPullRequests(t, "open = true\n")
	p.withOpenedPullRequests(t, fake.PullRequest{Number: 9, URL: fake.PullURL(9), Head: cartSpecBranch, Base: "main", State: fake.PullOpen})

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	if spec := p.issues(t)[1]; !inReview(spec) || inProgress(spec) {
		t.Errorf("Spec #1 = %+v, want it in-review", spec)
	}
}

func TestWithoutOpenAFinishedSpecIsNeverInReview(t *testing.T) {
	p := newRunProject(t, buildTestFliesswerk(t), cartIssues(), cartScript())
	p.withPullRequests(t, "open = false\n")

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	if spec := p.issues(t)[1]; inReview(spec) || inProgress(spec) {
		t.Errorf("Spec #1 = %+v, want it released, and not in-review", spec)
	}
	for _, w := range p.writes(t) {
		if slices.Contains(w.Add, "in-review") {
			t.Errorf("write %+v adds in-review", w)
		}
	}
	mustNotContain(t, "stdout", p.stdout, "in review")
}

func TestClaimingASpecInReviewSwapsItBackToInProgress(t *testing.T) {
	issues := specOfTwo()[:1]
	issues[0].Labels = []string{"in-review"}
	issues = append(issues, specOfTwo()[3], fake.Issue{Number: 2, Title: "Fix CI", Labels: []string{"ready-for-agent"}, Parent: 1})
	p := newRunProject(t, buildTestFliesswerk(t), issues, `{"sessions": [`+handsBack("Implement Ticket #2")+`]}`)
	p.withPullRequests(t, "open = true\n")

	if code := p.run(t); code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	if spec := p.issues(t)[1]; inReview(spec) || !inProgress(spec) {
		t.Errorf("Spec #1 = %+v, want it in-progress only", spec)
	}
}

func TestClaimingAStandaloneTicketInReviewSwapsItBackToInProgress(t *testing.T) {
	issues := []fake.Issue{{Number: 5, Title: "Fix the typo", Labels: []string{"in-review", "ready-for-agent"}}}
	p := newRunProject(t, buildTestFliesswerk(t), issues, `{"sessions": [`+handsBack("Implement Ticket #5")+`]}`)
	p.withPullRequests(t, "open = true\n")

	if code := p.run(t); code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	if tk := p.issues(t)[5]; inReview(tk) || !inProgress(tk) {
		t.Errorf("#5 = %+v, want it in-progress only", tk)
	}
}

func TestCheckWithPullRequestsNeedsTheInReviewLabel(t *testing.T) {
	p := checkProject(t, "[pull_request]\nopen = true\n")
	p.withLabels(t, "ready-for-agent", "in-progress", "needs-investigation")

	if code := p.fwk(t, "check"); code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, exitConfigError, p.stdout, p.stderr)
	}
	mustContain(t, "stderr", p.stderr, "in-review", "fliesswerk init",
		"create the label in-review (Its work waits in an open pull request)")
}

func TestCheckWithoutPullRequestsDoesntNeedTheInReviewLabel(t *testing.T) {
	p := checkProject(t, "[pull_request]\nopen = false\n")
	p.withLabels(t, "ready-for-agent", "in-progress", "needs-investigation")

	if code := p.fwk(t, "check"); code != exitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	if strings.Contains(p.stderr, "in-review") {
		t.Errorf("stderr names in-review:\n%s", p.stderr)
	}
}

func TestARunWithPullRequestsAndNoInReviewLabelStopsBeforeClaimingAnything(t *testing.T) {
	p := newRunProject(t, buildTestFliesswerk(t), cartIssues(), cartScript())
	p.withPullRequests(t, "open = true\n")
	p.withLabels(t, "ready-for-agent", "in-progress", "needs-investigation")

	if code := p.run(t); code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, exitConfigError, p.stdout, p.stderr)
	}
	if w := p.writes(t); len(w) > 0 {
		t.Errorf("writes = %+v, want none", w)
	}
	mustContain(t, "stderr", p.stderr, "create the label in-review (Its work waits in an open pull request)")
}

func TestInitCreatesTheInReviewLabelByItsConfiguredName(t *testing.T) {
	config := teamConfig + "in_review_label = \"waiting\"\n\n[pull_request]\nopen = true\n"
	p := teamProject(t, map[string]string{"config.toml": config, "config.local.toml": myDocker})
	p.withLabels(t, "ready-for-agent", "in-progress", "needs-investigation")

	p.init(t, p.initEnv(t))

	if got, want := p.createdLabels(t), []string{"waiting: Its work waits in an open pull request"}; !slices.Equal(got, want) {
		t.Errorf("created labels = %q, want %q", got, want)
	}
}
