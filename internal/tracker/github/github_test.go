package github_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Marcel-Goehn/fliesswerk/internal/tracker"
	"github.com/Marcel-Goehn/fliesswerk/internal/tracker/github"
)

const repo = "Marcel-Goehn/fliesswerk"

// call is one recorded gh invocation.
type call struct {
	args  string
	stdin string
}

// fixtureRunner answers gh calls from recorded `gh … --json` output in
// testdata, by the exact argv.
type fixtureRunner struct {
	t         *testing.T
	responses map[string]string // argv joined by spaces -> testdata file
	inline    map[string]string // argv joined by spaces -> output
	errs      map[string]error
	calls     []call
}

func (r *fixtureRunner) Run(_ context.Context, stdin []byte, args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	r.calls = append(r.calls, call{args: key, stdin: string(stdin)})
	if err, ok := r.errs[key]; ok {
		return nil, err
	}
	if out, ok := r.inline[key]; ok {
		return []byte(out), nil
	}
	file, ok := r.responses[key]
	if !ok {
		return nil, nil // a write: gh prints nothing we read
	}
	data, err := os.ReadFile(filepath.Join("testdata", file)) //nolint:gosec // a recorded fixture named by the test
	if err != nil {
		r.t.Fatal(err)
	}
	return data, nil
}

const listArgs = "issue list --repo " + repo + " --state open --label ready-for-agent --limit 1000 --json number,title,body,labels,state,url,parent,subIssues,blockedBy,assignees"
const viewSpecArgs = "issue view 31 --repo " + repo + " --json number,title,body,state,url,subIssues,labels,assignees"

func newTracker(r *fixtureRunner) *github.Tracker {
	return github.New(github.Options{Repo: repo, ReadyLabel: "ready-for-agent", InProgressLabel: "in-progress", Runner: r, GOOS: "linux"})
}

func ticket(t *testing.T, snap tracker.Snapshot, display string) tracker.Ticket {
	t.Helper()
	for _, tk := range snap.Tickets {
		if tk.ID.Display == display {
			return tk
		}
	}
	t.Fatalf("no Ticket %s in the Snapshot", display)
	return tracker.Ticket{}
}

func blockers(tk tracker.Ticket) []string {
	var out []string
	for _, b := range tk.Blockers {
		out = append(out, b.ID.Display+" "+b.State.String())
	}
	return out
}

func displays(ids []tracker.ID) []string {
	var out []string
	for _, id := range ids {
		out = append(out, id.Display)
	}
	return out
}

func TestSnapshotFromRecordedIssueList(t *testing.T) {
	r := &fixtureRunner{t: t, responses: map[string]string{
		listArgs:     "issue-list.json",
		viewSpecArgs: "issue-view-31.json",
	}}

	snap, err := newTracker(r).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(snap.Tickets) != 5 {
		t.Fatalf("got %d Tickets, want 5", len(snap.Tickets))
	}
	tk := ticket(t, snap, "#37")
	if tk.ID.Branch != "37" || tk.ID.Order != 37 {
		t.Errorf("id = %+v, want branch form 37 and order 37", tk.ID)
	}
	if tk.Title != "First Run: git, branches and loop, `kind = \"none\"`" || tk.State != tracker.Open {
		t.Errorf("title, state = %q, %v", tk.Title, tk.State)
	}
	if !slices.Contains(tk.Labels, "ready-for-agent") || !strings.Contains(tk.Body, "## What to build") {
		t.Errorf("labels = %v, body = %.40q…", tk.Labels, tk.Body)
	}
	if tk.Parent == nil || tk.Parent.Display != "#31" || tk.HasChildren {
		t.Errorf("parent = %v, has children = %v; want #31, false", tk.Parent, tk.HasChildren)
	}
	if got := blockers(tk); !slices.Equal(got, []string{"#36 open", "#35 open"}) {
		t.Errorf("blockers = %v", got)
	}
	if got := blockers(ticket(t, snap, "#64")); !slices.Equal(got, []string{"#33 closed", "#37 open"}) {
		t.Errorf("#64 blockers = %v", got)
	}
	if len(ticket(t, snap, "#35").TextOnlyBlockers) != 0 {
		t.Error("#35 merely mentions \"blocked by\" in prose; that's no text-only Blocker")
	}

	spec := ticket(t, snap, "#31")
	if !spec.HasChildren || spec.Parent != nil {
		t.Errorf("#31: has children = %v, parent = %v; want a Spec without a parent", spec.HasChildren, spec.Parent)
	}

	s, ok := snap.Specs[tk.Parent.Key]
	if !ok {
		t.Fatalf("no Spec for key %q", tk.Parent.Key)
	}
	if s.Title != "Spec: Factory v1 (Fliesswerk)" || !strings.HasPrefix(s.Body, "## Problem Statement") {
		t.Errorf("spec = %q, %.30q…", s.Title, s.Body)
	}
	if len(s.Tickets) != 34 {
		t.Fatalf("spec has %d Tickets, want 34", len(s.Tickets))
	}
	if first := s.Tickets[0]; first.ID.Display != "#32" || first.State != tracker.Closed || first.Title != "Walking skeleton and CI" {
		t.Errorf("first Spec Ticket = %+v", first)
	}

	for _, c := range r.calls {
		if c.args != listArgs && c.args != viewSpecArgs {
			t.Errorf("unexpected gh call: %s", c.args)
		}
	}
	if len(r.calls) != 2 {
		t.Errorf("made %d gh calls, want 2 (one list, one view per Spec)", len(r.calls))
	}
}

func TestSnapshotFlagsTextOnlyBlockers(t *testing.T) {
	r := &fixtureRunner{t: t, responses: map[string]string{
		listArgs:     "issue-list-text-blockers.json",
		viewSpecArgs: "issue-view-31.json",
	}}

	snap, err := newTracker(r).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	tk := ticket(t, snap, "#64")
	if got := displays(tk.TextOnlyBlockers); !slices.Equal(got, []string{"#36", "#41", "#40", "acme/lib#5"}) {
		t.Errorf("text-only blockers = %v, want [#36 #41 #40 acme/lib#5]", got)
	}
	if got := blockers(tk); !slices.Equal(got, []string{"#33 closed", "#37 open", "acme/lib#9 closed"}) {
		t.Errorf("blockers = %v", got)
	}
	if len(ticket(t, snap, "#66").TextOnlyBlockers) != 0 {
		t.Error("#66's Blocked by section names no issue")
	}
}

func TestSnapshotFailsWhenGhFails(t *testing.T) {
	r := &fixtureRunner{t: t, errs: map[string]error{listArgs: errors.New("HTTP 502")}}

	if _, err := newTracker(r).Snapshot(context.Background()); err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Fatalf("err = %v, want it to carry gh's error", err)
	}
}

func TestCommentsFromRecordedView(t *testing.T) {
	args := "issue view 34 --repo " + repo + " --json comments"
	r := &fixtureRunner{t: t, responses: map[string]string{args: "issue-view-34-comments.json"}}

	comments, err := newTracker(r).Comments(context.Background(), tracker.ID{Key: "34", Display: "#34", Branch: "34"})
	if err != nil {
		t.Fatal(err)
	}

	if len(comments) != 1 {
		t.Fatalf("got %d comments, want 1", len(comments))
	}
	c := comments[0]
	if c.Author != "Marcel-Goehn" || c.CreatedAt.Format("2006-01-02T15:04:05Z") != "2026-11-07T15:03:03Z" || c.Body == "" {
		t.Errorf("comment = %+v", c)
	}
}

func TestWritesGoThroughGh(t *testing.T) {
	id := tracker.ID{Key: "35", Display: "#35", Branch: "35"}
	ctx := context.Background()

	t.Run("comment passes the text on stdin", func(t *testing.T) {
		r := &fixtureRunner{t: t}
		if err := newTracker(r).Comment(ctx, id, "hello"); err != nil {
			t.Fatal(err)
		}
		want := []call{{args: "issue comment 35 --repo " + repo + " --body-file -", stdin: "hello"}}
		if !slices.Equal(r.calls, want) {
			t.Fatalf("calls = %q, want %q", r.calls, want)
		}
	})

	t.Run("close closes as completed, then comments", func(t *testing.T) {
		r := &fixtureRunner{t: t}
		if err := newTracker(r).Close(ctx, id, "done"); err != nil {
			t.Fatal(err)
		}
		want := []call{
			{args: "issue close 35 --repo " + repo + " --reason completed"},
			{args: "issue comment 35 --repo " + repo + " --body-file -", stdin: "done"},
		}
		if !slices.Equal(r.calls, want) {
			t.Fatalf("calls = %q, want %q", r.calls, want)
		}
	})

	t.Run("set labels adds and removes", func(t *testing.T) {
		r := &fixtureRunner{t: t}
		if err := newTracker(r).SetLabels(ctx, id, []string{"needs-investigation"}, []string{"ready-for-agent"}); err != nil {
			t.Fatal(err)
		}
		want := []call{{args: "issue edit 35 --repo " + repo + " --add-label needs-investigation --remove-label ready-for-agent"}}
		if !slices.Equal(r.calls, want) {
			t.Fatalf("calls = %q, want %q", r.calls, want)
		}
	})

	t.Run("an issue in another repo is addressed by URL", func(t *testing.T) {
		r := &fixtureRunner{t: t}
		other := tracker.ID{Key: "acme/lib#5", Display: "acme/lib#5", Branch: "acme-lib-5"}
		if err := newTracker(r).Comment(ctx, other, "x"); err != nil {
			t.Fatal(err)
		}
		if r.calls[0].args != "issue comment https://github.com/acme/lib/issues/5 --body-file -" {
			t.Fatalf("args = %q", r.calls[0].args)
		}
	})
}

func TestLinkBlockerFixNamesTheGhCommand(t *testing.T) {
	fix := newTracker(&fixtureRunner{t: t}).Describe().LinkBlockerFix
	got := fix(tracker.ID{Key: "64", Display: "#64", Branch: "64"}, tracker.ID{Key: "36", Display: "#36", Branch: "36"})
	if got != "gh issue edit 64 --repo "+repo+" --add-blocked-by 36" {
		t.Fatalf("fix = %q", got)
	}
}

func TestChecks(t *testing.T) {
	version, err := os.ReadFile("testdata/gh-version.txt")
	if err != nil {
		t.Fatal(err)
	}
	run := func(goos string, r *fixtureRunner) error {
		tr := github.New(github.Options{Repo: repo, ReadyLabel: "ready-for-agent", Runner: r, GOOS: goos})
		for _, c := range tr.Describe().Checks {
			if err := c.Run(context.Background()); err != nil {
				return fmt.Errorf("%s: %w", c.Name, err)
			}
		}
		return nil
	}
	versionRunner := func(out string, errs map[string]error) *fixtureRunner {
		return &fixtureRunner{t: t, inline: map[string]string{"--version": out}, errs: errs}
	}

	t.Run("recorded gh logged in passes", func(t *testing.T) {
		if err := run("linux", versionRunner(string(version), nil)); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("gh missing names the install command for the OS", func(t *testing.T) {
		for goos, want := range map[string]string{
			"darwin":  "brew install gh",
			"windows": "winget install --id GitHub.cli",
			"linux":   "https://github.com/cli/cli/blob/trunk/docs/install_linux.md",
		} {
			r := &fixtureRunner{t: t, errs: map[string]error{"--version": github.ErrNotInstalled}}
			err := run(goos, r)
			if err == nil || !strings.Contains(err.Error(), "not installed") || !strings.Contains(err.Error(), want) {
				t.Errorf("%s: err = %v, want it to name %q", goos, err, want)
			}
		}
	})

	t.Run("gh too old names the minimum and the upgrade", func(t *testing.T) {
		err := run("darwin", versionRunner("gh version 2.93.1 (2026-05-01)\nhttps://github.com/cli/cli/releases/tag/v2.93.1\n", nil))
		if err == nil || !strings.Contains(err.Error(), "2.93.1") || !strings.Contains(err.Error(), "2.94.0") || !strings.Contains(err.Error(), "brew upgrade gh") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("a repository the login can't read names tracker.repo", func(t *testing.T) {
		err := run("linux", versionRunner(string(version), map[string]error{"api repos/" + repo + " --silent": errors.New("HTTP 404")}))
		if err == nil || !strings.Contains(err.Error(), repo) || !strings.Contains(err.Error(), "tracker.repo") {
			t.Fatalf("err = %v", err)
		}
	})

	t.Run("gh not logged in names gh auth login and tracker.token", func(t *testing.T) {
		err := run("linux", versionRunner(string(version), map[string]error{"auth status": errors.New("exit status 1")}))
		if err == nil || !strings.Contains(err.Error(), "gh auth login") || !strings.Contains(err.Error(), "tracker.token") {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestSnapshotRefusesCutShortBlockerLists(t *testing.T) {
	list := `[{"number":5,"title":"T","body":"","state":"OPEN","url":"https://github.com/` + repo + `/issues/5",
		"labels":[{"name":"ready-for-agent"}],"parent":null,"subIssues":{"nodes":[],"totalCount":0},
		"blockedBy":{"nodes":[{"number":3,"state":"CLOSED","title":"x","url":"https://github.com/` + repo + `/issues/3"}],"totalCount":2}}]`
	r := &fixtureRunner{t: t, inline: map[string]string{listArgs: list}}

	_, err := newTracker(r).Snapshot(context.Background())
	if err == nil || !strings.Contains(err.Error(), "#5") || !strings.Contains(err.Error(), "1 of its 2 Blockers") {
		t.Fatalf("err = %v, want it to refuse #5's cut-short Blocker list", err)
	}
}

// eventsArgs is the gh call that reads when issue n got the in-progress
// label, and from whom.
func eventsArgs(path string) string {
	return "api --paginate " + path + `/events --jq .[] | select(.event == "labeled" and .label.name == "in-progress") | [.actor.login, .created_at] | @tsv`
}

func TestSnapshotReadsClaimsWithClaimerAndDate(t *testing.T) {
	list := `[{"number":5,"title":"Alone","body":"","state":"OPEN","url":"https://github.com/` + repo + `/issues/5",
		"labels":[{"name":"ready-for-agent"},{"name":"in-progress"}],"assignees":[{"login":"lead"},{"login":"alice"}],"parent":null,
		"subIssues":{"nodes":[],"totalCount":0},"blockedBy":{"nodes":[],"totalCount":0}},
		{"number":32,"title":"In a Spec","body":"","state":"OPEN","url":"https://github.com/` + repo + `/issues/32",
		"labels":[{"name":"ready-for-agent"}],"assignees":[],"parent":{"number":31,"state":"OPEN","title":"S","url":"https://github.com/` + repo + `/issues/31"},
		"subIssues":{"nodes":[],"totalCount":0},"blockedBy":{"nodes":[],"totalCount":0}},
		{"number":8,"title":"Relabelled","body":"","state":"OPEN","url":"https://github.com/` + repo + `/issues/8",
		"labels":[{"name":"ready-for-agent"},{"name":"in-progress"}],"assignees":[{"login":"carol"}],"parent":null,
		"subIssues":{"nodes":[],"totalCount":0},"blockedBy":{"nodes":[],"totalCount":0}},
		{"number":7,"title":"Unclaimed","body":"","state":"OPEN","url":"https://github.com/` + repo + `/issues/7",
		"labels":[{"name":"ready-for-agent"}],"assignees":[{"login":"bob"}],"parent":null,
		"subIssues":{"nodes":[],"totalCount":0},"blockedBy":{"nodes":[],"totalCount":0}}]`
	spec := `{"number":31,"title":"S","body":"","state":"OPEN","url":"https://github.com/` + repo + `/issues/31",
		"labels":[{"name":"in-progress"}],"assignees":[{"login":"bob"}],
		"subIssues":{"nodes":[{"number":32,"state":"OPEN","title":"In a Spec","url":"https://github.com/` + repo + `/issues/32"}],"totalCount":1}}`
	r := &fixtureRunner{t: t, inline: map[string]string{
		listArgs:     list,
		viewSpecArgs: spec,
		// alice labelled #5 twice; the last time counts.
		eventsArgs("repos/" + repo + "/issues/5"):  "alice\t2026-11-01T08:00:00Z\nalice\t2026-11-03T10:30:00Z\n",
		eventsArgs("repos/" + repo + "/issues/31"): "",
		// carol claimed #8; later a bot, and a since-deleted user, re-added the label.
		eventsArgs("repos/" + repo + "/issues/8"): "carol\t2026-11-01T08:00:00Z\nbot\t2026-11-04T08:00:00Z\n\t2026-11-05T08:00:00Z\n",
	}}

	snap, err := newTracker(r).Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if c := ticket(t, snap, "#5").Claim; c == nil || *c != (tracker.Claim{By: "alice", At: time.Date(2026, 11, 3, 10, 30, 0, 0, time.UTC)}) {
		t.Errorf("#5 claim = %+v, want alice's on 2026-11-03: the labeller, not the first assignee", c)
	}
	if c := snap.Specs["31"].Claim; c == nil || c.By != "bob" || !c.At.IsZero() {
		t.Errorf("Spec #31 claim = %+v, want bob's (its assignee, without a labelled event) with no date", c)
	}
	if c := ticket(t, snap, "#8").Claim; c == nil || *c != (tracker.Claim{By: "carol", At: time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)}) {
		t.Errorf("#8 claim = %+v, want carol's on 2026-11-01: the claimer is an assignee, whoever re-added the label", c)
	}
	if c := ticket(t, snap, "#7").Claim; c != nil {
		t.Errorf("#7 claim = %+v, want none: an assignee without the label is no claim", c)
	}
	if c := ticket(t, snap, "#32").Claim; c != nil {
		t.Errorf("#32 claim = %+v, want none: a Ticket of a Spec is claimed on its Spec", c)
	}
}

func TestClaimsListsEveryOpenClaimedIssue(t *testing.T) {
	list := `[{"number":31,"title":"S","url":"https://github.com/` + repo + `/issues/31","assignees":[{"login":"bob"}]}]`
	r := &fixtureRunner{t: t, inline: map[string]string{
		"issue list --repo " + repo + " --state open --label in-progress --limit 1000 --json number,title,url,assignees": list,
		eventsArgs("repos/" + repo + "/issues/31"): "bob\t2026-11-02T09:00:00Z\n",
	}}

	got, err := newTracker(r).Claims(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	want := []tracker.Claimed{{
		ID:    tracker.ID{Key: "31", Display: "#31", Branch: "31", Order: 31},
		Title: "S", Claim: tracker.Claim{By: "bob", At: time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)},
	}}
	if !slices.Equal(got, want) {
		t.Fatalf("claims = %+v, want %+v", got, want)
	}
}

func TestUserAsksGhOnce(t *testing.T) {
	r := &fixtureRunner{t: t, inline: map[string]string{"api user --jq .login": "me\n"}}
	tr := newTracker(r)

	for range 2 {
		if u, err := tr.User(context.Background()); err != nil || u != "me" {
			t.Fatalf("user = %q, %v; want me", u, err)
		}
	}
	if len(r.calls) != 1 {
		t.Fatalf("calls = %q, want one", r.calls)
	}
}

func TestClaimAndReleaseGoThroughGh(t *testing.T) {
	ctx := context.Background()
	id := tracker.ID{Key: "31", Display: "#31", Branch: "31"}
	r := &fixtureRunner{t: t}
	tr := newTracker(r)

	if err := tr.Claim(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := tr.Release(ctx, id, "alice"); err != nil {
		t.Fatal(err)
	}

	want := []call{
		{args: "issue edit 31 --repo " + repo + " --add-label in-progress --add-assignee @me"},
		{args: "issue edit 31 --repo " + repo + " --remove-label in-progress --remove-assignee alice"},
	}
	if !slices.Equal(r.calls, want) {
		t.Fatalf("calls = %q\nwant    %q", r.calls, want)
	}
}

// limitedRunner refuses a write with a rate limit, and answers the probe
// that follows with headers.
type limitedRunner struct {
	fixtureRunner
	write   string
	headers string
}

func (r *limitedRunner) Run(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	key := strings.Join(args, " ")
	if strings.HasPrefix(key, "api --include") {
		r.calls = append(r.calls, call{args: key})
		return []byte(r.headers), errors.New("gh: HTTP 403")
	}
	if key == r.write {
		return nil, errors.New("gh issue: HTTP 403: You have exceeded a secondary rate limit")
	}
	return r.fixtureRunner.Run(ctx, stdin, args...)
}

func TestARateLimitedWriteSaysHowLongToWait(t *testing.T) {
	id := tracker.ID{Key: "35", Display: "#35", Branch: "35"}
	write := "issue comment 35 --repo " + repo + " --body-file -"
	for name, tt := range map[string]struct {
		headers string
		atLeast time.Duration
		atMost  time.Duration
	}{
		"Retry-After": {"HTTP/2.0 403 Forbidden\r\nRetry-After: 90\r\n\r\n{}", 90 * time.Second, 90 * time.Second},
		"primary limit": {
			fmt.Sprintf("HTTP/2.0 403 Forbidden\r\nX-RateLimit-Remaining: 0\r\nX-RateLimit-Reset: %d\r\n\r\n{}", time.Now().Add(10*time.Minute).Unix()),
			9 * time.Minute, 11 * time.Minute,
		},
		"neither header: a minute, as GitHub says": {"HTTP/2.0 403 Forbidden\r\nX-RateLimit-Remaining: 12\r\n\r\n{}", time.Minute, time.Minute},
	} {
		t.Run(name, func(t *testing.T) {
			r := &limitedRunner{fixtureRunner: fixtureRunner{t: t}, write: write, headers: tt.headers}

			err := github.New(github.Options{Repo: repo, Runner: r, GOOS: "linux"}).Comment(context.Background(), id, "hi")

			var limited *tracker.RateLimitError
			if !errors.As(err, &limited) {
				t.Fatalf("err = %v, want a RateLimitError", err)
			}
			if limited.Wait < tt.atLeast || limited.Wait > tt.atMost {
				t.Errorf("wait = %s, want between %s and %s", limited.Wait, tt.atLeast, tt.atMost)
			}
		})
	}
}

func TestAnOrdinaryFailedWriteIsNotARateLimit(t *testing.T) {
	id := tracker.ID{Key: "35", Display: "#35", Branch: "35"}
	r := &fixtureRunner{t: t, errs: map[string]error{"issue comment 35 --repo " + repo + " --body-file -": errors.New("gh issue: HTTP 502")}}

	err := newTracker(r).Comment(context.Background(), id, "hi")

	var limited *tracker.RateLimitError
	if err == nil || errors.As(err, &limited) {
		t.Fatalf("err = %v, want a plain error", err)
	}
	if len(r.calls) != 1 {
		t.Errorf("calls = %q, want no probe", r.calls)
	}
}

// A Close retried after its comment landed must not comment twice, so the
// comment, which isn't idempotent, goes last.
func TestACloseThatFailsHasNotCommentedYet(t *testing.T) {
	id := tracker.ID{Key: "35", Display: "#35", Branch: "35"}
	closeArgs := "issue close 35 --repo " + repo + " --reason completed"
	r := &fixtureRunner{t: t, errs: map[string]error{closeArgs: errors.New("gh issue: HTTP 502")}}
	tr := newTracker(r)

	if err := tr.Close(context.Background(), id, "done"); err == nil {
		t.Fatal("want the failed close reported")
	}
	for _, c := range r.calls {
		if strings.HasPrefix(c.args, "issue comment") {
			t.Fatalf("calls = %q: the comment was posted though the close failed, so a retry would post it again", r.calls)
		}
	}
}
