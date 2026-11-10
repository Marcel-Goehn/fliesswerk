package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/fliesswerk/internal/git"
)

// cloneOf is a checkout of a new bare remote that has one commit on main.
func cloneOf(t *testing.T) (repo git.Repo, remote string) {
	t.Helper()
	dir := t.TempDir()
	remote = filepath.Join(dir, "remote.git")
	work := filepath.Join(dir, "work")
	run := func(in string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = in
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run(dir, "init", "-q", "--bare", "-b", "main", remote)
	run(dir, "clone", "-q", remote, work)
	run(work, "symbolic-ref", "HEAD", "refs/heads/main")
	run(work, "commit", "-q", "--allow-empty", "-m", "base")
	run(work, "push", "-q", "origin", "main")
	return git.Repo{Dir: work}, remote
}

func TestADryRunPushProvesTheRemoteAndCreatesNothing(t *testing.T) {
	ctx := context.Background()
	repo, remote := cloneOf(t)
	if err := repo.Fetch(ctx, "origin", "main"); err != nil {
		t.Fatal(err)
	}

	if err := repo.DryRunPush(ctx, "origin", "refs/remotes/origin/main", "fliesswerk/check"); err != nil {
		t.Fatal(err)
	}

	out, err := git.Repo{Dir: remote}.Run(ctx, "for-each-ref", "--format=%(refname)")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "fliesswerk/check") {
		t.Errorf("the dry run created a branch:\n%s", out)
	}
}

func TestADryRunPushToARemoteThatIsGoneFails(t *testing.T) {
	ctx := context.Background()
	repo, remote := cloneOf(t)
	if err := repo.Fetch(ctx, "origin", "main"); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(remote); err != nil {
		t.Fatal(err)
	}

	if err := repo.DryRunPush(ctx, "origin", "refs/remotes/origin/main", "fliesswerk/check"); err == nil {
		t.Fatal("the dry run succeeded against a remote that is gone")
	}
}

func TestReadVersion(t *testing.T) {
	for _, c := range []struct {
		output, want string
		ok           bool
	}{
		{"git version 2.43.0\n", "2.43.0", true},
		{"git version 2.47.1.windows.2\n", "2.47.1", true},
		{"git version 2.39.5 (Apple Git-154)\n", "2.39.5", true},
		{"nonsense", "", false},
	} {
		got, err := git.ReadVersion(c.output)
		if got != c.want || (err == nil) != c.ok {
			t.Errorf("ReadVersion(%q) = %q, %v", c.output, got, err)
		}
	}
}
