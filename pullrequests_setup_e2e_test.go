package main

import (
	"strings"
	"testing"
)

// withoutPullRequestPermission makes the fake Tracker's token one that
// can't open pull requests.
func (p *runProject) withoutPullRequestPermission(t *testing.T) {
	t.Helper()
	p.patchTracker(t, map[string]any{"no_pull_request_permission": true})
}

func TestCheckFailsNamingThePermissionWhenTheTokenCantOpenPullRequests(t *testing.T) {
	p := checkProject(t, "[pull_request]\nopen = true\n")
	p.withoutPullRequestPermission(t)

	code := p.fwk(t, "check")

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, exitConfigError, p.stdout, p.stderr)
	}
	mustContain(t, "stderr", p.stderr, "pull requests", "Pull requests: Read and write")
}

func TestCheckPassesWhenTheTokenCanOpenPullRequests(t *testing.T) {
	p := checkProject(t, "[pull_request]\nopen = true\n")

	if code := p.fwk(t, "check"); code != exitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	mustContain(t, "stdout", p.stdout, "fliesswerk check: pull requests ok")
}

func TestCheckDoesNotAskAboutPullRequestsWhenTheyAreOff(t *testing.T) {
	p := checkProject(t, "[pull_request]\nopen = false\n")
	p.withoutPullRequestPermission(t)

	if code := p.fwk(t, "check"); code != exitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	if strings.Contains(p.stdout, "pull requests ok") {
		t.Errorf("checked pull requests with them off:\n%s", p.stdout)
	}
}

func TestARunWhoseTokenCantOpenPullRequestsStopsBeforeClaimingAnything(t *testing.T) {
	p := newRunProject(t, buildTestFliesswerk(t), cartIssues(), cartScript())
	p.withPullRequests(t, "open = true\n")
	p.withoutPullRequestPermission(t)

	code := p.run(t)

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, exitConfigError, p.stdout, p.stderr)
	}
	if w := p.writes(t); len(w) > 0 {
		t.Errorf("writes = %+v, want none: the Run should stop before its first claim", w)
	}
	mustContain(t, "stderr", p.stderr, "Pull requests: Read and write")
}
