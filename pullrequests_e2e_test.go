package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/fliesswerk/internal/tracker"
	"github.com/Marcel-Goehn/fliesswerk/internal/tracker/fake"
)

// withPullRequests turns pull requests on, as `[pull_request]` settings,
// in the project's local config.
func (p *runProject) withPullRequests(t *testing.T, settings string) {
	t.Helper()
	writeFile(t, filepath.Join(p.work, ".fliesswerk", "config.local.toml"), noSandbox+"[pull_request]\n"+settings)
}

// withOpenedPullRequests gives the fake Tracker's repository prs.
func (p *runProject) withOpenedPullRequests(t *testing.T, prs ...fake.PullRequest) {
	t.Helper()
	p.patchTracker(t, map[string]any{"pull_requests": prs})
}

// pullRequests reads the fake Tracker's pull requests back, oldest first.
func (p *runProject) pullRequests(t *testing.T) []fake.PullRequest {
	t.Helper()
	data, err := os.ReadFile(p.trackerPath)
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		PullRequests []fake.PullRequest `json:"pull_requests"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	return st.PullRequests
}

// openedPullRequests are the open_pr writes the Run made.
func (p *runProject) openedPullRequests(t *testing.T) []fake.PullRequest {
	t.Helper()
	var out []fake.PullRequest
	for _, w := range p.writes(t) {
		if w.Op == "open_pr" {
			out = append(out, *w.PullRequest)
		}
	}
	return out
}

// finishedSpecs are summary.json's specs_finished.
func (p *runProject) finishedSpecs(t *testing.T) []map[string]any {
	t.Helper()
	var s struct {
		SpecsFinished []map[string]any `json:"specs_finished"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(p.logDir(t), "summary.json"))), &s); err != nil {
		t.Fatal(err)
	}
	return s.SpecsFinished
}

// summaryList is the list summary.json holds under key.
func (p *runProject) summaryList(t *testing.T, key string) []map[string]any {
	t.Helper()
	var s map[string]json.RawMessage
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(p.logDir(t), "summary.json"))), &s); err != nil {
		t.Fatal(err)
	}
	list, ok := s[key]
	if !ok {
		t.Fatalf("summary.json has no %s", key)
	}
	var out []map[string]any
	if err := json.Unmarshal(list, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// implements is a fake Harness session that commits file and reports
// summary.
func implements(match, file, summary string) string {
	return `{"match": "` + match + `", "actions": [
		{"write": "` + file + `", "content": "package cart\n"},
		{"run": ["git", "add", "` + file + `"]},
		{"run": ["git", "commit", "-q", "-m", "Add ` + file + `"]}
	], "report": {"outcome": "done", "summary": "` + summary + `"}}`
}

// specOfTwo is the cart Spec with a second Ticket, #3, after #2, and a
// third, #4, that an earlier Run closed.
func specOfTwo() []fake.Issue {
	return append(cartIssues(),
		fake.Issue{Number: 3, Title: "Cart totals", Labels: []string{"ready-for-agent"}, Parent: 1, BlockedBy: []int{2}},
		fake.Issue{Number: 4, Title: "Cart model", Parent: 1, Closed: true, Comments: []tracker.Comment{{
			Author: "me", Body: "Modelled the cart.\n\nCommits on `fliesswerk/spec-1-spec-shopping-cart`:\n\n- abc1234 Add the model\n\nClosed by the Factory after pushing `fliesswerk/spec-1-spec-shopping-cart`.\n",
		}}},
	)
}

const cartSpecBranch = "fliesswerk/spec-1-spec-shopping-cart"

func TestAFinishedSpecGetsAPullRequestListingItsTickets(t *testing.T) {
	p := newRunProject(t, buildTestFliesswerk(t), specOfTwo(), `{"sessions": [`+approves+`, `+
		implements("Implement Ticket #2", "cart/cart.go", "Added the cart package.")+`, `+
		implements("Implement Ticket #3", "cart/totals.go", "Added totals.")+`]}`)
	p.withHooks(t, "[pull_request]\nopen = true\n[[hooks.spec_finished]]\nrun = 'echo hook-ran'\n")

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	prs := p.openedPullRequests(t)
	if len(prs) != 1 {
		t.Fatalf("opened %d pull requests, want 1: %+v", len(prs), prs)
	}
	pr := prs[0]
	if pr.Head != cartSpecBranch || pr.Base != "main" || pr.Title != "Spec: Shopping cart" || pr.Draft {
		t.Errorf("pull request = %+v, want one from %s into main, titled like the Spec, ready for review", pr, cartSpecBranch)
	}
	mustContain(t, "the body", pr.Body,
		"Closes #1",
		"#2 Add the cart: Added the cart package.",
		"#3 Cart totals: Added totals.",
		"#4 Cart model: Modelled the cart.")
	url := pr.URL
	mustContain(t, "stdout", p.stdout, "#1: opened the pull request "+url)
	if opened, hook := strings.Index(p.stdout, "opened the pull request"), strings.Index(p.stdout, "spec_finished: started"); hook < opened {
		t.Errorf("spec_finished ran before the pull request was opened:\n%s", p.stdout)
	}
	mustContain(t, "the summary", p.stdout, "#1  Spec: Shopping cart  ("+cartSpecBranch+")  "+url)
	if got := p.finishedSpecs(t); len(got) != 1 || got[0]["pull_request"] != url {
		t.Errorf("summary.json's specs_finished = %v, want #1 with %s", got, url)
	}
}

func TestAFinishedStandaloneTicketGetsAPullRequestFromItsBranch(t *testing.T) {
	issues := []fake.Issue{{Number: 5, Title: "Fix the typo", Labels: []string{"ready-for-agent"}}}
	p := newRunProject(t, buildTestFliesswerk(t), issues, `{"sessions": [`+approves+`, `+
		implements("Implement Ticket #5", "typo.go", "Fixed the typo.")+`]}`)
	p.withPullRequests(t, "open = true\n")

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	prs := p.openedPullRequests(t)
	if len(prs) != 1 {
		t.Fatalf("opened %d pull requests, want 1: %+v", len(prs), prs)
	}
	if pr := prs[0]; pr.Head != "fliesswerk/ticket-5-fix-the-typo" || pr.Base != "main" || pr.Title != "Fix the typo" {
		t.Errorf("pull request = %+v, want one from the Ticket's branch into main, titled like it", pr)
	}
	mustContain(t, "the body", prs[0].Body, "Closes #5", "Fixed the typo.")
}

func TestALandedStandaloneTicketStaysOpenUntilItsPullRequestIsMerged(t *testing.T) {
	issues := []fake.Issue{{Number: 5, Title: "Fix the typo", Labels: []string{"ready-for-agent"}}}
	p := newRunProject(t, buildTestFliesswerk(t), issues, `{"sessions": [`+approves+`, `+
		implements("Implement Ticket #5", "typo.go", "Fixed the typo.")+`]}`)
	p.withPullRequests(t, "open = true\n")

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	five := p.issues(t)[5]
	if five.Closed {
		t.Errorf("#5 is closed, want it open until its pull request is merged")
	}
	if slices.Contains(five.Labels, "ready-for-agent") {
		t.Errorf("#5's labels = %v, want ready-for-agent removed", five.Labels)
	}
	prs := p.openedPullRequests(t)
	if len(prs) != 1 {
		t.Fatalf("opened %d pull requests, want 1: %+v", len(prs), prs)
	}
	if len(five.Comments) == 0 {
		t.Fatalf("#5 has no comments, want one with what was done and the pull request")
	}
	branch := "fliesswerk/ticket-5-fix-the-typo"
	mustContain(t, "#5's last comment", five.Comments[len(five.Comments)-1].Body,
		"Fixed the typo.", "Commits on `"+branch+"`", "Add typo.go", prs[0].URL)
	mustNotContain(t, "#5's last comment", five.Comments[len(five.Comments)-1].Body, "Closed by the Factory")
	mustContain(t, "stdout", p.stdout, "#5: released (it landed, and stays open until its pull request is merged)",
		"Landed, open until their pull request is merged:\n  #5  Fix the typo")
	mustNotContain(t, "stdout", p.stdout, "Closed:", "its last Ticket closed")
	if closed := p.summaryList(t, "closed"); len(closed) != 0 {
		t.Errorf("summary.json's closed = %v, want none: #5 stays open", closed)
	}
	if landed := p.summaryList(t, "landed"); len(landed) != 1 || landed[0]["id"] != "#5" || landed[0]["commits"] != 1.0 {
		t.Errorf("summary.json's landed = %v, want #5 with one commit", landed)
	}

	// A later Run doesn't take it again.
	if code := p.run(t); code != 0 {
		t.Fatalf("second Run: exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	mustNotContain(t, "the second Run's stdout", p.stdout, "#5: claimed", "Implement Ticket #5")
	if prs := p.openedPullRequests(t); len(prs) != 1 {
		t.Errorf("opened %d pull requests over both Runs, want 1", len(prs))
	}
}

func TestATicketBlockedByAStandaloneTicketInReviewWaits(t *testing.T) {
	issues := []fake.Issue{
		{Number: 5, Title: "Fix the typo", Labels: []string{"ready-for-agent"}},
		{Number: 6, Title: "Use the fixed word", Labels: []string{"ready-for-agent"}, BlockedBy: []int{5}},
	}
	p := newRunProject(t, buildTestFliesswerk(t), issues, `{"sessions": [`+approves+`, `+
		implements("Implement Ticket #5", "typo.go", "Fixed the typo.")+`, `+
		implements("Implement Ticket #6", "word.go", "Used the word.")+`]}`)
	p.withPullRequests(t, "open = true\n")

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	got := p.issues(t)
	if got[5].Closed || got[6].Closed {
		t.Errorf("#5 closed = %v, #6 closed = %v, want both open", got[5].Closed, got[6].Closed)
	}
	for _, w := range p.writes(t) {
		if w.Ticket == 6 {
			t.Errorf("#6 got the write %+v, want it untouched while #5 is open", w)
		}
	}
}

func TestASpecsTicketsStillCloseWhenTheyLand(t *testing.T) {
	p := newRunProject(t, buildTestFliesswerk(t), cartIssues(), cartScript())
	p.withPullRequests(t, "open = true\n")

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	if !p.issues(t)[2].Closed {
		t.Errorf("#2 is open, want it closed on landing")
	}
	if prs := p.openedPullRequests(t); len(prs) != 1 {
		t.Errorf("opened %+v, want one for the Spec", prs)
	}
}

func TestWithoutOpenAStandaloneTicketClosesOnLanding(t *testing.T) {
	issues := []fake.Issue{{Number: 5, Title: "Fix the typo", Labels: []string{"ready-for-agent"}}}
	p := newRunProject(t, buildTestFliesswerk(t), issues, `{"sessions": [`+approves+`, `+
		implements("Implement Ticket #5", "typo.go", "Fixed the typo.")+`]}`)
	p.withPullRequests(t, "open = false\n")

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	five := p.issues(t)[5]
	if !five.Closed {
		t.Errorf("#5 is open, want it closed on landing")
	}
	mustContain(t, "#5's last comment", five.Comments[len(five.Comments)-1].Body, "Fixed the typo.", "Closed by the Factory")
}

func TestDraftOpensThePullRequestAsADraft(t *testing.T) {
	p := newRunProject(t, buildTestFliesswerk(t), cartIssues(), cartScript())
	p.withPullRequests(t, "open = true\ndraft = true\n")

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	if prs := p.openedPullRequests(t); len(prs) != 1 || !prs[0].Draft {
		t.Errorf("opened %+v, want one draft", prs)
	}
}

func TestAnOpenPullRequestIsReusedAsItIs(t *testing.T) {
	p := newRunProject(t, buildTestFliesswerk(t), cartIssues(), cartScript())
	p.withPullRequests(t, "open = true\n")
	mine := fake.PullRequest{Number: 9, URL: fake.PullURL(9), Head: cartSpecBranch, Base: "main", Title: "My title", Body: "What I wrote.", State: fake.PullOpen}
	p.withOpenedPullRequests(t, mine)

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	if prs := p.openedPullRequests(t); len(prs) != 0 {
		t.Errorf("opened %+v, want none", prs)
	}
	if prs := p.pullRequests(t); len(prs) != 1 || prs[0] != mine {
		t.Errorf("pull requests = %+v, want only mine, unchanged", prs)
	}
	mustContain(t, "stdout", p.stdout, "#1: reused the open pull request "+mine.URL,
		"#1  Spec: Shopping cart  ("+cartSpecBranch+")  "+mine.URL)
	if got := p.finishedSpecs(t); len(got) != 1 || got[0]["pull_request"] != mine.URL {
		t.Errorf("summary.json's specs_finished = %v, want #1 with %s", got, mine.URL)
	}
}

func TestABranchWhoseEarlierPullRequestsAreAllDoneGetsANewOne(t *testing.T) {
	p := newRunProject(t, buildTestFliesswerk(t), cartIssues(), cartScript())
	p.withPullRequests(t, "open = true\n")
	p.withOpenedPullRequests(t,
		fake.PullRequest{Number: 7, URL: fake.PullURL(7), Head: cartSpecBranch, Base: "main", Title: "First", State: fake.PullMerged},
		fake.PullRequest{Number: 8, URL: fake.PullURL(8), Head: cartSpecBranch, Base: "main", Title: "Second", State: fake.PullClosed})

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	prs := p.openedPullRequests(t)
	if len(prs) != 1 || prs[0].Number != 9 {
		t.Fatalf("opened %+v, want a new one, #9", prs)
	}
	mustContain(t, "stdout", p.stdout, "#1: opened the pull request "+prs[0].URL)
}

func TestWithoutOpenNoPullRequestIsLookedUpOrOpened(t *testing.T) {
	p := newRunProject(t, buildTestFliesswerk(t), cartIssues(), cartScript())
	p.withPullRequests(t, "open = false\n")
	p.withOpenedPullRequests(t, fake.PullRequest{Number: 9, URL: fake.PullURL(9), Head: cartSpecBranch, Base: "main", State: fake.PullOpen})

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	if prs := p.openedPullRequests(t); len(prs) != 0 {
		t.Errorf("opened %+v, want none", prs)
	}
	mustNotContain(t, "stdout", p.stdout, "pull request", fake.PullURL(9))
	if got := p.finishedSpecs(t); len(got) != 1 || got[0]["pull_request"] != "" {
		t.Errorf("summary.json's specs_finished = %v, want #1 without a pull request", got)
	}
}

// dryRunIssues are the cart Spec of two, which a Run would finish, a
// standalone Ticket, and a Spec a Run wouldn't finish, since its #8 isn't
// ready.
func dryRunIssues() []fake.Issue {
	return append(specOfTwo(),
		fake.Issue{Number: 5, Title: "Fix the typo", Labels: []string{"ready-for-agent"}},
		fake.Issue{Number: 6, Title: "Spec: Checkout", Labels: []string{"ready-for-agent"}},
		fake.Issue{Number: 7, Title: "Add the checkout", Labels: []string{"ready-for-agent"}, Parent: 6},
		fake.Issue{Number: 8, Title: "Pay", Parent: 6},
	)
}

func TestADryRunNamesThePullRequestEachFinishingSpecWouldGet(t *testing.T) {
	p := newRunProject(t, buildTestFliesswerk(t), dryRunIssues(), `{"sessions": []}`)
	p.withPullRequests(t, "open = true\n")
	mine := fake.PullRequest{Number: 20, URL: fake.PullURL(20), Head: cartSpecBranch, Base: "main", State: fake.PullOpen}
	p.withOpenedPullRequests(t, mine)

	if code := p.run(t, "--dry-run"); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	mustContain(t, "stdout", p.stdout,
		"#1  Spec: Shopping cart: would reuse the open pull request "+mine.URL,
		"#5  Fix the typo: would open a pull request into main")
	mustNotContain(t, "stdout", p.stdout, "Spec: Checkout: would")
	if w := p.writes(t); len(w) != 0 {
		t.Errorf("the dry run wrote %+v, want nothing", w)
	}
}

func TestADryRunWithoutOpenShowsNoPullRequest(t *testing.T) {
	p := newRunProject(t, buildTestFliesswerk(t), dryRunIssues(), `{"sessions": []}`)
	p.withPullRequests(t, "open = false\n")

	if code := p.run(t, "--dry-run"); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}

	mustNotContain(t, "stdout", p.stdout, "pull request")
}
