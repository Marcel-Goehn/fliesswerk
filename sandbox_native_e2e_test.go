//go:build integration && (linux || darwin)

package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/fliesswerk/internal/sandbox/native"
)

// nativeProbeScript has the fake Harness record what the Sandbox can reach
// and change, and commit it. The real home and the project's own checkout
// must be out of reach.
const nativeProbeScript = `{"sessions": [` + approves + `, {"actions": [
	{"run": ["sh", "-c", "if curl -s --noproxy '*' --max-time 3 -o /dev/null http://1.1.1.1/; then echo reached; else echo cut; fi > net.txt"]},
	{"run": ["sh", "-c", "if test -e \"$REAL_HOME/.ssh/id_rsa\"; then echo open; else echo closed; fi > home.txt"]},
	{"run": ["sh", "-c", "if echo x >> .fliesswerk/config.toml; then echo changed; else echo refused; fi > protected.txt"]},
	{"run": ["git", "add", "net.txt", "home.txt", "protected.txt"]},
	{"run": ["git", "commit", "-q", "-m", "Probe the Sandbox"]}
], "report": {"outcome": "done", "summary": "Probed."}}]}`

func TestARunClosesATicketInsideANativeSandbox(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil && runtime.GOOS == "linux" {
		t.Skip("no bwrap; the native job installs it")
	}
	bin := buildTestFliesswerk(t)
	p := newRunProject(t, bin, cartIssues(), nativeProbeScript)
	// The fake Harness keeps its count of sessions beside its script.
	fake := t.TempDir()
	p.scriptPath = filepath.Join(fake, "script.json")
	writeFile(t, p.scriptPath, nativeProbeScript)
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".ssh", "id_rsa"), "PRIVATE KEY")
	writeFile(t, filepath.Join(p.work, ".fliesswerk", "config.local.toml"), `verify = ["inside"]

[commands]
inside = 'test ! -e "$REAL_HOME/.ssh/id_rsa"'

[sandbox]
kind = "native"
writable_paths = ["`+fake+`"]

[sandbox.env]
FWK_FAKE_HARNESS_SCRIPT = "`+p.scriptPath+`"
REAL_HOME = "`+home+`"

[[hooks.sandbox_created]]
run = 'test ! -e "$REAL_HOME/.ssh/id_rsa"'
`)
	curl, err := exec.LookPath("curl")
	if err != nil {
		t.Fatal(err)
	}
	path := []string{fakeClaudeBinDir(t), filepath.Dir(curl)}
	if runtime.GOOS == "linux" {
		bwrap, err := exec.LookPath("bwrap")
		if err != nil {
			t.Fatal(err)
		}
		path = append(path, filepath.Dir(bwrap))
	} else {
		path = append(path, "/bin") // where macOS keeps sh
	}
	p.extraEnv = append(p.extraEnv, "HOME="+home, "PATH="+strings.Join(path, string(os.PathListSeparator)))

	code := p.run(t)

	if _, err := native.Check(context.Background()); err != nil {
		var blocked *native.BlockedError
		if !errors.As(err, &blocked) {
			t.Fatal(err)
		}
		if code != exitConfigError || !strings.Contains(p.stderr, "sudo ") {
			t.Fatalf("a blocked bwrap must stop the Run with the fix: exit %d, stderr: %s", code, p.stderr)
		}
		return
	}
	if code != 0 {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s\nrun logs:\n%s", code, p.stdout, p.stderr, capturedRunLogs(t, p.work))
	}
	branch := "fliesswerk/spec-1-spec-shopping-cart"
	for file, want := range map[string]string{"net.txt": "cut", "home.txt": "closed", "protected.txt": "refused"} {
		if got := gitIn(t, p.remote, "show", branch+":"+file); got != want {
			t.Errorf("%s = %q, want %q", file, got, want)
		}
	}
	for _, w := range p.writes(t) {
		if w.Op == "close" && w.Ticket == 2 {
			return
		}
	}
	t.Errorf("#2 wasn't closed; writes: %+v", p.writes(t))
}

// capturedRunLogs is what a Run logged, which is where a failing command inside the
// Sandbox said why. The project is gone once the test is.
func capturedRunLogs(t *testing.T, project string) string {
	t.Helper()
	var all strings.Builder
	err := filepath.WalkDir(filepath.Join(project, ".fliesswerk", "logs"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path) //nolint:gosec // a log of the test's own Run
		if err != nil {
			return err
		}
		all.WriteString("--- " + path + "\n" + string(data) + "\n")
		return nil
	})
	if err != nil {
		t.Logf("reading the Run's logs: %v", err)
	}
	return all.String()
}

func TestANativeRunReportsItsMemoryInTheSummaryAndToCheck(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil && runtime.GOOS == "linux" {
		t.Skip("no bwrap; the native job installs it")
	}
	if _, err := native.Check(context.Background()); err != nil {
		t.Skipf("no native Sandbox here: %v", err)
	}
	bin := buildTestFliesswerk(t)
	script := `{"sessions": [` + approves + `, ` + implementsCart("Implement Ticket #2", `package cart\n`) + `]}`
	p := newRunProject(t, bin, cartIssues(), script)
	fake := t.TempDir()
	p.scriptPath = filepath.Join(fake, "script.json")
	writeFile(t, p.scriptPath, script)
	writeFile(t, filepath.Join(p.work, ".fliesswerk", "config.local.toml"), `[sandbox]
kind = "native"
writable_paths = ["`+fake+`"]

[sandbox.env]
FWK_FAKE_HARNESS_SCRIPT = "`+p.scriptPath+`"
`)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	curl, err := exec.LookPath("curl") // `check` probes the Sandbox with it
	if err != nil {
		t.Fatal(err)
	}
	path := []string{fakeClaudeBinDir(t), filepath.Dir(git), filepath.Dir(curl), "/bin"}
	if runtime.GOOS == "linux" {
		bwrap, err := exec.LookPath("bwrap")
		if err != nil {
			t.Fatal(err)
		}
		path = append(path, filepath.Dir(bwrap))
	}
	p.extraEnv = append(p.extraEnv, "HOME="+t.TempDir(), "PATH="+strings.Join(path, string(os.PathListSeparator)))

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s\nrun logs:\n%s", code, p.stdout, p.stderr, capturedRunLogs(t, p.work))
	}
	for _, want := range []string{"Memory: peak estimate", "per Sandbox"} {
		if !strings.Contains(p.stdout, want) {
			t.Errorf("the Run's output lacks %q:\n%s", want, p.stdout)
		}
	}

	if code := p.fwk(t, "check"); code != 0 {
		t.Fatalf("check: exit code = %d\nstderr: %s", code, p.stderr)
	}
	if !strings.Contains(p.stdout, "fliesswerk check: memory: peak estimate") {
		t.Errorf("check doesn't report the memory:\n%s", p.stdout)
	}
}

func TestCheckProvesANativeSandboxAndRunsItsCommands(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil && runtime.GOOS == "linux" {
		t.Skip("no bwrap; the native job installs it")
	}
	bin := buildTestFliesswerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": []}`)
	config := func(extra string) string {
		return `[sandbox]
kind = "native"

[sandbox.network]
allow = ["github.com"]

[commands]
sandboxed = 'test -n "$FLIESSWERK_SANDBOX"'
` + extra + `
[[hooks.sandbox_created]]
run = 'test -n "$FLIESSWERK_SANDBOX"'
`
	}
	writeFile(t, filepath.Join(p.work, ".fliesswerk", "config.local.toml"), config(""))
	curl, err := exec.LookPath("curl")
	if err != nil {
		t.Fatal(err)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	path := []string{fakeClaudeBinDir(t), filepath.Dir(curl), filepath.Dir(git), "/bin"}
	if runtime.GOOS == "linux" {
		bwrap, err := exec.LookPath("bwrap")
		if err != nil {
			t.Fatal(err)
		}
		path = append(path, filepath.Dir(bwrap))
	}
	p.extraEnv = append(p.extraEnv, "HOME="+t.TempDir(), "PATH="+strings.Join(path, string(os.PathListSeparator)))

	code := p.fwk(t, "check", "--commands")

	if _, err := native.Check(context.Background()); err != nil {
		var blocked *native.BlockedError
		if !errors.As(err, &blocked) {
			t.Fatal(err)
		}
		if code != exitConfigError || !strings.Contains(p.stderr, "sudo ") {
			t.Fatalf("a blocked bwrap must stop check with the fix: exit %d, stderr: %s", code, p.stderr)
		}
		return
	}
	if code != exitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	mustContain(t, "stdout", p.stdout,
		"direct connections are blocked",
		"github.com is reachable through the egress proxy",
		"claude ok",
		"claude starts where Steps run",
		"`sandboxed` passes on main",
	)

	writeFile(t, filepath.Join(p.work, ".fliesswerk", "config.local.toml"), config("broken = 'echo nope && exit 3'\n"))
	if code := p.fwk(t, "check", "--commands"); code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", code, exitConfigError, p.stderr)
	}
	mustContain(t, "stderr", p.stderr, "`broken` is already failing on main", "nope")
}

func TestCheckFailsWhenTheHarnessCannotStartInANativeSandbox(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil && runtime.GOOS == "linux" {
		t.Skip("no bwrap; the native job installs it")
	}
	if _, err := native.Check(context.Background()); err != nil {
		t.Skipf("no native Sandbox here: %v", err)
	}
	bin := buildTestFliesswerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": []}`)
	writeFile(t, filepath.Join(p.work, ".fliesswerk", "config.local.toml"), `[sandbox]
kind = "native"

[sandbox.env]
FWK_FAKE_START = "needs_bwrap"
`)
	curl, err := exec.LookPath("curl") // `check` probes the Sandbox with it
	if err != nil {
		t.Fatal(err)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	path := []string{fakeClaudeBinDir(t), filepath.Dir(curl), filepath.Dir(git), "/bin"}
	if runtime.GOOS == "linux" {
		bwrap, err := exec.LookPath("bwrap")
		if err != nil {
			t.Fatal(err)
		}
		path = append(path, filepath.Dir(bwrap))
	}
	p.extraEnv = append(p.extraEnv, "HOME="+t.TempDir(), "PATH="+strings.Join(path, string(os.PathListSeparator)))

	code := p.fwk(t, "check")

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, exitConfigError, p.stdout, p.stderr)
	}
	mustContain(t, "stderr", p.stderr, "claude can't start where Steps run", "bubblewrap is required", "scrub_credentials = false")
	if runtime.GOOS == "linux" {
		mustContain(t, "stderr", p.stderr, "Install it: sudo apt-get install -y bubblewrap")
	}
}
