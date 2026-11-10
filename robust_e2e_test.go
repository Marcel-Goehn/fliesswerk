package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/tracker/fake"
)

// lockPath is where a Run keeps its lock.
func (p *runProject) lockPath() string {
	return filepath.Join(p.work, ".kraftwerk", "state", "lock")
}

// deadPID is the PID of a process that has already ended.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$") //nolint:gosec // this test binary itself
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

func TestASecondRunInTheSameCheckoutIsRefusedNamingTheHoldingPID(t *testing.T) {
	bin := buildTestKraftwerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": []}`)
	holder := strconv.Itoa(os.Getpid()) // this test is a live process
	writeFile(t, p.lockPath(), holder+"\n")

	code := p.run(t)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	if !strings.Contains(p.stderr, holder) {
		t.Errorf("stderr doesn't name PID %s:\n%s", holder, p.stderr)
	}
	p.untouched(t)
	if got, err := os.ReadFile(p.lockPath()); err != nil || strings.TrimSpace(string(got)) != holder {
		t.Errorf("the holder's lock changed: %q, %v", got, err)
	}
}

func TestAStaleLockIsTakenOverAndReleasedAtTheEnd(t *testing.T) {
	bin := buildTestKraftwerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": [`+approves+`, `+implementsCart("Implement Ticket #2", `package cart\n`)+`]}`)
	dead := deadPID(t)
	writeFile(t, p.lockPath(), fmt.Sprintf("%d\n", dead))

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	if !p.issues(t)[2].Closed {
		t.Errorf("#2 wasn't closed\nstdout: %s", p.stdout)
	}
	if !strings.Contains(p.stdout, strconv.Itoa(dead)) {
		t.Errorf("stdout doesn't say whose lock was taken over:\n%s", p.stdout)
	}
	if _, err := os.Stat(p.lockPath()); !os.IsNotExist(err) {
		t.Errorf("the lock is still there after the Run: %v", err)
	}
}

// syncBuffer is an output a test can read while the Run still writes it.
type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// liveRun is a `kraftwerk run` still going.
type liveRun struct {
	cmd            *exec.Cmd
	stdout, stderr *syncBuffer
}

// start starts a Run, with attr setting up its process.
func (p *runProject) start(t *testing.T, attr *syscall.SysProcAttr) *liveRun {
	t.Helper()
	cmd := exec.Command(p.bin, "run")
	cmd.Dir, cmd.Env, cmd.SysProcAttr = p.work, p.env(t), attr
	r := &liveRun{cmd: cmd, stdout: &syncBuffer{}, stderr: &syncBuffer{}}
	cmd.Stdout, cmd.Stderr = r.stdout, r.stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return r
}

// wait is the Run's exit code once it has ended.
func (r *liveRun) wait(t *testing.T) int {
	t.Helper()
	err := r.cmd.Wait()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatal(err)
	}
	return r.cmd.ProcessState.ExitCode()
}

// until waits for what to hold.
func until(t *testing.T, r *liveRun, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(30 * time.Second); !ok(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("still waiting for %s\nstdout: %s\nstderr: %s", what, r.stdout, r.stderr)
		}
	}
}

// setIssues replaces what the fake Tracker holds.
func (p *runProject) setIssues(t *testing.T, issues []fake.Issue) {
	t.Helper()
	state, err := json.Marshal(map[string]any{"ready_label": "ready-for-agent", "issues": issues})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, p.trackerPath, string(state))
}

// worktreePath is a Factory worktree in the user's checkout.
func (p *runProject) worktreePath(name string) string {
	return filepath.Join(p.work, ".kraftwerk", "state", "worktrees", name)
}

func TestARunCleansUpAfterACrashedRunAndKeepsSpecBranches(t *testing.T) {
	bin := buildTestKraftwerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": [{"match": "Implement Ticket #2", "actions": [
		{"write": "started.txt", "content": "x"}, {"sleep": "5s"}]}]}`)
	// A standalone Ticket's branch has the place of a Spec branch, and
	// starts just like a Ticket branch does.
	standalone := "kraftwerk/ticket-7-fix-the-typo"
	gitIn(t, p.work, "branch", standalone)
	spec := "kraftwerk/spec-1-spec-shopping-cart"

	crashed := p.start(t, nil)
	marker := filepath.Join(p.worktreePath("ticket-2"), "started.txt")
	until(t, crashed, "the Step to start", func() bool { return exists(marker) })
	if err := crashed.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	crashed.wait(t)
	if !exists(p.lockPath()) || gitIn(t, p.work, "branch", "--list", "kraftwerk/ticket-2") == "" {
		t.Fatal("the crash left no lock or no Ticket branch to clean up")
	}

	// Nothing is ready now, so only the cleanup can remove the leftovers.
	p.setIssues(t, nil)
	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	if exists(p.worktreePath("ticket-2")) || exists(p.worktreePath(filepath.Base(spec))) {
		t.Errorf("the crashed Run's worktrees are left\nstdout: %s", p.stdout)
	}
	if got := gitIn(t, p.work, "worktree", "list", "--porcelain"); strings.Count(got, "worktree ") != 1 {
		t.Errorf("git still knows the crashed Run's worktrees:\n%s", got)
	}
	if got := gitIn(t, p.work, "branch", "--list", "kraftwerk/ticket-2"); got != "" {
		t.Errorf("the Ticket branch is left: %q", got)
	}
	for _, branch := range []string{spec, standalone} {
		if gitIn(t, p.work, "branch", "--list", branch) == "" {
			t.Errorf("Spec branch %s was removed", branch)
		}
	}
}

// patchTracker sets top-level keys of the fake Tracker's file.
func (p *runProject) patchTracker(t *testing.T, patch map[string]any) {
	t.Helper()
	data, err := os.ReadFile(p.trackerPath)
	if err != nil {
		t.Fatal(err)
	}
	st := map[string]any{}
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	maps.Copy(st, patch)
	out, err := json.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, p.trackerPath, string(out))
}

// ranOnce is a project whose Ticket was implemented by a Run that couldn't
// reach the Tracker, so all its writes are queued.
func ranOnce(t *testing.T) (p *runProject, harnessLog string) {
	t.Helper()
	bin := buildTestKraftwerk(t)
	p = newRunProject(t, bin, cartIssues(), `{"sessions": [`+approves+`, `+implementsCart("Implement Ticket #2", `package cart\n`)+`]}`)
	harnessLog = filepath.Join(t.TempDir(), "harness.jsonl")
	p.extraEnv = []string{"KWK_FAKE_HARNESS_LOG=" + harnessLog}
	p.patchTracker(t, map[string]any{"fail_writes": 1000})

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	if !strings.Contains(p.stdout, "Writes queued for replay") || !strings.Contains(p.stdout, "close #2") {
		t.Fatalf("the summary doesn't list the queued close:\n%s", p.stdout)
	}
	if p.issues(t)[2].Closed {
		t.Fatal("#2 was closed though every write failed")
	}
	return p, harnessLog
}

func TestFailedWritesAreReplayedBeforeTheNextSnapshotSoNoTicketRunsTwice(t *testing.T) {
	p, harnessLog := ranOnce(t)
	p.patchTracker(t, map[string]any{"fail_writes": 0, "fail_snapshot": true})

	code := p.run(t)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2: a failing Snapshot stops the Run\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	if !strings.Contains(p.stderr, "the Snapshot failed") {
		t.Errorf("stderr doesn't say why:\n%s", p.stderr)
	}
	issue := p.issues(t)[2]
	if !issue.Closed || len(issue.Comments) != 1 {
		t.Errorf("#2 = %+v, want it closed by the replay, before the Snapshot", issue)
	}
	if spec := p.issues(t)[1]; slices.Contains(spec.Labels, "in-progress") {
		t.Errorf("Spec #1 = %+v, want the replayed release after the replayed claim", spec)
	}
	if n := len(harnessCalls(t, harnessLog)); n != 2 {
		t.Errorf("the Harness ran %d times, want only the first Run's implement and review", n)
	}
	if strings.Contains(p.stdout, "Writes queued for replay") {
		t.Errorf("the summary still lists queued writes:\n%s", p.stdout)
	}
}

func TestATicketWhoseCloseIsStillQueuedDoesNotRunAgain(t *testing.T) {
	p, harnessLog := ranOnce(t)

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	if n := len(harnessCalls(t, harnessLog)); n != 2 {
		t.Errorf("the Harness ran %d times, want the Ticket not to run twice", n)
	}
	if !strings.Contains(p.stdout, "close #2") {
		t.Errorf("the summary lost the queued close:\n%s", p.stdout)
	}
}

func TestAFailingSnapshotStopsTheRunWithExit2(t *testing.T) {
	bin := buildTestKraftwerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": []}`)
	p.patchTracker(t, map[string]any{"fail_snapshot": true})

	if code := p.run(t); code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	if !strings.Contains(p.stderr, "the Snapshot failed") {
		t.Errorf("stderr doesn't say why:\n%s", p.stderr)
	}
}

func TestARejectedPushStopsThatSpecOnlyAndExplainsTheFix(t *testing.T) {
	bin := buildTestKraftwerk(t)
	spec := "kraftwerk/spec-1-spec-shopping-cart"
	issues := append(cartIssues(),
		fake.Issue{Number: 3, Title: "Cart page", Labels: []string{"ready-for-agent"}, Parent: 1},
		fake.Issue{Number: 10, Title: "Spec: Search", Labels: []string{"ready-for-agent"}},
		fake.Issue{Number: 11, Title: "Add search", Labels: []string{"ready-for-agent"}, Parent: 10})
	// While #2 is implemented, someone else pushes to the Spec branch.
	p := newRunProject(t, bin, issues, `{"sessions": [`+approves+`, {"match": "Implement Ticket #2", "actions": [
		{"run": ["git", "commit", "-q", "--allow-empty", "-m", "Someone else's commit"]},
		{"run": ["git", "push", "-q", "origin", "HEAD:refs/heads/`+spec+`"]},
		{"run": ["git", "reset", "-q", "--hard", "HEAD~1"]},
		{"write": "cart/cart.go", "content": "package cart\n"},
		{"run": ["git", "add", "cart/cart.go"]},
		{"run": ["git", "commit", "-q", "-m", "Add the cart package"]}
	], "report": {"outcome": "done", "summary": "Added the cart package."}},
	`+implementsCart("Implement Ticket #11", `package search\n`)+`]}`)

	code := p.run(t)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	after := p.issues(t)
	for _, n := range []int{2, 3} {
		if i := after[n]; i.Closed || len(i.Comments) != 0 || !slices.Contains(i.Labels, "ready-for-agent") {
			t.Errorf("#%d = %+v, want it left untouched", n, i)
		}
	}
	if !after[11].Closed {
		t.Errorf("#11 wasn't closed: the other Spec should carry on\nstdout: %s", p.stdout)
	}
	if got := gitIn(t, p.remote, "log", "-1", "--format=%s", spec); got != "Someone else's commit" {
		t.Errorf("the remote Spec branch ends in %q, want the human's commit kept", got)
	}
	if !strings.Contains(p.stdout, "git fetch origin "+spec) {
		t.Errorf("the summary doesn't explain the fix:\n%s", p.stdout)
	}
}

func TestExitCodesMatchTheSpec(t *testing.T) {
	bin := buildTestKraftwerk(t)
	const repoTable = "[tracker]\nrepo = \"acme/shop\"\n"
	twoTickets := append(cartIssues(), fake.Issue{Number: 3, Title: "Cart page", Labels: []string{"ready-for-agent"}, Parent: 1})
	textOnly := []fake.Issue{{Number: 5, Title: "Blocked in text only", Labels: []string{"ready-for-agent"}, TextOnlyBlockers: []int{9}}}
	script := `{"sessions": [` + approves + `, ` + implementsCart("Implement Ticket", `package cart\n`) + `]}`

	for name, tt := range map[string]struct {
		issues []fake.Issue
		config string
		want   int
	}{
		"0: max_iterations reached":          {twoTickets, "version = 1\nmax_iterations = 1\n" + repoTable, 0},
		"1: a Ticket is skipped until a fix": {textOnly, "version = 1\n" + repoTable, 1},
		"2: run_start failed":                {cartIssues(), "version = 1\n" + repoTable + "[[hooks.run_start]]\nrun = \"exit 1\"\n", 2},
		"3: the config is not valid":         {cartIssues(), "version = 1\nmax_iteration = 1\n" + repoTable, 3},
	} {
		t.Run(name, func(t *testing.T) {
			p := newRunProject(t, bin, tt.issues, script)
			writeFile(t, filepath.Join(p.work, ".kraftwerk", "config.toml"), tt.config)

			if code := p.run(t); code != tt.want {
				t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, tt.want, p.stdout, p.stderr)
			}
		})
	}
}
