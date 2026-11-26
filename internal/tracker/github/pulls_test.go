package github_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Marcel-Goehn/fliesswerk/internal/tracker"
	"github.com/Marcel-Goehn/fliesswerk/internal/tracker/github"
)

const prListArgs = "pr list --repo " + repo + " --head fliesswerk/spec-31-pull-requests --state all --limit 100 --json number,url,state,headRefName"

func TestFindPullRequestReturnsTheOpenOneForTheBranch(t *testing.T) {
	r := &fixtureRunner{t: t, responses: map[string]string{prListArgs: "pr-list-head.json"}}

	url, err := newTracker(r).FindPullRequest(context.Background(), "fliesswerk/spec-31-pull-requests")

	if err != nil {
		t.Fatal(err)
	}
	if url != "https://github.com/Marcel-Goehn/fliesswerk/pull/140" {
		t.Errorf("url = %q, want the open PR's", url)
	}
	if want := []call{{args: prListArgs}}; !slices.Equal(r.calls, want) {
		t.Errorf("calls = %q\nwant    %q", r.calls, want)
	}
}

func TestFindPullRequestCountsNoMergedOrClosedOnes(t *testing.T) {
	for name, r := range map[string]*fixtureRunner{
		"only merged and closed": {responses: map[string]string{prListArgs: "pr-list-head-done.json"}},
		"none at all":            {inline: map[string]string{prListArgs: "[]"}},
		"another branch's open": {inline: map[string]string{prListArgs: `[{"headRefName":"fliesswerk/spec-31-pull-requests-2",` +
			`"number":7,"state":"OPEN","url":"https://github.com/Marcel-Goehn/fliesswerk/pull/7"}]`}},
	} {
		t.Run(name, func(t *testing.T) {
			r.t = t

			url, err := newTracker(r).FindPullRequest(context.Background(), "fliesswerk/spec-31-pull-requests")

			if err != nil {
				t.Fatal(err)
			}
			if url != "" {
				t.Errorf("url = %q, want none", url)
			}
		})
	}
}

func TestFindPullRequestFailsWhenGhFails(t *testing.T) {
	r := &fixtureRunner{t: t, errs: map[string]error{prListArgs: errors.New("gh pr: HTTP 502")}}

	if _, err := newTracker(r).FindPullRequest(context.Background(), "fliesswerk/spec-31-pull-requests"); err == nil {
		t.Fatal("want the failed lookup reported")
	}
}

func TestOpenPullRequestGoesThroughGhWithTheBodyOnStdin(t *testing.T) {
	createArgs := "pr create --repo " + repo + " --head fliesswerk/spec-31-pull-requests --base main --title Pull requests --body-file -"
	for name, tt := range map[string]struct {
		draft bool
		args  string
	}{
		"ready for review": {false, createArgs},
		"as a draft":       {true, createArgs + " --draft"},
	} {
		t.Run(name, func(t *testing.T) {
			r := &fixtureRunner{t: t, inline: map[string]string{tt.args: "https://github.com/Marcel-Goehn/fliesswerk/pull/141\n"}}
			pr := tracker.PullRequest{Head: "fliesswerk/spec-31-pull-requests", Base: "main", Title: "Pull requests", Body: "Closes #31\n", Draft: tt.draft}

			url, err := newTracker(r).OpenPullRequest(context.Background(), pr)

			if err != nil {
				t.Fatal(err)
			}
			if url != "https://github.com/Marcel-Goehn/fliesswerk/pull/141" {
				t.Errorf("url = %q, want the one gh printed", url)
			}
			if want := []call{{args: tt.args, stdin: "Closes #31\n"}}; !slices.Equal(r.calls, want) {
				t.Errorf("calls = %q\nwant    %q", r.calls, want)
			}
		})
	}
}

func TestAnOpenedPullRequestWithoutAURLIsAnError(t *testing.T) {
	args := "pr create --repo " + repo + " --head b --base main --title T --body-file -"
	r := &fixtureRunner{t: t, inline: map[string]string{args: "\n"}}

	if _, err := newTracker(r).OpenPullRequest(context.Background(), tracker.PullRequest{Head: "b", Base: "main", Title: "T"}); err == nil {
		t.Fatal("want an error when gh prints no URL")
	}
}

func TestARateLimitedPullRequestSaysHowLongToWait(t *testing.T) {
	write := "pr create --repo " + repo + " --head b --base main --title T --body-file -"
	r := &limitedRunner{fixtureRunner: fixtureRunner{t: t}, write: write, headers: "HTTP/2.0 403 Forbidden\r\nRetry-After: 90\r\n\r\n{}"}

	_, err := github.New(github.Options{Repo: repo, Runner: r, GOOS: "linux"}).
		OpenPullRequest(context.Background(), tracker.PullRequest{Head: "b", Base: "main", Title: "T"})

	var limited *tracker.RateLimitError
	if !errors.As(err, &limited) || limited.Wait != 90*time.Second {
		t.Fatalf("err = %v, want a RateLimitError of 90s", err)
	}
	if want := []call{{args: "api --include repos/" + repo + "/pulls"}}; !slices.Equal(r.calls, want) {
		t.Errorf("calls = %q, want a probe of the repository's pull requests", r.calls)
	}
}

func TestCanOpenPullRequestsTriesAnEmptyOneThatGitHubMustRefuseAsInvalid(t *testing.T) {
	probe := "api --method POST repos/" + repo + "/pulls --input - --silent"
	for name, tt := range map[string]struct {
		err   error
		wants []string // nil when the credential can open pull requests
	}{
		"allowed: GitHub only finds the empty pull request invalid": {
			err: errors.New("gh api: gh: Invalid request.\n\n\"head\", \"base\" weren't supplied. (HTTP 422)"),
		},
		"a fine-grained token without the permission": {
			err:   errors.New("gh api: gh: Resource not accessible by personal access token (HTTP 403)"),
			wants: []string{repo, "Pull requests", "Read and write", "Resource not accessible"},
		},
		"a login that can't see the repository": {
			err:   errors.New("gh api: gh: Not Found (HTTP 404)"),
			wants: []string{repo, "Pull requests", "Read and write"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := &fixtureRunner{t: t, errs: map[string]error{probe: tt.err}}

			err := newTracker(r).CanOpenPullRequests(context.Background())

			if want := []call{{args: probe, stdin: "{}"}}; !slices.Equal(r.calls, want) {
				t.Errorf("calls = %q\nwant    %q", r.calls, want)
			}
			if tt.wants == nil {
				if err != nil {
					t.Fatalf("err = %v, want none", err)
				}
				return
			}
			if err == nil {
				t.Fatal("want an error naming the permission")
			}
			for _, w := range tt.wants {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("err = %v, want it to name %q", err, w)
				}
			}
		})
	}
}
