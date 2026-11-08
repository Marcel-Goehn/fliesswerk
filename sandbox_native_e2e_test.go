//go:build integration && linux

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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
	if _, err := exec.LookPath("bwrap"); err != nil {
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
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		t.Fatal(err)
	}
	p.extraEnv = append(p.extraEnv, "HOME="+home, "PATH="+strings.Join([]string{fakeClaudeBinDir(t), filepath.Dir(curl), filepath.Dir(bwrap)}, string(os.PathListSeparator)))

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
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
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
