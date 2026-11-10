package credentials_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/fliesswerk/internal/credentials"
)

// project is a git checkout with a .fliesswerk/ whose .gitignore holds
// ignore, and a credentials file in its own directory.
func project(t *testing.T, ignore string) credentials.Resolver {
	t.Helper()
	root := t.TempDir()
	cmd := exec.Command("git", "init", "-q", root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("no git: %v\n%s", err, out)
	}
	write(t, filepath.Join(root, ".fliesswerk", ".gitignore"), ignore)
	return credentials.Resolver{
		Root:   root,
		Store:  filepath.Join(t.TempDir(), "fliesswerk", "credentials"),
		Getenv: func(string) string { return "" },
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func source(t *testing.T, v map[string]any) credentials.Source {
	t.Helper()
	s, ok := credentials.ParseSource(v)
	if !ok {
		t.Fatalf("ParseSource(%v) failed", v)
	}
	return s
}

func TestEnvSource(t *testing.T) {
	r := project(t, ".env\n")
	r.Getenv = func(name string) string {
		if name == "MY_TOKEN" {
			return "from-env"
		}
		return ""
	}

	got, err := r.Resolve(context.Background(), source(t, map[string]any{"env": "MY_TOKEN"}))
	if err != nil || got != "from-env" {
		t.Errorf("Resolve = %q, %v; want from-env", got, err)
	}

	_, err = r.Resolve(context.Background(), source(t, map[string]any{"env": "UNSET_TOKEN"}))
	if err == nil || !strings.Contains(err.Error(), "UNSET_TOKEN is not set") {
		t.Errorf("Resolve of an unset variable = %v, want it to say so", err)
	}
}

func TestCommandSourceUsesStdout(t *testing.T) {
	r := project(t, ".env\n")
	cmd := "echo from-command"
	got, err := r.Resolve(context.Background(), source(t, map[string]any{"command": cmd}))
	if err != nil || got != "from-command" {
		t.Errorf("Resolve = %q, %v; want from-command", got, err)
	}

	_, err = r.Resolve(context.Background(), source(t, map[string]any{"command": "exit 3"}))
	if err == nil || !strings.Contains(err.Error(), "exit 3") {
		t.Errorf("Resolve of a failing command = %v, want it to name the command", err)
	}

	_, err = r.Resolve(context.Background(), source(t, map[string]any{"command": "exit 0"}))
	if err == nil || !strings.Contains(err.Error(), "printed nothing") {
		t.Errorf("Resolve of a command that prints nothing = %v, want it to say so", err)
	}
}

func TestStoredSource(t *testing.T) {
	r := project(t, ".env\n")
	if err := credentials.Store(r.Store, "claude", "from-store"); err != nil {
		t.Fatal(err)
	}
	if err := credentials.Store(r.Store, "github", "other"); err != nil {
		t.Fatal(err)
	}

	got, err := r.Resolve(context.Background(), source(t, map[string]any{"stored": "claude"}))
	if err != nil || got != "from-store" {
		t.Errorf("Resolve = %q, %v; want from-store", got, err)
	}
	_, err = r.Resolve(context.Background(), source(t, map[string]any{"stored": "missing"}))
	if err == nil || !strings.Contains(err.Error(), "fliesswerk auth set missing") {
		t.Errorf("Resolve of a missing key = %v, want it to name `fliesswerk auth set missing`", err)
	}
}

func TestStoreOverwritesAKeyAndIsUserOnly(t *testing.T) {
	r := project(t, ".env\n")
	for _, v := range []string{"old", "new"} {
		if err := credentials.Store(r.Store, "claude", v); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := r.Resolve(context.Background(), source(t, map[string]any{"stored": "claude"})); err != nil || got != "new" {
		t.Errorf("Resolve = %q, %v; want the newer value", got, err)
	}
	if runtime.GOOS == "windows" {
		return // a user-only ACL, not a mode
	}
	fi, err := os.Stat(r.Store)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("credentials file mode = %v, want 0600", fi.Mode().Perm())
	}
}

func TestStoredSourceRefusesAFileOthersCanRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("modes don't apply")
	}
	r := project(t, ".env\n")
	if err := credentials.Store(r.Store, "claude", "v"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(r.Store, 0o644); err != nil { //nolint:gosec // a file others can read is what this test needs
		t.Fatal(err)
	}
	_, err := r.Resolve(context.Background(), source(t, map[string]any{"stored": "claude"}))
	if err == nil || !strings.Contains(err.Error(), "chmod 600") {
		t.Errorf("Resolve = %v, want a refusal naming chmod 600", err)
	}
}

func TestDotenvSource(t *testing.T) {
	r := project(t, ".env\n")
	write(t, filepath.Join(r.Root, ".fliesswerk", ".env"), "# a comment\n"+
		"PLAIN=plain-value\n"+
		"export EXPORTED = exported-value\n"+
		"DOUBLE=\"double # not a comment\"\n"+
		"SINGLE='single'\n"+
		"TRAILING=value # a comment\n")

	for key, want := range map[string]string{
		"PLAIN": "plain-value", "EXPORTED": "exported-value", "DOUBLE": "double # not a comment",
		"SINGLE": "single", "TRAILING": "value",
	} {
		got, err := r.Resolve(context.Background(), source(t, map[string]any{"dotenv": key}))
		if err != nil || got != want {
			t.Errorf("Resolve(%s) = %q, %v; want %q", key, got, err, want)
		}
	}
	_, err := r.Resolve(context.Background(), source(t, map[string]any{"dotenv": "MISSING"}))
	if err == nil || !strings.Contains(err.Error(), "MISSING") {
		t.Errorf("Resolve of a missing key = %v, want it named", err)
	}
}

func TestADotenvThatIsNotIgnoredIsRefused(t *testing.T) {
	r := project(t, "config.local.toml\n")
	write(t, filepath.Join(r.Root, ".fliesswerk", ".env"), "KEY=value\n")

	_, err := r.Resolve(context.Background(), source(t, map[string]any{"dotenv": "KEY"}))
	if err == nil || !strings.Contains(err.Error(), "not git-ignored") {
		t.Errorf("Resolve = %v, want a refusal", err)
	}
	if err := credentials.CheckDotenvIgnored(context.Background(), r.Root); err == nil || !strings.Contains(err.Error(), ".fliesswerk/.gitignore") {
		t.Errorf("CheckDotenvIgnored = %v, want it to name the fix in .fliesswerk/.gitignore", err)
	}
}

func TestParseSourceTakesExactlyOneKnownSource(t *testing.T) {
	for _, v := range []any{
		"a plain value",
		map[string]any{},
		map[string]any{"env": "A", "stored": "b"},
		map[string]any{"vault": "x"},
	} {
		if _, ok := credentials.ParseSource(v); ok {
			t.Errorf("ParseSource(%v) succeeded", v)
		}
	}
}
