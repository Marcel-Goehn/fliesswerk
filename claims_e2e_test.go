package main

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/tracker/fake"
)

// claimedAt is when the claims in these tests were made.
var claimedAt = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

// claimedByAlice is a Spec alice claimed, with a ready Ticket, and a free
// standalone Ticket.
func claimedByAlice() []fake.Issue {
	return []fake.Issue{
		{Number: 1, Title: "Spec: Shopping cart", Labels: []string{"in-progress"}, Assignees: []string{"alice"}, ClaimedBy: "alice", ClaimedAt: claimedAt},
		{Number: 2, Title: "Add the cart", Labels: []string{"ready-for-agent"}, Parent: 1},
		{Number: 5, Title: "Fix the typo", Labels: []string{"ready-for-agent"}},
	}
}

const aliceClaim = "#1  Spec: Shopping cart: claimed by alice on 2026-11-02"

// implementsAny is a fake Harness that implements any Ticket.
const implementsAny = `{"sessions": [` + approves + `, {"match": "", "actions": [
	{"write": "change.txt", "content": "changed\n"},
	{"run": ["git", "add", "change.txt"]},
	{"run": ["git", "commit", "-q", "-m", "Change"]}
], "report": {"outcome": "done", "summary": "Changed it."}}]}`

func TestDryRunListsSpecsClaimedByOthersWithClaimerAndDate(t *testing.T) {
	p := newRunProject(t, buildTestKraftwerk(t), claimedByAlice(), `{"sessions": []}`)

	code := p.kwk(t, "run", "--dry-run")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0: a Spec claimed by someone else needs no fix\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	for _, want := range []string{
		"Iteration 1 would take 1 Ticket (max_parallel = 1):\n  #5  Fix the typo  (standalone)\n",
		"\nClaimed by others, skipped:\n  " + aliceClaim + "\n",
	} {
		if !strings.Contains(p.stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, p.stdout)
		}
	}
}

func TestRunSummaryListsSpecsClaimedByOthers(t *testing.T) {
	p := newRunProject(t, buildTestKraftwerk(t), claimedByAlice(), implementsAny)

	code := p.kwk(t, "run")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	if want := "Claimed by others, skipped:\n  " + aliceClaim + "\n"; !strings.Contains(p.stdout, want) {
		t.Errorf("stdout lacks %q:\n%s", want, p.stdout)
	}
	if issues := p.issues(t); issues[2].Closed || !issues[5].Closed {
		t.Errorf("closed: #2 %v, #5 %v; want only #5", issues[2].Closed, issues[5].Closed)
	}
}

func TestCheckListsSpecsClaimedByOthers(t *testing.T) {
	p := newRunProject(t, buildTestKraftwerk(t), claimedByAlice(), `{"sessions": []}`)

	code := p.kwk(t, "check")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	if want := "warning: " + aliceClaim + "; your Runs skip it\n"; !strings.Contains(p.stderr, want) {
		t.Errorf("stderr lacks %q:\n%s", want, p.stderr)
	}
}

func TestRunNLimitsTheRunToOneSpecWhichItClaimsAndReleases(t *testing.T) {
	issues := append(cartIssues(), fake.Issue{Number: 5, Title: "Fix the typo", Labels: []string{"ready-for-agent"}})
	p := newRunProject(t, buildTestKraftwerk(t), issues, implementsAny)

	code := p.kwk(t, "run", "1")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	got := p.issues(t)
	if !got[2].Closed || got[5].Closed {
		t.Fatalf("closed: #2 %v, #5 %v; want only #2", got[2].Closed, got[5].Closed)
	}
	var ops []string
	for _, w := range p.writes(t) {
		if w.Op == "claim" || w.Op == "release" {
			ops = append(ops, w.Op)
		}
	}
	if !slices.Equal(ops, []string{"claim", "release"}) || slices.Contains(got[1].Labels, "in-progress") {
		t.Fatalf("claim writes = %v, Spec #1 labels %v; want it claimed, then released when #2 closed", ops, got[1].Labels)
	}
}

func TestReleaseRemovesAClaimOnPurpose(t *testing.T) {
	p := newRunProject(t, buildTestKraftwerk(t), claimedByAlice(), `{"sessions": []}`)

	code := p.kwk(t, "release", "1")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	if want := "Released " + aliceClaim + ".\n"; !strings.Contains(p.stdout, want) {
		t.Errorf("stdout lacks %q:\n%s", want, p.stdout)
	}
	if spec := p.issues(t)[1]; slices.Contains(spec.Labels, "in-progress") || len(spec.Assignees) != 0 {
		t.Fatalf("Spec #1: labels %v, assignees %v; want the claim gone", spec.Labels, spec.Assignees)
	}

	if code := p.kwk(t, "release", "5"); code != 1 || !strings.Contains(p.stderr, "5 isn't claimed") {
		t.Fatalf("release of an unclaimed issue: exit code = %d, want 1 saying so; stderr: %s", code, p.stderr)
	}
	if code := p.kwk(t, "release"); code != 3 {
		t.Fatalf("release without a number: exit code = %d, want 3; stderr: %s", code, p.stderr)
	}
}
