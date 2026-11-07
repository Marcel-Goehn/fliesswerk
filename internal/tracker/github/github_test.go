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

	"github.com/Marcel-Goehn/kraftwerk/internal/tracker"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker/github"
)

const repo = "Marcel-Goehn/kraftwerk"

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

const listArgs = "issue list --repo " + repo + " --state open --label ready-for-agent --limit 1000 --json number,title,body,labels,state,url,parent,subIssues,blockedBy"
const viewSpecArgs = "issue view 31 --repo " + repo + " --json number,title,body,state,url,subIssues"

func newTracker(r *fixtureRunner) *github.Tracker {
	return github.New(github.Options{Repo: repo, ReadyLabel: "ready-for-agent", Runner: r, GOOS: "linux"})
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
	if s.Title != "Spec: Factory v1 (Kraftwerk)" || !strings.HasPrefix(s.Body, "## Problem Statement") {
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

	t.Run("close comments, then closes as completed", func(t *testing.T) {
		r := &fixtureRunner{t: t}
		if err := newTracker(r).Close(ctx, id, "done"); err != nil {
			t.Fatal(err)
		}
		want := []call{
			{args: "issue comment 35 --repo " + repo + " --body-file -", stdin: "done"},
			{args: "issue close 35 --repo " + repo + " --reason completed"},
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
