package git_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/fliesswerk/internal/git"
)

// A worktree git can't remove itself, as on Windows when a container left
// Linux symlinks in it, is still removed, folder and all.
func TestAWorktreeGitFailsToRemoveIsRemovedAnyway(t *testing.T) {
	ctx := context.Background()
	repo, _ := cloneOf(t)
	wt := filepath.Join(t.TempDir(), "ticket-2")
	if err := repo.AddDetachedWorktree(ctx, wt, "main"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(wt, ".venv", "lib"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".venv", "lib", "site.py"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// A broken .git makes `git worktree remove --force` refuse.
	if err := os.WriteFile(filepath.Join(wt, ".git"), []byte("gitdir: /nowhere\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Run(ctx, "worktree", "remove", "--force", wt); err == nil {
		t.Fatal("git removed the worktree itself; the test no longer makes it fail")
	}

	if err := repo.RemoveWorktree(ctx, wt); err != nil {
		t.Errorf("RemoveWorktree: %v", err)
	}

	if _, err := os.Lstat(wt); !os.IsNotExist(err) {
		t.Errorf("the worktree's folder is left: %v", err)
	}
	list, err := repo.Run(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(list, "worktree "); n != 1 {
		t.Errorf("git still knows the worktree:\n%s", list)
	}
}

func TestTheMainCheckoutIsNeverRemoved(t *testing.T) {
	ctx := context.Background()
	t.Run("with a .git directory", func(t *testing.T) {
		repo, _ := cloneOf(t)
		if err := repo.RemoveWorktree(ctx, repo.Dir); err == nil {
			t.Error("RemoveWorktree of the main checkout succeeded")
		}
		if _, err := os.Stat(filepath.Join(repo.Dir, ".git", "HEAD")); err != nil {
			t.Errorf("the main checkout is gone: %v", err)
		}
	})
	t.Run("with a .git file", func(t *testing.T) {
		dir := t.TempDir()
		work := filepath.Join(dir, "work")
		repo := git.Repo{Dir: dir}
		if _, err := repo.Run(ctx, "init", "-q", "--separate-git-dir", filepath.Join(dir, "repo.git"), work); err != nil {
			t.Fatal(err)
		}
		readme := filepath.Join(work, "README.md")
		if err := os.WriteFile(readme, []byte("mine\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := (git.Repo{Dir: work}).RemoveWorktree(ctx, work); err == nil {
			t.Error("RemoveWorktree of the main checkout succeeded")
		}
		if _, err := os.Stat(readme); err != nil {
			t.Errorf("the main checkout is gone: %v", err)
		}
	})
}

// A Sandbox may have put a repository of its own where the worktree's .git
// file was; the Factory's worktree is still removed.
func TestAWorktreeWithAPlantedGitDirectoryIsRemoved(t *testing.T) {
	for _, from := range []string{"the top of the checkout", "a subdirectory"} {
		t.Run(from, func(t *testing.T) {
			repo, _ := cloneOf(t)
			if from == "a subdirectory" {
				repo.Dir = filepath.Join(repo.Dir, "project")
				if err := os.Mkdir(repo.Dir, 0o750); err != nil {
					t.Fatal(err)
				}
			}
			plantedRemoved(t, repo)
		})
	}
}

func plantedRemoved(t *testing.T, repo git.Repo) {
	t.Helper()
	ctx := context.Background()
	wt := filepath.Join(t.TempDir(), "ticket-2")
	if err := repo.AddDetachedWorktree(ctx, wt, "main"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(wt, ".git")); err != nil {
		t.Fatal(err)
	}
	if _, err := (git.Repo{Dir: wt}).Run(ctx, "init", "-q", wt); err != nil {
		t.Fatal(err)
	}

	if err := repo.RemoveWorktree(ctx, wt); err != nil {
		t.Errorf("RemoveWorktree: %v", err)
	}

	if _, err := os.Lstat(wt); !os.IsNotExist(err) {
		t.Errorf("the worktree's folder is left: %v", err)
	}
}

func TestAWorktreeThatCannotBeDeletedSaysWhatIsLeftAndWhere(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only folder only keeps its files on Unix, and not from root")
	}
	ctx := context.Background()
	repo, _ := cloneOf(t)
	wt := filepath.Join(t.TempDir(), "ticket-2")
	if err := repo.AddDetachedWorktree(ctx, wt, "main"); err != nil {
		t.Fatal(err)
	}
	stuck := filepath.Join(wt, "stuck")
	if err := os.MkdirAll(stuck, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stuck, "file"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(stuck, 0o500); err != nil { //nolint:gosec // a folder nothing can be deleted from
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(stuck, 0o750) }) //nolint:gosec // so the test's temp dir can go

	err := repo.RemoveWorktree(ctx, wt)

	if err == nil {
		t.Fatal("RemoveWorktree succeeded")
	}
	if want := "the folder " + wt + " is left behind"; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to say %q", err, want)
	}
}
