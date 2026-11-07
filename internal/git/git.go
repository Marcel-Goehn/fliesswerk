// Package git runs the git commands the Factory needs, on the host, with
// the user's own git credentials. Nothing here ever checks out, resets or
// writes into the user's checkout: branches are made with `git branch`
// and worked in worktrees the Factory owns.
package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// NonInteractiveEnv keeps git from ever waiting for a person: no
// credential prompt, no editor, no pager.
var NonInteractiveEnv = []string{"GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true", "GIT_PAGER=cat"}

// Repo is a git repository, worked on through the directory Dir (the
// user's checkout or one of the Factory's worktrees).
type Repo struct {
	Dir string
}

// Run runs git in r.Dir and returns its stdout with surrounding white
// space trimmed. The error carries git's stderr.
func (r Repo) Run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // git is the tool this package exists to run
	cmd.Dir = r.Dir
	cmd.Env = append(os.Environ(), NonInteractiveEnv...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// Prefix is r.Dir relative to the root of its working tree, "" at the
// root. Git works it out, so it holds whatever path r.Dir is reached by
// (a symlink, a short Windows name).
func (r Repo) Prefix(ctx context.Context) (string, error) {
	out, err := r.Run(ctx, "rev-parse", "--show-prefix")
	if err != nil {
		return "", err
	}
	return filepath.FromSlash(strings.TrimSuffix(out, "/")), nil
}

// IsIgnored reports whether git ignores path, relative to r.Dir.
func (r Repo) IsIgnored(ctx context.Context, path string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "check-ignore", "-q", "--", path) //nolint:gosec // path is a fixed path in the runtime state
	cmd.Dir = r.Dir
	cmd.Env = append(os.Environ(), NonInteractiveEnv...)
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &exit) && exit.ExitCode() == 1:
		return false, nil
	}
	return false, fmt.Errorf("git check-ignore %s: %w", path, err)
}

// Fetch updates the remote-tracking ref of one branch of remote.
func (r Repo) Fetch(ctx context.Context, remote, branch string) error {
	_, err := r.Run(ctx, "fetch", "--quiet", "--no-tags", remote,
		"+refs/heads/"+branch+":refs/remotes/"+remote+"/"+branch)
	return err
}

// LocalBranches lists the local branches whose name starts with prefix.
func (r Repo) LocalBranches(ctx context.Context, prefix string) ([]string, error) {
	out, err := r.Run(ctx, "for-each-ref", "--format=%(refname)", "refs/heads/")
	if err != nil {
		return nil, err
	}
	return withPrefix(strings.Fields(out), "refs/heads/", prefix), nil
}

// RemoteBranches lists the branches of remote whose name starts with
// prefix, asking the remote itself.
func (r Repo) RemoteBranches(ctx context.Context, remote, prefix string) ([]string, error) {
	out, err := r.Run(ctx, "ls-remote", "--heads", remote)
	if err != nil {
		return nil, err
	}
	var refs []string
	for _, line := range strings.Split(out, "\n") {
		if _, ref, ok := strings.Cut(line, "\t"); ok {
			refs = append(refs, ref)
		}
	}
	return withPrefix(refs, "refs/heads/", prefix), nil
}

func withPrefix(refs []string, namespace, prefix string) []string {
	var out []string
	for _, ref := range refs {
		if name, ok := strings.CutPrefix(ref, namespace); ok && strings.HasPrefix(name, prefix) {
			out = append(out, name)
		}
	}
	return out
}

// HasBranch reports whether the local branch exists.
func (r Repo) HasBranch(ctx context.Context, branch string) bool {
	_, err := r.Run(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// CreateBranch makes branch point at start, without checking it out.
func (r Repo) CreateBranch(ctx context.Context, branch, start string) error {
	_, err := r.Run(ctx, "branch", "--no-track", branch, start)
	return err
}

// DeleteBranch deletes a local branch, merged or not.
func (r Repo) DeleteBranch(ctx context.Context, branch string) error {
	_, err := r.Run(ctx, "branch", "-D", branch)
	return err
}

// AddWorktree checks branch out in a new worktree at path.
func (r Repo) AddWorktree(ctx context.Context, path, branch string) error {
	_, err := r.Run(ctx, "worktree", "add", "--quiet", path, branch)
	return err
}

// RemoveWorktree removes the worktree at path, with whatever it holds.
func (r Repo) RemoveWorktree(ctx context.Context, path string) error {
	_, err := r.Run(ctx, "worktree", "remove", "--force", path)
	return err
}

// Rev is the SHA a revision names.
func (r Repo) Rev(ctx context.Context, rev string) (string, error) {
	return r.Run(ctx, "rev-parse", "--verify", "--quiet", rev+"^{commit}")
}

// Commit is one commit, for reporting.
type Commit struct {
	SHA     string
	Subject string
}

// Short is the commit's abbreviated SHA.
func (c Commit) Short() string { return c.SHA[:min(len(c.SHA), 7)] }

// Log lists the commits reachable from to but not from from, oldest first.
func (r Repo) Log(ctx context.Context, from, to string) ([]Commit, error) {
	out, err := r.Run(ctx, "log", "--reverse", "--format=%H %s", from+".."+to)
	if err != nil {
		return nil, err
	}
	var commits []Commit
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		sha, subject, _ := strings.Cut(line, " ")
		commits = append(commits, Commit{SHA: sha, Subject: subject})
	}
	return commits, nil
}

// Touched lists, once each and sorted, the files the commits reachable
// from to but not from from changed, even if a later one changed them
// back. Paths are relative to r.Dir, with forward slashes, and files
// outside it are left out. A rename counts as both of its paths.
func (r Repo) Touched(ctx context.Context, from, to string) ([]string, error) {
	out, err := r.Run(ctx, "log", "--format=", "--name-only", "--no-renames", "--relative", "-z", from+".."+to)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var files []string
	for _, f := range strings.Split(out, "\x00") {
		if f = strings.TrimSpace(f); f != "" && !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	sort.Strings(files)
	return files, nil
}

// MergeFastForward moves the branch checked out in r.Dir forward to rev,
// failing if that isn't a fast-forward.
func (r Repo) MergeFastForward(ctx context.Context, rev string) error {
	_, err := r.Run(ctx, "merge", "--quiet", "--ff-only", rev)
	return err
}

// Push moves the branch dst on remote to the local branch src. It is
// never forced, so the remote refuses it unless it is a fast-forward.
func (r Repo) Push(ctx context.Context, remote, src, dst string) error {
	_, err := r.Run(ctx, "push", "--quiet", "--porcelain", remote, "refs/heads/"+src+":refs/heads/"+dst)
	return err
}

// PruneWorktrees forgets worktrees whose directory is gone.
func (r Repo) PruneWorktrees(ctx context.Context) error {
	_, err := r.Run(ctx, "worktree", "prune")
	return err
}

// Message is one commit with its full message.
type Message struct {
	SHA  string
	Text string
}

// Short is the commit's abbreviated SHA.
func (m Message) Short() string { return m.SHA[:min(len(m.SHA), 7)] }

// Messages lists the commits `git log --no-merges args…` names, newest
// first, with their full messages.
func (r Repo) Messages(ctx context.Context, args ...string) ([]Message, error) {
	out, err := r.Run(ctx, append([]string{"log", "--no-merges", "--format=%H%x00%B%x1e"}, args...)...)
	if err != nil {
		return nil, err
	}
	var msgs []Message
	for _, rec := range strings.Split(out, "\x1e") {
		sha, text, ok := strings.Cut(strings.TrimSpace(rec), "\x00")
		if ok {
			msgs = append(msgs, Message{SHA: sha, Text: strings.TrimSpace(text)})
		}
	}
	return msgs, nil
}
