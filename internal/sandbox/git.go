package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Marcel-Goehn/kraftwerk/internal/git"
)

// GitDirs are the directories git keeps a worktree's state in.
type GitDirs struct {
	// Common is the repository's git directory, and Admin this worktree's
	// own part of it, whose name in Common's worktrees/ is ID.
	Common, Admin, ID string
}

// WorktreeGit finds the git directories of worktree, and creates what a
// bind mount of them needs to exist. git would read config.worktree if the
// agent could create it, so it can't be left for the agent to make.
func WorktreeGit(worktree string) (GitDirs, error) {
	data, err := os.ReadFile(filepath.Join(worktree, ".git")) //nolint:gosec // the Factory's own worktree
	if err != nil {
		return GitDirs{}, err
	}
	admin, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: ")
	if !ok {
		return GitDirs{}, fmt.Errorf("%s is not a git worktree", worktree)
	}
	dirs := GitDirs{Common: filepath.Dir(filepath.Dir(admin)), Admin: admin, ID: filepath.Base(admin)}
	if err := os.MkdirAll(filepath.Join(dirs.Common, "logs"), 0o750); err != nil { //nolint:gosec // inside the git directory of the Factory's own worktree
		return GitDirs{}, err
	}
	worktreeConfig := filepath.Join(admin, "config.worktree")
	if _, err := os.Stat(worktreeConfig); errors.Is(err, os.ErrNotExist) { //nolint:gosec // inside the git directory of the Factory's own worktree
		if err := os.WriteFile(worktreeConfig, nil, 0o600); err != nil { //nolint:gosec // inside the git directory of the Factory's own worktree
			return GitDirs{}, err
		}
	}
	return dirs, nil
}

var identLine = regexp.MustCompile(`^(.*) <(.*)> \d+ [+-]\d+$`)

// CommitIdentity is the env that gives commits in a Sandbox the identity
// the user's git would give them. The user's git config isn't in there.
func CommitIdentity(ctx context.Context, root string) []string {
	var env []string
	repo := git.Repo{Dir: root}
	for _, who := range []string{"AUTHOR", "COMMITTER"} {
		out, err := repo.Run(ctx, "var", "GIT_"+who+"_IDENT")
		if m := identLine.FindStringSubmatch(strings.TrimSpace(out)); err == nil && m != nil {
			env = append(env, "GIT_"+who+"_NAME="+m[1], "GIT_"+who+"_EMAIL="+m[2])
		}
	}
	return env
}
