//go:build live

package github_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Marcel-Goehn/fliesswerk/internal/git"
	"github.com/Marcel-Goehn/fliesswerk/internal/tracker"
	"github.com/Marcel-Goehn/fliesswerk/internal/tracker/github"
)

// The live tests run the adapter and the Factory's push against a real
// GitHub repository that exists only for them: FLIESSWERK_LIVE_REPO
// (owner/name), reached with FLIESSWERK_LIVE_TOKEN, a fine-grained token
// that can access only that repository (Issues and Contents read/write).
// Only `-tags live` builds them, so PRs never run them; the nightly
// workflow does, and then resets the repository.

const (
	liveReady = "ready-for-agent"
	// liveSwapped is the label SetLabels swaps the ready label for.
	liveSwapped = "fliesswerk-live"
)

type live struct {
	repo, token, tag string
	gh               github.ExecRunner
}

func newLive(t *testing.T) live {
	t.Helper()
	l := live{repo: os.Getenv("FLIESSWERK_LIVE_REPO"), token: os.Getenv("FLIESSWERK_LIVE_TOKEN")}
	if l.repo == "" || l.token == "" {
		t.Fatal("FLIESSWERK_LIVE_REPO and FLIESSWERK_LIVE_TOKEN must both be set")
	}
	l.gh = github.ExecRunner{Token: l.token}
	// Only this test's gh config: no login or host of the machine's.
	t.Setenv("GH_CONFIG_DIR", t.TempDir())
	l.tag = "live-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	return l
}

// run runs gh as test setup or observation, never as the code under test.
func (l live) run(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	out, err := l.gh.Run(context.Background(), []byte(stdin), args...)
	if err != nil {
		t.Fatalf("gh %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out))
}

// issue opens an issue and returns its number; it is closed when the test
// ends, whatever the test did with it.
func (l live) issue(t *testing.T, title, body string, extra ...string) string {
	t.Helper()
	args := append([]string{"issue", "create", "--repo", l.repo, "--title", l.tag + " " + title, "--body-file", "-"}, extra...)
	url := l.run(t, body, args...)
	n := url[strings.LastIndex(url, "/")+1:]
	if _, err := strconv.Atoi(n); err != nil {
		t.Fatalf("gh issue create printed %q, not an issue URL", url)
	}
	t.Cleanup(func() {
		_, _ = l.gh.Run(context.Background(), nil, "issue", "close", n, "--repo", l.repo, "--reason", "not planned")
	})
	return n
}

type liveIssue struct {
	State       string `json:"state"`
	StateReason string `json:"stateReason"`
	Labels      []struct {
		Name string `json:"name"`
	} `json:"labels"`
	Comments []struct {
		Body string `json:"body"`
	} `json:"comments"`
}

func (l live) view(t *testing.T, n string) liveIssue {
	t.Helper()
	var i liveIssue
	out := l.run(t, "", "issue", "view", n, "--repo", l.repo, "--json", "state,stateReason,labels,comments")
	if err := json.Unmarshal([]byte(out), &i); err != nil {
		t.Fatalf("gh issue view %s printed JSON this test can't read: %v", n, err)
	}
	return i
}

func (i liveIssue) labels() []string {
	out := make([]string, 0, len(i.Labels))
	for _, l := range i.Labels {
		out = append(out, l.Name)
	}
	return out
}

func find(snap tracker.Snapshot, n string) (tracker.Ticket, bool) {
	for _, tk := range snap.Tickets {
		if tk.ID.Key == n {
			return tk, true
		}
	}
	return tracker.Ticket{}, false
}

// TestLiveTracker takes a Ticket of a real Spec through Snapshot, Comment,
// SetLabels and Close, and checks each on GitHub itself.
func TestLiveTracker(t *testing.T) {
	l := newLive(t)
	ctx := t.Context()
	for _, label := range []string{liveReady, liveSwapped} {
		l.run(t, "", "label", "create", label, "--repo", l.repo, "--force")
	}

	spec := l.issue(t, "Spec", "The Spec.")
	blocker := l.issue(t, "Blocker", "Must close first.")
	ticket := l.issue(t, "Ticket", "Do the thing.\n\n## Blocked by\n\n- #"+blocker+"\n",
		"--label", liveReady, "--parent", spec, "--blocked-by", blocker)

	tr := github.New(github.Options{Repo: l.repo, ReadyLabel: liveReady, InProgressLabel: "in-progress", Runner: l.gh, GOOS: "linux"})

	snap, err := tr.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	tk, ok := find(snap, ticket)
	if !ok {
		t.Fatalf("Snapshot has no #%s among its %d Tickets", ticket, len(snap.Tickets))
	}
	if tk.Title != l.tag+" Ticket" || !slices.Contains(tk.Labels, liveReady) || tk.State != tracker.Open || tk.HasChildren {
		t.Errorf("Snapshot's #%s: title %q, labels %v, state %s, has children %v", ticket, tk.Title, tk.Labels, tk.State, tk.HasChildren)
	}
	if tk.Parent == nil || tk.Parent.Key != spec {
		t.Errorf("Snapshot's #%s: parent = %v, want #%s", ticket, tk.Parent, spec)
	}
	if len(tk.Blockers) != 1 || tk.Blockers[0].ID.Key != blocker || tk.Blockers[0].State != tracker.Open {
		t.Errorf("Snapshot's #%s: Blockers = %+v, want only the open #%s", ticket, tk.Blockers, blocker)
	}
	if len(tk.TextOnlyBlockers) != 0 {
		t.Errorf("Snapshot's #%s: text-only Blockers = %v, want none: its one is linked", ticket, tk.TextOnlyBlockers)
	}
	s := snap.Specs[spec]
	if s.Title != l.tag+" Spec" || len(s.Tickets) != 1 || s.Tickets[0].ID.Key != ticket {
		t.Errorf("Snapshot's Spec #%s: title %q, Tickets %+v, want only #%s", spec, s.Title, s.Tickets, ticket)
	}

	comment := "A comment from " + l.tag + "\n\n<details><summary>log</summary>\n\n```\nline 1\nline 2\n```\n</details>\n"
	if err := tr.Comment(ctx, tk.ID, comment); err != nil {
		t.Fatalf("Comment: %v", err)
	}
	if got := l.view(t, ticket).Comments; len(got) != 1 || strings.TrimSpace(got[0].Body) != strings.TrimSpace(comment) {
		t.Errorf("after Comment, #%s's comments are %+v, want only %q", ticket, got, comment)
	}

	if err := tr.SetLabels(ctx, tk.ID, []string{liveSwapped}, []string{liveReady}); err != nil {
		t.Fatalf("SetLabels: %v", err)
	}
	if got := l.view(t, ticket).labels(); !slices.Equal(got, []string{liveSwapped}) {
		t.Errorf("after SetLabels, #%s's labels are %v, want [%s]", ticket, got, liveSwapped)
	}
	snap, err = tr.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot after SetLabels: %v", err)
	}
	if _, ok := find(snap, ticket); ok {
		t.Errorf("Snapshot still has #%s after its ready label was removed", ticket)
	}

	closing := "Closed by " + l.tag
	if err := tr.Close(ctx, tk.ID, closing); err != nil {
		t.Fatalf("Close: %v", err)
	}
	got := l.view(t, ticket)
	if got.State != "CLOSED" || got.StateReason != "COMPLETED" {
		t.Errorf("after Close, #%s is %s (%s), want CLOSED (COMPLETED)", ticket, got.State, got.StateReason)
	}
	if n := len(got.Comments); n == 0 || strings.TrimSpace(got.Comments[n-1].Body) != closing {
		t.Errorf("after Close, #%s's last comment isn't %q: %+v", ticket, closing, got.Comments)
	}
}

// TestLivePush pushes a branch the way the Factory pushes a Spec branch: a
// new branch and a fast-forward land, and a push that isn't one is
// refused as an ErrPushRejected.
func TestLivePush(t *testing.T) {
	l := newLive(t)
	ctx := t.Context()
	dir := t.TempDir()

	// Only this test's git config, and the token as a header git reads
	// from the environment, never from its command line.
	global := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(global, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	auth := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + l.token))
	for k, v := range map[string]string{
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_CONFIG_GLOBAL":   global,
		"GIT_CONFIG_COUNT":    "1",
		"GIT_CONFIG_KEY_0":    "http.https://github.com/.extraheader",
		"GIT_CONFIG_VALUE_0":  "AUTHORIZATION: basic " + auth,
		"GIT_AUTHOR_NAME":     "fliesswerk live test",
		"GIT_AUTHOR_EMAIL":    "live@fliesswerk.invalid",
		"GIT_COMMITTER_NAME":  "fliesswerk live test",
		"GIT_COMMITTER_EMAIL": "live@fliesswerk.invalid",
	} {
		t.Setenv(k, v)
	}

	r := git.Repo{Dir: filepath.Join(dir, "repo")}
	if err := os.Mkdir(r.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	branch := "fliesswerk-live/" + l.tag
	gitRun := func(args ...string) string {
		t.Helper()
		out, err := r.Run(ctx, args...)
		if err != nil {
			t.Fatalf("git %s: %v", strings.Join(args, " "), err)
		}
		return out
	}
	remoteTip := func() string {
		t.Helper()
		out := gitRun("ls-remote", "origin", "refs/heads/"+branch)
		sha, _, _ := strings.Cut(out, "\t")
		return sha
	}
	gitRun("init", "--quiet", "--initial-branch", "spec")
	gitRun("remote", "add", "origin", "https://github.com/"+l.repo+".git")
	t.Cleanup(func() {
		_, _ = r.Run(context.Background(), "push", "--quiet", "origin", "--delete", "refs/heads/"+branch)
	})

	gitRun("commit", "--quiet", "--allow-empty", "-m", "first")
	first := gitRun("rev-parse", "HEAD")
	if err := r.Push(ctx, "origin", "spec", branch); err != nil {
		t.Fatalf("pushing a new branch: %v", err)
	}
	if got := remoteTip(); got != first {
		t.Fatalf("after pushing a new branch, it is at %q on GitHub, want %s", got, first)
	}

	gitRun("commit", "--quiet", "--allow-empty", "-m", "second")
	second := gitRun("rev-parse", "HEAD")
	if err := r.Push(ctx, "origin", "spec", branch); err != nil {
		t.Fatalf("pushing a fast-forward: %v", err)
	}
	if got := remoteTip(); got != second {
		t.Fatalf("after a fast-forward push, the branch is at %q on GitHub, want %s", got, second)
	}

	gitRun("branch", "diverged", first)
	gitRun("checkout", "--quiet", "diverged")
	gitRun("commit", "--quiet", "--allow-empty", "-m", "diverged")
	err := r.Push(ctx, "origin", "diverged", branch)
	if !errors.Is(err, git.ErrPushRejected) {
		t.Fatalf("pushing a commit that isn't a fast-forward: err = %v, want an ErrPushRejected", err)
	}
	if got := remoteTip(); got != second {
		t.Errorf("after the refused push, the branch is at %q on GitHub, want it left at %s", got, second)
	}
}
