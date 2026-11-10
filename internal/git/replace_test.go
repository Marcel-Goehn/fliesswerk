package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/kraftwerk/internal/git"
)

func TestAReplaceRefCannotHideWhatACommitTouched(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "base")
	base := run("rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "protected"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "touch")
	touching := run("rev-parse", "HEAD")
	innocent := run("commit-tree", "-p", base, "-m", "nothing to see", base+"^{tree}")
	run("replace", touching, innocent)

	repo := git.Repo{Dir: dir}
	ctx := context.Background()
	touched, err := repo.Touched(ctx, base, touching)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := repo.Changed(ctx, base, touching)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(touched, "protected") || !slices.Contains(changed, "protected") {
		t.Errorf("touched %q, changed %q: the replace ref hid the commit", touched, changed)
	}
}
