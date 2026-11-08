package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/kraftwerk/internal/tracker/fake"
)

// commitsFile is the fake Harness's actions that commit one file.
func commitsFile(name, content string) string {
	return fmt.Sprintf(`{"write": %q, "content": %q},
		{"run": ["git", "add", %q]},
		{"run": ["git", "commit", "-q", "-m", %q]}`, name, content, name, "Add "+name)
}

// meetsThenCommits is a session that tells the other session it has
// started, waits until that one has too, and commits a file. Sessions that
// run one after the other never get past the wait.
func meetsThenCommits(match, mine, theirs, file string) string {
	return fmt.Sprintf(`{"match": %q, "actions": [{"touch": %q}, {"await": %q}, %s],
		"report": {"outcome": "done", "summary": "Added %s."}}`, match, mine, theirs, commitsFile(file, "x\n"), file)
}

func twoTicketsOfASpec() []fake.Issue {
	return []fake.Issue{
		{Number: 1, Title: "Spec: Shopping cart", Body: "Customers need a cart.", Labels: []string{"ready-for-agent"}},
		{Number: 2, Title: "Add the cart", Labels: []string{"ready-for-agent"}, Parent: 1},
		{Number: 3, Title: "Add the shelf", Labels: []string{"ready-for-agent"}, Parent: 1},
	}
}

func TestTwoCleanTicketsOfASpecRunAtOnceAndBothLand(t *testing.T) {
	bin := buildTestKraftwerk(t)
	dir := t.TempDir()
	two, three := filepath.Join(dir, "2"), filepath.Join(dir, "3")
	p := newRunProject(t, bin, twoTicketsOfASpec(), `{"sessions": [`+approves+`, `+
		meetsThenCommits("Implement Ticket #2", two, three, "cart.txt")+`, `+
		meetsThenCommits("Implement Ticket #3", three, two, "shelf.txt")+`]}`)
	writeFile(t, filepath.Join(p.work, ".kraftwerk", "config.local.toml"), "verify = [\"test\"]\n"+noSandbox+
		"[commands]\ntest = \"git status --short\"\n")

	if code := p.run(t, "--max-parallel", "2"); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	issues := p.issues(t)
	if !issues[2].Closed || !issues[3].Closed {
		t.Errorf("#2 closed = %v, #3 closed = %v, want both\nstdout: %s", issues[2].Closed, issues[3].Closed, p.stdout)
	}
	spec := "kraftwerk/spec-1-spec-shopping-cart"
	files := gitIn(t, p.remote, "ls-tree", "--name-only", spec)
	for _, want := range []string{"cart.txt", "shelf.txt"} {
		if !strings.Contains(files, want) {
			t.Errorf("the Spec branch lacks %s; it has:\n%s", want, files)
		}
	}
	if n := strings.Count(p.stdout, "merged "+spec); n != 1 {
		t.Errorf("stdout says %d merges, want 1: the first Ticket fast-forwards\n%s", n, p.stdout)
	}
	// Each Ticket's own verify, and once more on the merged result.
	if n := strings.Count(p.stdout, "verify: passed"); n != 3 {
		t.Errorf("verify passed %d times, want 3\n%s", n, p.stdout)
	}
}

func TestTicketsOfTwoSpecsRunAtOnceOnTheirOwnBranches(t *testing.T) {
	bin := buildTestKraftwerk(t)
	dir := t.TempDir()
	cart, search := filepath.Join(dir, "cart"), filepath.Join(dir, "search")
	issues := append(cartIssues(),
		fake.Issue{Number: 10, Title: "Spec: Search", Labels: []string{"ready-for-agent"}},
		fake.Issue{Number: 11, Title: "Add search", Labels: []string{"ready-for-agent"}, Parent: 10})
	p := newRunProject(t, bin, issues, `{"sessions": [`+approves+`, `+
		meetsThenCommits("Implement Ticket #2", cart, search, "cart.txt")+`, `+
		meetsThenCommits("Implement Ticket #11", search, cart, "search.txt")+`]}`)

	if code := p.run(t, "--max-parallel", "2"); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	after := p.issues(t)
	if !after[2].Closed || !after[11].Closed {
		t.Errorf("#2 closed = %v, #11 closed = %v, want both\nstdout: %s", after[2].Closed, after[11].Closed, p.stdout)
	}
	cartFiles := gitIn(t, p.remote, "ls-tree", "--name-only", "kraftwerk/spec-1-spec-shopping-cart")
	searchFiles := gitIn(t, p.remote, "ls-tree", "--name-only", "kraftwerk/spec-10-spec-search")
	if !strings.Contains(cartFiles, "cart.txt") || strings.Contains(cartFiles, "search.txt") {
		t.Errorf("the cart Spec's branch holds:\n%s", cartFiles)
	}
	if !strings.Contains(searchFiles, "search.txt") || strings.Contains(searchFiles, "cart.txt") {
		t.Errorf("the search Spec's branch holds:\n%s", searchFiles)
	}
}

// conflictsOnce is the sessions of a Ticket that first commits shared.txt
// its own way, once, and then, implemented afresh, a file of its own.
func conflictsOnce(ticket, content, own string) string {
	return fmt.Sprintf(`{"match": "Implement Ticket #%s", "times": 1, "actions": [%s],
		"report": {"outcome": "done", "summary": "Wrote shared.txt."}},
		{"match": "Implement Ticket #%s", "actions": [%s],
		"report": {"outcome": "done", "summary": "Added %s."}}`,
		ticket, commitsFile("shared.txt", content), ticket, commitsFile(own, "x\n"), own)
}

func TestAConflictingPairReQueuesOneAndBothEndUpClosed(t *testing.T) {
	bin := buildTestKraftwerk(t)
	p := newRunProject(t, bin, twoTicketsOfASpec(), `{"sessions": [`+approves+`, `+
		conflictsOnce("2", "from 2\n", "two.txt")+`, `+
		conflictsOnce("3", "from 3\n", "three.txt")+`]}`)
	logPath := filepath.Join(t.TempDir(), "harness.jsonl")
	p.extraEnv = []string{"KWK_FAKE_HARNESS_LOG=" + logPath}

	if code := p.run(t, "--max-parallel", "2"); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	issues := p.issues(t)
	if !issues[2].Closed || !issues[3].Closed {
		t.Errorf("#2 closed = %v, #3 closed = %v, want both\nstdout: %s", issues[2].Closed, issues[3].Closed, p.stdout)
	}
	if n := strings.Count(p.stdout, "re-queued"); n != 1 {
		t.Errorf("stdout says %d re-queues, want 1\n%s", n, p.stdout)
	}
	if !strings.Contains(p.stdout, "conflict in shared.txt") {
		t.Errorf("stdout doesn't name the conflict:\n%s", p.stdout)
	}
	// Implement and review of both, and of the one implemented afresh.
	if n := len(harnessCalls(t, logPath)); n != 6 {
		t.Errorf("the Harness ran %d times, want 6", n)
	}
	files := gitIn(t, p.remote, "ls-tree", "--name-only", "kraftwerk/spec-1-spec-shopping-cart")
	if !strings.Contains(files, "shared.txt") || strings.Count(files, "two.txt")+strings.Count(files, "three.txt") != 1 {
		t.Errorf("the Spec branch holds:\n%s\nwant shared.txt and the retried Ticket's own file", files)
	}
}
