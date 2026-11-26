package main

import (
	"slices"
	"strings"
	"testing"
)

// withoutPullRequestPermission makes the fake Tracker's token one that
// can't open pull requests.
func (p *runProject) withoutPullRequestPermission(t *testing.T) {
	t.Helper()
	p.patchTracker(t, map[string]any{"no_pull_request_permission": true})
}

// prAnswer is where acceptAll answers "Open a pull request when a Spec
// finishes?".
const prAnswer = 4

func TestInitAsksWhetherToOpenPullRequestsAndWritesTheAnswer(t *testing.T) {
	for name, tt := range map[string]struct {
		answer     string
		want       bool
		permission string
	}{
		"yes":                {"y", true, "Pull requests to Read and write"},
		"no, the suggestion": {"", false, "Pull requests to Read-only"},
	} {
		t.Run(name, func(t *testing.T) {
			p := initProject(t, nodePostgres())
			answers := slices.Clone(acceptAll)
			answers[prAnswer] = tt.answer

			p.init(t, p.initEnv(t), p.thenSandbox(t, answers, containerOnDocker...)...)

			mustContain(t, "stdout", p.stdout, "Open a pull request when a Spec finishes? [y/N]")
			if got := valueAt(p.config(t), "pull_request", "open"); got != tt.want {
				t.Errorf("pull_request.open = %v, want %v", got, tt.want)
			}
			if !strings.Contains(p.stdout, tt.permission) {
				t.Errorf("the token walkthrough doesn't say %q:\n%s", tt.permission, p.stdout)
			}
		})
	}
}

func TestInitRerunKeepsThePullRequestAnswer(t *testing.T) {
	config := teamConfig + "\n[pull_request]\nopen = true\n"
	p := teamProject(t, map[string]string{"config.toml": config})

	p.init(t, p.initEnv(t), mine(viaGateway(fakeGateway(t))...)...)

	if strings.Contains(p.stdout, "Open a pull request") {
		t.Errorf("asked again:\n%s", p.stdout)
	}
	if got := p.fliesswerkFile(t, "config.toml"); got != config {
		t.Errorf("config.toml changed: %q", got)
	}
	mustContain(t, "stdout", p.stdout, "Pull requests to Read and write", "fliesswerk check: pull requests ok")
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
