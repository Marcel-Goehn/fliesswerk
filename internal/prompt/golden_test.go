package prompt_test

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/fliesswerk/internal/git"
	"github.com/Marcel-Goehn/fliesswerk/internal/prompt"
	"github.com/Marcel-Goehn/fliesswerk/internal/sandbox"
	"github.com/Marcel-Goehn/fliesswerk/internal/tracker"
	"github.com/Marcel-Goehn/fliesswerk/internal/verify"
)

var update = flag.Bool("update", false, "rewrite the golden files of the rendered prompts")

// with is c with Feedback commands, as most cases have them.
func with(c prompt.Context) prompt.Context {
	c.Commands = commands
	return c
}

var services = []sandbox.Service{
	{Name: "postgres", Addresses: []string{"postgres:5432", "localhost:5432"}},
	{Name: "worker", Addresses: []string{"worker"}},
}

func withServices(c prompt.Context) prompt.Context {
	c.Services = services
	return c
}

var baseCommits = prompt.Recent{BaseBranch: "main", Commits: []git.Message{
	{SHA: "cccccccccccccccccccc", Text: "Fix the header\n\nIt overlapped the menu."},
	{SHA: "aaaaaaaaaaaaaaaaaaaa", Text: "Start the shop"},
}}

var ownBranchCommits = prompt.Recent{BaseBranch: "main", OnSpecBranch: true, Commits: []git.Message{
	{SHA: "dddddddddddddddddddd", Text: "Fix the typo in the intro"},
}}

// failedTests is a failed verify whose output holds a run of three
// backticks, so the fence around it takes four.
var failedTests = &verify.Failure{
	Command: verify.Command{Name: "test", Line: "go test ./..."},
	Output:  "--- FAIL: TestCart (0.00s)\n    cart_test.go:12: want:\n        ```\n        total: 3\n        ```\nFAIL\n",
	Err:     errors.New("exit status 1"),
}

var conflict = prompt.Merging{
	SpecBranch: "fliesswerk/spec-1",
	Files:      []string{"cart/cart.go", "cart/cart_test.go"},
	Merged: []prompt.MergedTicket{
		{
			Ticket:  tracker.Ticket{ID: id("4"), Title: "Add checkout", Body: "Check the cart out."},
			Commits: []git.Commit{{SHA: "eeeeeeeeeeeeeeeeeeee", Subject: "Add checkout"}, {SHA: "ffffffffffffffffffff", Subject: "Test checkout"}},
		},
		{
			Ticket:  tracker.Ticket{ID: id("5"), Title: "Add payment", Body: "Pay for the cart."},
			Commits: []git.Commit{{SHA: "9999999999999999999", Subject: "Add payment"}},
		},
	},
}

func TestEveryRenderedPromptMatchesItsGoldenFile(t *testing.T) {
	standards := with(inSpec())
	standards.Standards = "docs/coding-standards.md"

	for name, got := range map[string]string{
		"implement-spec":                 prompt.Implement(with(inSpec()), specCommits, nil),
		"implement-spec-no-commits-yet":  prompt.Implement(with(inSpec()), baseCommits, nil),
		"implement-standalone":           prompt.Implement(with(standalone()), baseCommits, nil),
		"implement-standalone-on-branch": prompt.Implement(with(standalone()), ownBranchCommits, nil),
		"implement-retry":                prompt.Implement(with(inSpec()), specCommits, failedTests),
		"implement-services":             prompt.Implement(withServices(with(standalone())), baseCommits, nil),
		"implement-no-commands":          prompt.Implement(inSpec(), specCommits, nil),
		"review-standards":               prompt.Review(standards, "abc1234"),
		"review-no-standards":            prompt.Review(with(inSpec()), "abc1234"),
		"review-standalone-services":     prompt.Review(withServices(with(standalone())), "abc1234"),
		"merge-conflict":                 prompt.Merge(with(inSpec()), conflict),
		"merge-no-merged-tickets":        prompt.Merge(with(inSpec()), prompt.Merging{SpecBranch: "fliesswerk/spec-1", Files: []string{"cart/cart.go"}}),
		"merge-standalone-services":      prompt.Merge(withServices(with(standalone())), prompt.Merging{SpecBranch: "main", Files: []string{"README.md"}}),
	} {
		t.Run(name, func(t *testing.T) { golden(t, name, got) })
	}
}

// golden fails unless got is, byte for byte, testdata/<name>.golden, and
// with -update rewrites the file to got first.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path in this repo
	if err != nil {
		t.Fatalf("%v; run `go test ./internal/prompt -run Golden -update`", err)
	}
	// A Windows checkout may have turned the file's line endings into CRLF.
	want := strings.ReplaceAll(string(data), "\r\n", "\n")
	if got != want {
		t.Errorf("the prompt differs from %s (- golden, + rendered); if the change is intended, run `go test ./internal/prompt -run Golden -update`:\n%s", path, lineDiff(want, got))
	}
}

// lineDiff lists the lines of want and got, marking those only in want
// with "-" and those only in got with "+".
func lineDiff(want, got string) string {
	a, b := strings.SplitAfter(want, "\n"), strings.SplitAfter(got, "\n")
	// common[i][j] is the length of the longest common subsequence of
	// a[i:] and b[j:].
	common := make([][]int, len(a)+1)
	for i := range common {
		common[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				common[i][j] = common[i+1][j+1] + 1
			} else {
				common[i][j] = max(common[i+1][j], common[i][j+1])
			}
		}
	}
	type diffLine struct{ mark, text string }
	var lines []diffLine
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			lines = append(lines, diffLine{" ", a[i]})
			i, j = i+1, j+1
		case i < len(a) && (j == len(b) || common[i+1][j] >= common[i][j+1]):
			lines = append(lines, diffLine{"-", a[i]})
			i++
		default:
			lines = append(lines, diffLine{"+", b[j]})
			j++
		}
	}
	// Only the changed lines are shown, each with up to context unchanged
	// lines around it, quoted so whitespace shows.
	const context = 3
	var out strings.Builder
	shown := -1
	for k, l := range lines {
		near := false
		for d := max(0, k-context); d <= min(len(lines)-1, k+context); d++ {
			near = near || lines[d].mark != " "
		}
		if !near {
			continue
		}
		if shown >= 0 && k > shown+1 {
			out.WriteString("…\n")
		}
		fmt.Fprintf(&out, "%s %q\n", l.mark, l.text)
		shown = k
	}
	return out.String()
}
