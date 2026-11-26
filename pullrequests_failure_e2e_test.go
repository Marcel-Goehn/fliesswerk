package main

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Marcel-Goehn/fliesswerk/internal/tracker/fake"
)

// pullRequestsFail makes the fake Tracker's pull request lookup, or the
// opening of one, fail: which is "find" or "open".
func (p *runProject) pullRequestsFail(t *testing.T, which string) {
	t.Helper()
	p.patchTracker(t, map[string]any{"fail_pull_requests": which})
}

// failedInSummary is summary.json's failed.
func (p *runProject) failedInSummary(t *testing.T) []map[string]any {
	t.Helper()
	var s struct {
		Failed []map[string]any `json:"failed"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(p.logDir(t), "summary.json"))), &s); err != nil {
		t.Fatal(err)
	}
	return s.Failed
}

// lastComment is the body of the issue's last comment, "" if it has none.
func lastComment(i fake.Issue) string {
	if len(i.Comments) == 0 {
		return ""
	}
	return i.Comments[len(i.Comments)-1].Body
}

// The fake Tracker's reason for a failed pull request write or lookup.
const fakePullRequestReason = "Resource not accessible by integration"

func TestASpecWhosePullRequestCantBeOpenedNeedsAPerson(t *testing.T) {
	p := newRunProject(t, buildTestFliesswerk(t), cartIssues(), cartScript())
	p.withHooks(t, "[pull_request]\nopen = true\n[[hooks.spec_finished]]\nrun = 'echo hook-ran'\n")
	p.pullRequestsFail(t, "open")

	if code := p.run(t); code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	spec := p.issues(t)[1]
	mustContain(t, "Spec #1's last comment", lastComment(spec), "pull request", fakePullRequestReason)
	if inReview(spec) || inProgress(spec) || len(spec.Assignees) != 0 {
		t.Errorf("Spec #1 = %+v, want its Claim released and no in-review label", spec)
	}
	if !p.issues(t)[2].Closed {
		t.Errorf("#2 is open, want it closed: its code is pushed")
	}
	mustContain(t, "stdout", p.stdout, "spec_finished: started", "hook-ran")
	mustContain(t, "the summary", p.stdout, "Failed:", "#1  Spec: Shopping cart", fakePullRequestReason, "[pull_request]")
	failed := p.failedInSummary(t)
	if len(failed) != 1 || failed[0]["id"] != "#1" || failed[0]["kind"] != "pull_request" {
		t.Fatalf("summary.json's failed = %v, want Spec #1 as pull_request", failed)
	}
	mustContain(t, "its reason", failed[0]["reason"].(string), fakePullRequestReason)
	if got := p.finishedSpecs(t); len(got) != 1 || got[0]["pull_request"] != "" {
		t.Errorf("summary.json's specs_finished = %v, want #1 without a pull request", got)
	}

	// A later Run with nothing new ready doesn't try again.
	p.patchTracker(t, map[string]any{"fail_pull_requests": ""})
	if code := p.run(t); code != 0 {
		t.Fatalf("second Run: exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	if prs := p.pullRequests(t); len(prs) != 0 {
		t.Errorf("pull requests = %+v, want none: nothing retries it by itself", prs)
	}
}

func TestASpecWhosePullRequestCantBeLookedUpNeedsAPerson(t *testing.T) {
	p := newRunProject(t, buildTestFliesswerk(t), cartIssues(), cartScript())
	p.withPullRequests(t, "open = true\n")
	p.pullRequestsFail(t, "find")

	if code := p.run(t); code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	spec := p.issues(t)[1]
	mustContain(t, "Spec #1's last comment", lastComment(spec), "pull request", fakePullRequestReason)
	if inReview(spec) || inProgress(spec) {
		t.Errorf("Spec #1 = %+v, want its Claim released and no in-review label", spec)
	}
	if prs := p.openedPullRequests(t); len(prs) != 0 {
		t.Errorf("opened %+v, want none after a failed lookup", prs)
	}
	if failed := p.failedInSummary(t); len(failed) != 1 || failed[0]["id"] != "#1" || failed[0]["kind"] != "pull_request" {
		t.Errorf("summary.json's failed = %v, want Spec #1 as pull_request", failed)
	}
}

func TestAStandaloneTicketWhosePullRequestCantBeOpenedStaysOpenWithoutItsReadyLabel(t *testing.T) {
	issues := []fake.Issue{{Number: 5, Title: "Fix the typo", Labels: []string{"ready-for-agent"}}}
	p := newRunProject(t, buildTestFliesswerk(t), issues, `{"sessions": [`+approves+`, `+
		implements("Implement Ticket #5", "typo.go", "Fixed the typo.")+`]}`)
	p.withPullRequests(t, "open = true\n")
	p.pullRequestsFail(t, "open")

	if code := p.run(t); code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	five := p.issues(t)[5]
	if five.Closed || slices.Contains(five.Labels, "ready-for-agent") || inReview(five) || inProgress(five) {
		t.Errorf("#5 = %+v, want it open, without ready-for-agent, in-review or in-progress", five)
	}
	mustContain(t, "#5's last comment", lastComment(five), "pull request", fakePullRequestReason)
	if failed := p.failedInSummary(t); len(failed) != 1 || failed[0]["id"] != "#5" || failed[0]["kind"] != "pull_request" {
		t.Errorf("summary.json's failed = %v, want #5 as pull_request", failed)
	}

	// A later Run doesn't take it, nor try the pull request again.
	p.patchTracker(t, map[string]any{"fail_pull_requests": ""})
	if code := p.run(t); code != 0 {
		t.Fatalf("second Run: exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	mustNotContain(t, "the second Run's stdout", p.stdout, "Implement Ticket #5")
	if prs := p.pullRequests(t); len(prs) != 0 {
		t.Errorf("pull requests = %+v, want none", prs)
	}
}
