package github

import (
	"cmp"
	"context"
	"fmt"
	"strings"

	"github.com/Marcel-Goehn/fliesswerk/internal/tracker"
)

// prListLimit bounds the pull requests one branch is looked up in. gh
// lists the newest first, and a branch has at most one open one.
const prListLimit = "100"

// ghPull is a pull request as `gh pr list --json` prints it.
type ghPull struct {
	Number      int    `json:"number"`
	URL         string `json:"url"`
	State       string `json:"state"`
	HeadRefName string `json:"headRefName"`
}

// FindPullRequest lists every pull request from head, whatever its state,
// and returns the URL of the open one. Merged and closed ones don't count.
func (t *Tracker) FindPullRequest(ctx context.Context, head string) (string, error) {
	args := append(t.args("pr", "list", ""), "--head", head, "--state", "all", "--limit", prListLimit, "--json", "number,url,state,headRefName")
	var pulls []ghPull
	if err := t.runJSON(ctx, &pulls, args...); err != nil {
		return "", fmt.Errorf("looking up the pull request of %s: %w", head, err)
	}
	for _, p := range pulls {
		if p.HeadRefName == head && strings.EqualFold(p.State, "open") {
			return p.URL, nil
		}
	}
	return "", nil
}

// OpenPullRequest opens a pull request with `gh pr create`. The body goes
// to gh on stdin, so no body is ever too long for a command line. gh
// prints the new pull request's URL.
func (t *Tracker) OpenPullRequest(ctx context.Context, pr tracker.PullRequest) (string, error) {
	args := append(t.args("pr", "create", ""), "--head", pr.Head, "--base", pr.Base, "--title", pr.Title, "--body-file", "-")
	if pr.Draft {
		args = append(args, "--draft")
	}
	out, err := t.writeAt(ctx, t.pullsPath(), []byte(pr.Body), args...)
	if err != nil {
		return "", fmt.Errorf("opening a pull request from %s: %w", pr.Head, err)
	}
	for _, line := range strings.Fields(string(out)) {
		if strings.HasPrefix(line, "https://") {
			return line, nil
		}
	}
	return "", fmt.Errorf("opening a pull request from %s: gh printed no URL: %q", pr.Head, strings.TrimSpace(string(out)))
}

// CanOpenPullRequests asks GitHub to open an empty pull request. GitHub
// checks the credential's permission before what it was sent, so a
// credential that may open pull requests gets 422 for the missing head and
// base, and nothing is ever opened.
func (t *Tracker) CanOpenPullRequests(ctx context.Context) error {
	_, err := t.opts.Runner.Run(ctx, []byte("{}"), "api", "--method", "POST", t.pullsPath(), "--input", "-", "--silent")
	if err == nil || strings.Contains(err.Error(), "HTTP 422") {
		return nil
	}
	return fmt.Errorf("your login or `tracker.token` can't open pull requests in %s (%v). Give the token Pull requests: Read and write under Repository permissions (a classic token needs the repo scope), or set `pull_request.open = false`",
		cmp.Or(t.opts.Repo, "the repository"), err)
}

// pullsPath is the REST path of the repository's pull requests.
func (t *Tracker) pullsPath() string {
	return "repos/" + cmp.Or(t.opts.Repo, "{owner}/{repo}") + "/pulls"
}
