package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Marcel-Goehn/fliesswerk/internal/tracker/fake"
)

// The Hooks' command lines are portable between sh and cmd.exe, whose echo
// keeps a space before a redirection: hence `echo one>>order.txt`.

// withHooks makes hooks, TOML tables, part of the project's config.
func (p *runProject) withHooks(t *testing.T, hooks string) {
	t.Helper()
	writeFile(t, filepath.Join(p.work, ".fliesswerk", "config.local.toml"), noSandbox+hooks)
}

// wordsIn is the whitespace-separated words of the file, none if it
// doesn't exist.
func wordsIn(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // a test's own temp file
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(data))
}

func TestHooksRunInDeclaredOrderAndTheFirstFailureStopsTheRunButNotRunEnd(t *testing.T) {
	bin := buildTestFliesswerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": []}`)
	p.withHooks(t, `
[[hooks.run_start]]
run = 'echo one>>order.txt'
[[hooks.run_start]]
run = 'echo two>>order.txt'
[[hooks.run_start]]
run = 'git --no-such-flag'
[[hooks.run_start]]
run = 'echo three>>order.txt'
[[hooks.run_end]]
run = 'echo end>>order.txt'
`)

	code := p.run(t)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	if got, want := wordsIn(t, filepath.Join(p.work, "order.txt")), []string{"one", "two", "end"}; !slices.Equal(got, want) {
		t.Errorf("Hooks ran as %v, want %v", got, want)
	}
	if !strings.Contains(p.stderr, "run_start") || !strings.Contains(p.stderr, "git --no-such-flag") {
		t.Errorf("stderr doesn't name the failed Hook:\n%s", p.stderr)
	}
	if !strings.Contains(p.stdout, "no-such-flag") {
		t.Errorf("stdout lacks the Hook's output:\n%s", p.stdout)
	}
	p.untouched(t)
}

func TestRunStartWaitsForTheProjectToBeSetUpButRunEndStillRuns(t *testing.T) {
	bin := buildTestFliesswerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": []}`)
	writeFile(t, filepath.Join(p.work, ".fliesswerk", ".gitignore"), "config.local.toml\nlogs/\n.env\n")
	p.withHooks(t, `
[[hooks.run_start]]
run = 'echo start>>order.txt'
[[hooks.run_end]]
run = 'echo end>>order.txt'
`)

	if code := p.run(t); code != 3 {
		t.Fatalf("exit code = %d, want 3\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	if got, want := wordsIn(t, filepath.Join(p.work, "order.txt")), []string{"end"}; !slices.Equal(got, want) {
		t.Errorf("Hooks ran as %v, want %v", got, want)
	}
}

// pause is a command line that waits for about secs seconds. A Run's PATH
// holds little, so it names the program in full.
func pause(secs int) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(`%%SystemRoot%%\System32\ping.exe -n %d 127.0.0.1 >nul`, secs+1)
	}
	return fmt.Sprintf("/bin/sleep %d", secs)
}

// writesLater is a command line that starts a child shell which, after
// about secs seconds, appends to file. Only a stop that reaches the Hook's
// whole process tree keeps the file from appearing.
func writesLater(secs int, file string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(`%%ComSpec%% /c "%s & echo finished>>%s"`, pause(secs), file)
	}
	return fmt.Sprintf(`/bin/sh -c "%s; echo finished>>%s"`, pause(secs), file)
}

// afterChildWouldHaveWritten waits until a writesLater(4, …) started
// after start would have written its file.
func afterChildWouldHaveWritten(start time.Time) {
	time.Sleep(time.Until(start.Add(6 * time.Second)))
}

func TestAParallelGroupStopsItsSiblingsOnTheFirstFailureAndReportsEveryOutput(t *testing.T) {
	bin := buildTestFliesswerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": []}`)
	p.withHooks(t, `
[[hooks.run_start]]
parallel = [
  'echo sibling-started&& `+writesLater(4, "sibling.txt")+`',
  '`+pause(1)+`&& echo about-to-fail&& git --no-such-flag',
]
`)

	start := time.Now()
	code := p.run(t)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	for _, want := range []string{"sibling-started", "stopped", "about-to-fail", "no-such-flag"} {
		if !strings.Contains(p.stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, p.stdout)
		}
	}
	if !strings.Contains(p.stderr, "git --no-such-flag") || strings.Contains(p.stderr, "sibling-started") {
		t.Errorf("stderr should name the command that failed first, not its sibling:\n%s", p.stderr)
	}
	afterChildWouldHaveWritten(start)
	if got := wordsIn(t, filepath.Join(p.work, "sibling.txt")); got != nil {
		t.Errorf("the sibling ran to its end: %v", got)
	}
}

func TestAHookStillRunningAtItsTimeoutIsStopped(t *testing.T) {
	bin := buildTestFliesswerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": []}`)
	p.withHooks(t, `
[[hooks.run_start]]
run = 'echo started&& `+writesLater(4, "slow.txt")+`'
timeout = "1s"
`)

	start := time.Now()
	code := p.run(t)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	if !strings.Contains(p.stderr, "still running after 1s") {
		t.Errorf("stderr lacks the timeout:\n%s", p.stderr)
	}
	if !strings.Contains(p.stdout, "started") {
		t.Errorf("stdout lacks what the Hook printed before its timeout:\n%s", p.stdout)
	}
	afterChildWouldHaveWritten(start)
	if got := wordsIn(t, filepath.Join(p.work, "slow.txt")); got != nil {
		t.Errorf("the Hook ran to its end: %v", got)
	}
}

func TestSandboxCreatedRunsInTheTicketsWorktreeBeforeImplementAndHostEntriesInTheProjectRoot(t *testing.T) {
	bin := buildTestFliesswerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": [`+approves+`, {"match": "Implement Ticket #2", "actions": [
		{"run": ["git", "add", "from-hook.txt"]},
		{"run": ["git", "commit", "-q", "-m", "Commit what the Hook made"]}
	], "report": {"outcome": "done", "summary": "Done."}}]}`)
	p.withHooks(t, `
[[hooks.sandbox_created]]
run = 'echo made>>from-hook.txt'
[[hooks.sandbox_created]]
run = 'echo on-host>>host.txt'
where = "host"
`)

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	if got := p.specLog(t); got != "Commit what the Hook made" {
		t.Errorf("Spec branch commits = %q, want the one holding the Hook's file", got)
	}
	if got := wordsIn(t, filepath.Join(p.work, "host.txt")); !slices.Equal(got, []string{"on-host"}) {
		t.Errorf("the host entry wrote %v to the project root, want on-host once", got)
	}
	if got := wordsIn(t, filepath.Join(p.work, "from-hook.txt")); got != nil {
		t.Errorf("the Sandbox entry ran in the user's checkout: %v", got)
	}
}

func TestAFailedSandboxCreatedGetsOneFreshRetry(t *testing.T) {
	bin := buildTestFliesswerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": [`+approves+`, `+implementsCart("Implement Ticket #2", `package cart\n`)+`]}`)
	p.withHooks(t, `
[[hooks.sandbox_created]]
run = 'git config --get fwk.tried || (git config fwk.tried 1 && git --no-such-flag)'
`)

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	if !strings.Contains(p.stdout, "sandbox_created: retrying") || !p.issues(t)[2].Closed {
		t.Errorf("want #2 closed after a retry\nstdout: %s", p.stdout)
	}
}

func TestSandboxCreatedFailingAgainHandsTheTicketBackWithoutRunningAStep(t *testing.T) {
	bin := buildTestFliesswerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": []}`)
	p.withHooks(t, `
[[hooks.sandbox_created]]
run = 'git --no-such-flag'
`)
	logPath := filepath.Join(t.TempDir(), "harness.jsonl")
	p.extraEnv = []string{"FWK_FAKE_HARNESS_LOG=" + logPath}

	if code := p.run(t); code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	p.handedBack(t, 2, "sandbox_created", "again after a retry", "no-such-flag")
	if _, err := os.Stat(logPath); err == nil {
		t.Errorf("the Harness ran %d times, want none", len(harnessCalls(t, logPath)))
	}
}

// envRef is a command line's reference to an environment variable.
func envRef(name string) string {
	if runtime.GOOS == "windows" {
		return "%" + name + "%"
	}
	return "$" + name
}

func TestSpecFinishedRunsOnceAfterTheSpecsLastTicketOnTheSpecBranch(t *testing.T) {
	bin := buildTestFliesswerk(t)
	issues := append(cartIssues(), fake.Issue{Number: 3, Title: "Cart totals", Labels: []string{"ready-for-agent"}, Parent: 1, BlockedBy: []int{2}})
	p := newRunProject(t, bin, issues, `{"sessions": [`+approves+`, `+
		implementsCart("Implement Ticket #2", `package cart\n`)+`, `+
		implementsCart("Implement Ticket #3", `package cart // totals\n`)+`]}`)
	out := filepath.Join(t.TempDir(), "out.txt")
	p.withHooks(t, `
[[hooks.spec_finished]]
run = 'echo `+envRef("FLIESSWERK_SPEC")+` `+envRef("FLIESSWERK_BRANCH")+`>>`+out+`&& git rev-parse --abbrev-ref HEAD>>`+out+`'
`)

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	branch := "fliesswerk/spec-1-spec-shopping-cart"
	if got, want := wordsIn(t, out), []string{"#1", branch, branch}; !slices.Equal(got, want) {
		t.Errorf("the Hook saw %v, want %v", got, want)
	}
}

func TestSpecFinishedAndRunEndOnlyWarnWhenTheyFailAndAStandaloneTicketFinishes(t *testing.T) {
	bin := buildTestFliesswerk(t)
	issues := []fake.Issue{{Number: 5, Title: "Fix the typo", Labels: []string{"ready-for-agent"}}}
	p := newRunProject(t, bin, issues, `{"sessions": [`+approves+`, `+implementsCart("Implement Ticket #5", `package cart\n`)+`]}`)
	out := filepath.Join(t.TempDir(), "out.txt")
	p.withHooks(t, `
[[hooks.spec_finished]]
run = 'echo `+envRef("FLIESSWERK_SPEC")+` `+envRef("FLIESSWERK_BRANCH")+`>>`+out+`'
[[hooks.spec_finished]]
run = 'git --no-such-flag'
[[hooks.spec_finished]]
run = 'echo after-the-failure>>`+out+`'
[[hooks.run_end]]
run = 'git --no-such-flag'
`)

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	if got, want := wordsIn(t, out), []string{"#5", "fliesswerk/ticket-5-fix-the-typo"}; !slices.Equal(got, want) {
		t.Errorf("the Hook saw %v, want %v", got, want)
	}
	for _, want := range []string{"warning: the spec_finished Hook failed", "warning: the run_end Hook failed"} {
		if !strings.Contains(p.stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, p.stdout)
		}
	}
}

func TestHookOutputIsRedactedAndHostHooksGetOnlyTheSecretsTheyName(t *testing.T) {
	bin := buildTestFliesswerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": [`+approves+`, `+implementsCart("Implement Ticket #2", `package cart\n`)+`]}`)
	p.withHooks(t, `
[sandbox.secrets]
NAMED = { env = "FWK_TEST_NAMED" }
OTHER = { env = "FWK_TEST_OTHER" }

[[hooks.run_start]]
run = 'echo host-named=[`+envRef("NAMED")+`] host-other=[`+envRef("OTHER")+`]'
secrets = ["NAMED"]

[[hooks.sandbox_created]]
run = 'echo sandbox-other=[`+envRef("OTHER")+`]'
`)
	p.extraEnv = []string{"FWK_TEST_NAMED=hunter2-named", "FWK_TEST_OTHER=hunter2-other"}

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	for _, want := range []string{"host-named=[***]", "sandbox-other=[***]"} {
		if !strings.Contains(p.stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, p.stdout)
		}
	}
	if strings.Contains(p.stdout, "host-other=[***]") {
		t.Errorf("a host Hook got a secret it didn't name:\n%s", p.stdout)
	}
	if strings.Contains(p.stdout, "hunter2") {
		t.Errorf("a secret leaked:\n%s", p.stdout)
	}
}
