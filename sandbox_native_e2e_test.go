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

	"github.com/Marcel-Goehn/kraftwerk/internal/sandbox/native"
)

// nativeProbeScript has the fake Harness record what the Sandbox can reach
// and change, and commit it. The real home and the project's own checkout
// must be out of reach.
const nativeProbeScript = `{"sessions": [` + approves + `, {"actions": [
	{"run": ["sh", "-c", "if curl -s --noproxy '*' --max-time 3 -o /dev/null http://1.1.1.1/; then echo reached; else echo cut; fi > net.txt"]},
	{"run": ["sh", "-c", "if test -e \"$REAL_HOME/.ssh/id_rsa\"; then echo open; else echo closed; fi > home.txt"]},
	{"run": ["sh", "-c", "if echo x >> .kraftwerk/config.toml; then echo changed; else echo refused; fi > protected.txt"]},
	{"run": ["git", "add", "net.txt", "home.txt", "protected.txt"]},
	{"run": ["git", "commit", "-q", "-m", "Probe the Sandbox"]}
], "report": {"outcome": "done", "summary": "Probed."}}]}`

func TestARunClosesATicketInsideANativeSandbox(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil && runtime.GOOS == "linux" {
		t.Skip("no bwrap; the native job installs it")
	}
	bin := buildTestKraftwerk(t)
	p := newRunProject(t, bin, cartIssues(), nativeProbeScript)
	// The fake Harness keeps its count of sessions beside its script.
	fake := t.TempDir()
	p.scriptPath = filepath.Join(fake, "script.json")
	writeFile(t, p.scriptPath, nativeProbeScript)
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".ssh", "id_rsa"), "PRIVATE KEY")
	writeFile(t, filepath.Join(p.work, ".kraftwerk", "config.local.toml"), `verify = ["inside"]

[commands]
inside = 'test ! -e "$REAL_HOME/.ssh/id_rsa"'

[sandbox]
kind = "native"
writable_paths = ["`+fake+`"]

[sandbox.env]
KWK_FAKE_HARNESS_SCRIPT = "`+p.scriptPath+`"
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
	branch := "kraftwerk/spec-1-spec-shopping-cart"
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
	err := filepath.WalkDir(filepath.Join(project, ".kraftwerk", "logs"), func(path string, d fs.DirEntry, err error) error {
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
	bin := buildTestKraftwerk(t)
	script := `{"sessions": [` + approves + `, ` + implementsCart("Implement Ticket #2", `package cart\n`) + `]}`
	p := newRunProject(t, bin, cartIssues(), script)
	fake := t.TempDir()
	p.scriptPath = filepath.Join(fake, "script.json")
	writeFile(t, p.scriptPath, script)
	writeFile(t, filepath.Join(p.work, ".kraftwerk", "config.local.toml"), `[sandbox]
kind = "native"
writable_paths = ["`+fake+`"]

[sandbox.env]
KWK_FAKE_HARNESS_SCRIPT = "`+p.scriptPath+`"
`)
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	path := []string{fakeClaudeBinDir(t), filepath.Dir(git), "/bin"}
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

	if code := p.kwk(t, "check"); code != 0 {
		t.Fatalf("check: exit code = %d\nstderr: %s", code, p.stderr)
	}
	if !strings.Contains(p.stdout, "kraftwerk check: memory: peak estimate") {
		t.Errorf("check doesn't report the memory:\n%s", p.stdout)
	}
}
