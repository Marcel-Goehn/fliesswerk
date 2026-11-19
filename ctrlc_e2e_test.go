//go:build unix

package main

import (
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Marcel-Goehn/fliesswerk/internal/tracker/fake"
)

// pressCtrlC signals the Run's whole process group, as a terminal does.
func pressCtrlC(t *testing.T, r *liveRun) {
	t.Helper()
	if err := syscall.Kill(-r.cmd.Process.Pid, syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
}

func TestTheFirstCtrlCLetsTheTicketInFlightFinishAndStartsNothingNew(t *testing.T) {
	bin := buildTestFliesswerk(t)
	issues := append(cartIssues(), fake.Issue{Number: 3, Title: "Cart page", Labels: []string{"ready-for-agent"}, Parent: 1})
	p := newRunProject(t, bin, issues, `{"sessions": [`+approves+`, {"match": "Implement Ticket #2", "actions": [
		{"write": "started.txt", "content": "x"},
		{"sleep": "2s"},
		{"write": "cart/cart.go", "content": "package cart\n"},
		{"run": ["git", "add", "cart/cart.go"]},
		{"run": ["git", "commit", "-q", "-m", "Add the cart package"]}
	], "report": {"outcome": "done", "summary": "Added the cart package."}}]}`)
	harnessLog := filepath.Join(t.TempDir(), "harness.jsonl")
	p.extraEnv = []string{"FWK_FAKE_HARNESS_LOG=" + harnessLog}

	r := p.start(t, &syscall.SysProcAttr{Setpgid: true})
	marker := filepath.Join(p.worktreePath("ticket-2"), "started.txt")
	until(t, r, "the Step to start", func() bool { return exists(marker) })
	pressCtrlC(t, r)
	code := r.wait(t)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout: %s\nstderr: %s", code, r.stdout, r.stderr)
	}
	issuesAfter := p.issues(t)
	if !issuesAfter[2].Closed {
		t.Errorf("#2 wasn't finished\nstdout: %s\nstderr: %s", r.stdout, r.stderr)
	}
	if issuesAfter[3].Closed || len(issuesAfter[3].Comments) != 0 {
		t.Errorf("#3 = %+v, want it never started", issuesAfter[3])
	}
	if n := len(harnessCalls(t, harnessLog)); n != 2 {
		t.Errorf("the Harness ran %d times, want #2's implement and review only", n)
	}
	if !strings.Contains(r.stdout.String(), "Ctrl-C") {
		t.Errorf("the summary doesn't say why the Run stopped:\n%s", r.stdout)
	}
}

func TestTheSecondCtrlCKillsTheStepDiscardsTheWorkAndKeepsTheClaim(t *testing.T) {
	bin := buildTestFliesswerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": [{"match": "Implement Ticket #2", "actions": [
		{"write": "started.txt", "content": "x"}, {"sleep": "1m"}]}]}`)

	begin := time.Now()
	r := p.start(t, &syscall.SysProcAttr{Setpgid: true})
	marker := filepath.Join(p.worktreePath("ticket-2"), "started.txt")
	until(t, r, "the Step to start", func() bool { return exists(marker) })
	pressCtrlC(t, r)
	until(t, r, "the first Ctrl-C to land", func() bool { return strings.Contains(r.stdout.String(), "Ctrl-C again") })
	pressCtrlC(t, r)
	code := r.wait(t)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout: %s\nstderr: %s", code, r.stdout, r.stderr)
	}
	if took := time.Since(begin); took > 30*time.Second {
		t.Errorf("the Run took %s: the Step wasn't killed", took)
	}
	issues := p.issues(t)
	if i := issues[2]; i.Closed || len(i.Comments) != 0 || !slices.Contains(i.Labels, "ready-for-agent") || slices.Contains(i.Labels, "needs-investigation") {
		t.Errorf("#2 = %+v, want it as it was: open, ready, no comment", i)
	}
	if !slices.Contains(issues[1].Labels, "in-progress") {
		t.Errorf("Spec #1 = %+v, want its claim kept", issues[1])
	}
	if got := p.writes(t); len(got) != 1 || got[0].Op != "claim" {
		t.Errorf("writes = %+v, want only the claim", got)
	}
	if exists(p.worktreePath("ticket-2")) || gitIn(t, p.work, "branch", "--list", "fliesswerk/ticket-*") != "" {
		t.Errorf("the work in flight wasn't discarded\nstdout: %s", r.stdout)
	}
	if exists(p.lockPath()) {
		t.Error("the lock wasn't released")
	}
}
