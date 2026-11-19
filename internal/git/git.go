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

	"github.com/Marcel-Goehn/fliesswerk/internal/shell"
)

// NonInteractiveEnv keeps git from ever waiting for a person: no
// credential prompt, no editor, no pager.
var NonInteractiveEnv = []string{"GIT_TERMINAL_PROMPT=0", "GIT_EDITOR=true", "GIT_PAGER=cat"}

// hostEnv is what the Factory's own git runs with. A Sandbox can write
// refs, and a ref under refs/replace would change what git says a commit
// contains, hiding from the Factory that it touched a Protected path.
func hostEnv() []string {
	return append(append(os.Environ(), NonInteractiveEnv...), "GIT_NO_REPLACE_OBJECTS=1")
}

// Repo is a git repository, worked on through the directory Dir (the
// user's checkout or one of the Factory's worktrees).
type Repo struct {
	Dir string
}

// Run runs git in r.Dir and returns its stdout with surrounding white
// space trimmed. The error carries git's stderr.
func (r Repo) Run(ctx context.Context, args ...string) (string, error) {
	out, err := r.output(ctx, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// output runs git in r.Dir and returns its stdout, also when it fails.
func (r Repo) output(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...) //nolint:gosec // git is the tool this package exists to run
	shell.Isolate(cmd)
	cmd.Dir = r.Dir
	cmd.Env = hostEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), &runError{msg: fmt.Sprintf("git %s: %s", strings.Join(args, " "), msg), cause: err}
	}
	return stdout.String(), nil
}

// runError is a git command that failed: git's stderr, and how it ended.
type runError struct {
	msg   string
	cause error
}

func (e *runError) Error() string { return e.msg }
func (e *runError) Unwrap() error { return e.cause }

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
	shell.Isolate(cmd)
	cmd.Dir = r.Dir
	cmd.Env = hostEnv()
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

// ticketBranchKey is the git config variable that marks a branch as a
// Ticket branch. The name can't: a standalone Ticket's branch, which is
// kept, begins with a Ticket branch's whole name.
const ticketBranchKey = "fliesswerkticket"

// MarkTicketBranch marks branch as a Ticket branch. Deleting the branch
// deletes the mark.
func (r Repo) MarkTicketBranch(ctx context.Context, branch string) error {
	_, err := r.Run(ctx, "config", "branch."+branch+"."+ticketBranchKey, "true")
	return err
}

// TicketBranches lists the marked branches whose name starts with prefix.
func (r Repo) TicketBranches(ctx context.Context, prefix string) ([]string, error) {
	branches, err := r.LocalBranches(ctx, prefix)
	if err != nil {
		return nil, err
	}
	var marked []string
	for _, b := range branches {
		v, err := r.Run(ctx, "config", "--type=bool", "--default=false", "--get", "branch."+b+"."+ticketBranchKey)
		if err != nil {
			return nil, err
		}
		if v == "true" {
			marked = append(marked, b)
		}
	}
	return marked, nil
}

// AddWorktree checks branch out in a new worktree at path.
func (r Repo) AddWorktree(ctx context.Context, path, branch string) error {
	_, err := r.Run(ctx, "worktree", "add", "--quiet", path, branch)
	return err
}

// AddDetachedWorktree checks rev out in a new worktree at path, on no
// branch: commits made there move no branch.
func (r Repo) AddDetachedWorktree(ctx context.Context, path, rev string) error {
	_, err := r.Run(ctx, "worktree", "add", "--quiet", "--detach", path, rev)
	return err
}

// AddEmptyWorktree makes a new worktree at path for rev, on no branch and with
// no files checked out, for what only needs a worktree to exist.
func (r Repo) AddEmptyWorktree(ctx context.Context, path, rev string) error {
	_, err := r.Run(ctx, "worktree", "add", "--quiet", "--detach", "--no-checkout", path, rev)
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
	return r.commits(ctx, "log", "--reverse", "--format=%H %s", from+".."+to)
}

// LogTouching is Log, limited to the commits that changed one of files,
// which are relative to the top of the working tree: r.Dir must be there.
// A commit on a side branch counts even if a merge later dropped its
// change.
func (r Repo) LogTouching(ctx context.Context, from, to string, files []string) ([]Commit, error) {
	args := []string{"--literal-pathspecs", "log", "--full-history", "--reverse", "--format=%H %s", from + ".." + to, "--"}
	return r.commits(ctx, append(args, files...)...)
}

// commits runs a git command that prints "<sha> <subject>" lines.
func (r Repo) commits(ctx context.Context, args ...string) ([]Commit, error) {
	out, err := r.Run(ctx, args...)
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
	return sortedPaths(out), nil
}

// sortedPaths are the NUL-separated paths of out, once each and sorted.
func sortedPaths(out string) []string {
	seen := map[string]bool{}
	var files []string
	for _, f := range strings.Split(out, "\x00") {
		if f = strings.TrimSpace(f); f != "" && !seen[f] {
			seen[f] = true
			files = append(files, f)
		}
	}
	sort.Strings(files)
	return files
}

// Changed lists, sorted, the files that differ between from and to. Paths
// are relative to r.Dir, with forward slashes, and files outside it are
// left out. A rename counts as both of its paths.
func (r Repo) Changed(ctx context.Context, from, to string) ([]string, error) {
	out, err := r.Run(ctx, "diff", "--name-only", "--no-renames", "--relative", "-z", from, to)
	if err != nil {
		return nil, err
	}
	return sortedPaths(out), nil
}

// MergeFastForward moves the branch checked out in r.Dir forward to rev,
// failing if that isn't a fast-forward.
func (r Repo) MergeFastForward(ctx context.Context, rev string) error {
	_, err := r.Run(ctx, "merge", "--quiet", "--ff-only", rev)
	return err
}

// ConflictError says a merge has real conflicts.
type ConflictError struct {
	// Files are the conflicting files, relative to the Repo's Dir.
	Files []string
}

func (e *ConflictError) Error() string {
	return "conflict in " + strings.Join(e.Files, ", ")
}

// MergeTree merges theirs into ours without touching a worktree, a branch
// or the index, and returns the merged tree. Real conflicts are a
// *ConflictError.
func (r Repo) MergeTree(ctx context.Context, ours, theirs string) (string, error) {
	out, err := r.output(ctx, "merge-tree", "--write-tree", "--name-only", "--no-messages", "-z", ours, theirs)
	var exit *exec.ExitError
	conflicted := errors.As(err, &exit) && exit.ExitCode() == 1
	if err != nil && !conflicted {
		return "", err
	}
	fields := strings.Split(strings.TrimRight(out, "\x00"), "\x00")
	if conflicted {
		return "", &ConflictError{Files: fields[1:]}
	}
	return fields[0], nil
}

// StartMerge merges rev into HEAD in r.Dir and leaves the merge in
// progress, conflicts and all, for someone to finish with a commit of the
// given message.
func (r Repo) StartMerge(ctx context.Context, rev, message string) error {
	_, err := r.Run(ctx, "merge", "--quiet", "--no-ff", "--no-commit", "-m", message, rev)
	if _, inProgress := r.Run(ctx, "rev-parse", "--verify", "--quiet", "MERGE_HEAD"); inProgress == nil {
		return nil
	}
	return err
}

// Clean reports whether r.Dir has nothing uncommitted: no changes, no
// untracked files, no merge conflicts. Ignored files don't count.
func (r Repo) Clean(ctx context.Context) (bool, error) {
	out, err := r.Run(ctx, "status", "--porcelain")
	return out == "", err
}

// Contains reports whether commit is rev or one of its ancestors.
func (r Repo) Contains(ctx context.Context, rev, commit string) (bool, error) {
	missing, err := r.Log(ctx, rev, commit)
	return len(missing) == 0, err
}

// ConflictMarkers lists those of files that hold conflict markers at rev.
// The files are relative to the top of the working tree: r.Dir must be
// there. A file rev doesn't have holds none. "=======" alone is no marker,
// since it also underlines headings.
func (r Repo) ConflictMarkers(ctx context.Context, rev string, files []string) ([]string, error) {
	args := []string{"--literal-pathspecs", "grep", "-z", "-l", "-I", "-E", "^(<<<<<<<|>>>>>>>) ", rev, "--"}
	out, err := r.output(ctx, append(args, files...)...)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var marked []string
	for _, f := range strings.Split(strings.TrimRight(out, "\x00"), "\x00") {
		marked = append(marked, strings.TrimPrefix(f, rev+":"))
	}
	return marked, nil
}

// CommitTree makes a commit of tree with the given parents and returns
// its SHA. No branch moves.
func (r Repo) CommitTree(ctx context.Context, tree, message string, parents ...string) (string, error) {
	args := []string{"commit-tree", tree, "-m", message}
	for _, p := range parents {
		args = append(args, "-p", p)
	}
	return r.Run(ctx, args...)
}

// ResetHard moves the branch checked out in r.Dir to rev, along with its
// tracked files.
func (r Repo) ResetHard(ctx context.Context, rev string) error {
	_, err := r.Run(ctx, "reset", "--quiet", "--hard", rev)
	return err
}

// ErrPushRejected says the remote refused a push, as opposed to it never
// getting there.
var ErrPushRejected = errors.New("the push was rejected")

// Push moves the branch dst on remote to the local branch src. It is
// never forced, so the remote refuses it unless it is a fast-forward: the
// error is then an ErrPushRejected.
func (r Repo) Push(ctx context.Context, remote, src, dst string) error {
	out, err := r.output(ctx, "push", "--quiet", "--porcelain", remote, "refs/heads/"+src+":refs/heads/"+dst)
	if err != nil && pushRefused(out) {
		return fmt.Errorf("%w: %w", ErrPushRejected, err)
	}
	return err
}

// pushRefused reports whether `git push --porcelain` flagged a ref with
// "!". Its stderr can't tell: it is in the user's language.
func pushRefused(porcelain string) bool {
	for line := range strings.Lines(porcelain) {
		if strings.HasPrefix(line, "!") {
			return true
		}
	}
	return false
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
