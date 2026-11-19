package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/fliesswerk/internal/tracker/fake"
)

// handedBack checks that Ticket n came back to a person: open, with
// needs-investigation instead of the ready label, and a comment holding
// each of want and naming the Run's log directory. It returns the
// comment.
func (p *runProject) handedBack(t *testing.T, n int, want ...string) string {
	t.Helper()
	i := p.issues(t)[n]
	if i.Closed {
		t.Errorf("#%d was closed", n)
	}
	if slices.Contains(i.Labels, "ready-for-agent") || !slices.Contains(i.Labels, "needs-investigation") {
		t.Errorf("#%d labels = %v, want needs-investigation instead of ready-for-agent", n, i.Labels)
	}
	if len(i.Comments) != 1 {
		t.Fatalf("#%d has %d comments, want one\nstdout: %s", n, len(i.Comments), p.stdout)
	}
	c := i.Comments[0].Body
	for _, w := range append(want, ".fliesswerk/logs/") {
		if !strings.Contains(c, w) {
			t.Errorf("#%d's comment lacks %q:\n%s", n, w, c)
		}
	}
	if gitIn(t, p.work, "branch", "--list", "fliesswerk/ticket-*") != "" {
		t.Errorf("the work of #%d wasn't discarded: its Ticket branch is left", n)
	}
	return c
}

// untouched checks that the Tracker saw no write.
func (p *runProject) untouched(t *testing.T) {
	t.Helper()
	for n, i := range p.issues(t) {
		if i.Closed || len(i.Comments) != 0 || !slices.Contains(i.Labels, "ready-for-agent") {
			t.Errorf("#%d = %+v, want it untouched", n, i)
		}
	}
}

func TestRunHandsACannotDoTicketBackAndTheSpecCarriesOn(t *testing.T) {
	bin := buildTestFliesswerk(t)
	issues := append(cartIssues(),
		fake.Issue{Number: 3, Title: "Cart totals", Labels: []string{"ready-for-agent"}, Parent: 1, BlockedBy: []int{2}},
		fake.Issue{Number: 4, Title: "Cart page", Labels: []string{"ready-for-agent"}, Parent: 1})
	p := newRunProject(t, bin, issues, `{"sessions": [`+approves+`,
		{"match": "Implement Ticket #2", "actions": [{"text": "The Ticket names no currency."}],
		 "report": {"outcome": "cannot_do", "reason": "Which currency? The Ticket doesn't say.", "summary": "Stopped."}},
		`+implementsCart("Implement Ticket #4", `package cart\n`)+`]}`)

	code := p.run(t)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	c := p.handedBack(t, 2, "Which currency? The Ticket doesn't say.", "<details>", "The Ticket names no currency.")
	if strings.Contains(c, "Make a cart package.") {
		t.Errorf("the comment holds the prompt:\n%s", c)
	}
	issuesAfter := p.issues(t)
	if !issuesAfter[4].Closed {
		t.Errorf("#4 wasn't closed: the rest of the Spec should carry on\nstdout: %s", p.stdout)
	}
	if issuesAfter[3].Closed || len(issuesAfter[3].Comments) != 0 {
		t.Error("#3 was worked on, but its Blocker #2 is still open")
	}
}

func TestRunRetriesAFailedStepOnceInAFreshSession(t *testing.T) {
	for name, failure := range map[string]string{
		"error(other)": `"error": "other"`,
		"no_report":    `"actions": [{"text": "I forgot to report."}]`,
	} {
		t.Run(name, func(t *testing.T) {
			bin := buildTestFliesswerk(t)
			p := newRunProject(t, bin, cartIssues(), `{"sessions": [`+approves+`,
				{"match": "Implement Ticket #2", "times": 1, `+failure+`},
				`+implementsCart("Implement Ticket #2", `package cart\n`)+`]}`)
			logPath := filepath.Join(t.TempDir(), "harness.jsonl")
			p.extraEnv = []string{"FWK_FAKE_HARNESS_LOG=" + logPath}

			if code := p.run(t); code != 0 {
				t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
			}
			if n := len(harnessCalls(t, logPath)); n != 3 {
				t.Errorf("the Harness ran %d times, want implement twice, then review", n)
			}
			if !strings.Contains(p.stdout, "retrying in a fresh session") || !p.issues(t)[2].Closed {
				t.Errorf("want #2 closed after a retry\nstdout: %s", p.stdout)
			}
		})
	}
}

func TestRunHandsATicketBackWhenItsStepFailsAgainAfterTheRetry(t *testing.T) {
	bin := buildTestFliesswerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": [{"match": "Ticket #2", "error": "other"}]}`)

	if code := p.run(t); code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	p.handedBack(t, 2, "scripted failure", "again after a retry")
}

func TestRunRetriesATransientErrorThreeTimes(t *testing.T) {
	bin := buildTestFliesswerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": [`+approves+`,
		{"match": "Implement Ticket #2", "times": 3, "error": "transient"},
		`+implementsCart("Implement Ticket #2", `package cart\n`)+`]}`)

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	if !strings.Contains(p.stdout, "retry 3 of 3") || !p.issues(t)[2].Closed {
		t.Errorf("want #2 closed on the third retry\nstdout: %s", p.stdout)
	}
}

func TestRunStopsWithExit2AfterThreeTransientRetriesAndLeavesTheTicketUntouched(t *testing.T) {
	bin := buildTestFliesswerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": [{"match": "Ticket #2", "error": "transient"}]}`)
	logPath := filepath.Join(t.TempDir(), "harness.jsonl")
	p.extraEnv = []string{"FWK_FAKE_HARNESS_LOG=" + logPath}

	if code := p.run(t); code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	if n := len(harnessCalls(t, logPath)); n != 4 {
		t.Errorf("the Harness ran %d times, want 4: three retries", n)
	}
	if !strings.Contains(p.stderr, "Overloaded") {
		t.Errorf("stderr lacks the error:\n%s", p.stderr)
	}
	p.untouched(t)
}

func TestRunStopsWithExit2OnAnAuthErrorAndLeavesTheTicketUntouched(t *testing.T) {
	bin := buildTestFliesswerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": [{"match": "Ticket #2", "error": "auth"}]}`)

	if code := p.run(t); code != 2 {
		t.Fatalf("exit code = %d, want 2\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	p.untouched(t)
}

func TestRunHandsATicketBackWhenVerifyFailsAgainWithTheCommandsOutput(t *testing.T) {
	bin := buildTestFliesswerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": [`+approves+`, `+implementsCart("Implement Ticket #2", `package cart\n`)+`]}`)
	writeFile(t, filepath.Join(p.work, ".fliesswerk", "config.local.toml"), "verify = [\"test\"]\n"+noSandbox+
		"[commands]\ntest = \"git grep -q fixed -- cart/cart.go || (echo cart.go lacks the fix && exit 1)\"\n")

	if code := p.run(t); code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	p.handedBack(t, 2, "`test`", "<details>", "cart.go lacks the fix")
}

func TestRunRejectsACommitToAProtectedPathWithoutARetry(t *testing.T) {
	bin := buildTestFliesswerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": [`+approves+`, {"match": "Implement Ticket #2", "actions": [
		{"write": ".claude/settings.json", "content": "{}"},
		{"write": "cart/cart.go", "content": "package cart\n"},
		{"run": ["git", "add", "."]},
		{"run": ["git", "commit", "-q", "-m", "Add the cart"]}
	], "report": {"outcome": "done", "summary": "Added the cart."}}]}`)
	writeFile(t, filepath.Join(p.work, ".fliesswerk", "config.local.toml"), "protected_paths = [\".claude/\"]\n"+noSandbox)
	logPath := filepath.Join(t.TempDir(), "harness.jsonl")
	p.extraEnv = []string{"FWK_FAKE_HARNESS_LOG=" + logPath}

	if code := p.run(t); code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	if n := len(harnessCalls(t, logPath)); n != 1 {
		t.Errorf("the Harness ran %d times, want implement once: no retry, no review", n)
	}
	c := p.handedBack(t, 2, "`.claude/settings.json`")
	if strings.Contains(c, "cart/cart.go") {
		t.Errorf("the comment names a file that isn't protected:\n%s", c)
	}
	if got := gitIn(t, p.remote, "for-each-ref", "--format=%(refname:short)"); got != "main" {
		t.Errorf("branches on the remote = %q, want main only", got)
	}
}

func TestRunRedactsAProjectSecretInTheFailureComment(t *testing.T) {
	bin := buildTestFliesswerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": [{"match": "Ticket #2", "expand": true,
		"actions": [{"text": "The database password is $DB_PASSWORD."}],
		"report": {"outcome": "cannot_do", "reason": "No database.", "summary": "Stopped."}}]}`)
	writeFile(t, filepath.Join(p.work, ".fliesswerk", "config.local.toml"), noSandbox+"withhold_secrets = false\n[sandbox.secrets]\nDB_PASSWORD = { env = \"FWK_TEST_DB_PASSWORD\" }\n")
	p.extraEnv = []string{"FWK_TEST_DB_PASSWORD=s3cr3t-hunter2", "FWK_FAKE_LOG_ENV=DB_PASSWORD"}

	if code := p.run(t); code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	c := p.handedBack(t, 2, "The database password is ***.")
	if strings.Contains(c, "s3cr3t-hunter2") {
		t.Errorf("the comment leaks the secret:\n%s", c)
	}
}

func TestRunHandsBackATicketWhoseStepIsOOMKilledTwice(t *testing.T) {
	bin := buildTestFliesswerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": [{"match": "Implement Ticket #2", "kill": true}]}`)

	code := p.run(t)

	if code != exitFailures {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, exitFailures, p.stdout, p.stderr)
	}
	p.handedBack(t, 2, "killed for lack of memory", "again after a retry")
	if !strings.Contains(p.stdout, "retrying in a fresh session") {
		t.Errorf("the first kill wasn't retried:\n%s", p.stdout)
	}
}
