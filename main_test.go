package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// buildKraftwerk compiles the kraftwerk binary into a temp dir, passing
// ldflags through to `go build`, and returns its path.
func buildKraftwerk(t *testing.T, ldflags string) string {
	t.Helper()
	name := "kraftwerk"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(t.TempDir(), name)
	args := []string{"build", "-o", bin}
	if ldflags != "" {
		args = append(args, "-ldflags", ldflags)
	}
	cmd := exec.Command("go", append(args, ".")...)
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

func runKraftwerk(t *testing.T, bin string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("run %s: %v", bin, err)
	}
	return out.String(), errOut.String(), code
}

func TestVersionPrintsInjectedVersion(t *testing.T) {
	bin := buildKraftwerk(t, "-X main.version=1.2.3")

	stdout, stderr, code := runKraftwerk(t, bin, "--version")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
	}
	if stdout != "kraftwerk 1.2.3\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "kraftwerk 1.2.3\n")
	}
}

func TestVersionOfDevBuildSaysDev(t *testing.T) {
	bin := buildKraftwerk(t, "")

	stdout, stderr, code := runKraftwerk(t, bin, "--version")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
	}
	if !strings.HasPrefix(stdout, "kraftwerk dev") {
		t.Fatalf("stdout = %q, want it to start with %q", stdout, "kraftwerk dev")
	}
}

func TestUnknownArgumentFailsWithUsage(t *testing.T) {
	bin := buildKraftwerk(t, "")

	stdout, stderr, code := runKraftwerk(t, bin, "frobnicate")

	if code != 3 {
		t.Fatalf("exit code = %d, want 3 (config failure)", code)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want it empty", stdout)
	}
	if !strings.Contains(stderr, `unknown argument "frobnicate"`) || !strings.Contains(stderr, "Usage:") {
		t.Fatalf("stderr = %q, want it to name the argument and show usage", stderr)
	}
}
