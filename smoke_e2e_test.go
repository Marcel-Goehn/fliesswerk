//go:build smoke

package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// TestPublishedRelease is the tag pipeline's last step: it runs what the
// install script put on the PATH, on each OS. FLIESSWERK_SMOKE_TAG is the tag
// that was installed. Only `-tags smoke` builds it, so PRs never run it.
func TestPublishedRelease(t *testing.T) {
	tag := os.Getenv("FLIESSWERK_SMOKE_TAG")
	if tag == "" {
		t.Fatal("FLIESSWERK_SMOKE_TAG isn't set")
	}
	want := "fliesswerk " + strings.TrimPrefix(tag, "v") + "\n"
	var bin string
	for _, name := range []string{"fwk", "fliesswerk"} {
		var err error
		if bin, err = exec.LookPath(name); err != nil {
			t.Fatalf("the install didn't put %s on the PATH: %v", name, err)
		}
		if stdout, stderr, code := runFliesswerk(t, bin, "--version"); code != 0 || stdout != want {
			t.Fatalf("%s --version: exit code = %d, stdout = %q, want %q; stderr: %s", name, code, stdout, want, stderr)
		}
	}

	root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})
	stdout, stderr, code := runEnv(t, bin, root, append(ghEnv(t, "ok"), configHome(t)...), checkNoSandbox...)

	if code != exitOK {
		t.Fatalf("fliesswerk check: exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	mustContain(t, "stdout", stdout, "the config is valid", "no Sandbox to probe")
}
