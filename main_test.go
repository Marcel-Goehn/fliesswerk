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
	return runIn(t, bin, "", args...)
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

// runIn runs the binary with dir as its working directory (the test's own,
// if dir is empty).
func runIn(t *testing.T, bin, dir string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
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

// projectDir writes .kraftwerk/<name> for every file and returns the
// project root.
func projectDir(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".kraftwerk"), 0o750); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, ".kraftwerk", name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestCheck(t *testing.T) {
	bin := buildKraftwerk(t, "")

	t.Run("valid config exits 0", func(t *testing.T) {
		root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})
		stdout, stderr, code := runIn(t, bin, root, "check")
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
		}
		if !strings.Contains(stdout, "valid") {
			t.Fatalf("stdout = %q, want it to say the config is valid", stdout)
		}
	})

	t.Run("finds the project from a subdirectory", func(t *testing.T) {
		root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})
		sub := filepath.Join(root, "src", "pkg")
		if err := os.MkdirAll(sub, 0o750); err != nil {
			t.Fatal(err)
		}
		if _, stderr, code := runIn(t, bin, sub, "check"); code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
		}
	})

	t.Run("invalid config exits 3 with every finding", func(t *testing.T) {
		root := projectDir(t, map[string]string{
			"config.toml":       "version = 1\nmax_paralel = 2\n[sandbox.env]\nAPI_TOKEN = \"x\"\n",
			"config.local.toml": "[logs]\nkeep_days = 0\n",
		})
		_, stderr, code := runIn(t, bin, root, "check")
		if code != 3 {
			t.Fatalf("exit code = %d, want 3; stderr: %s", code, stderr)
		}
		for _, want := range []string{
			`error: .kraftwerk/config.toml: max_paralel: unknown key; did you mean "max_parallel"?`,
			"error: .kraftwerk/config.local.toml: logs.keep_days:",
			"warning: .kraftwerk/config.toml: sandbox.env.API_TOKEN: looks like a secret",
		} {
			if !strings.Contains(stderr, want) {
				t.Errorf("stderr lacks %q:\n%s", want, stderr)
			}
		}
	})

	t.Run("warnings alone exit 0", func(t *testing.T) {
		root := projectDir(t, map[string]string{"config.toml": "version = 1\n[sandbox.env]\nAPI_TOKEN = \"x\"\n"})
		_, stderr, code := runIn(t, bin, root, "check")
		if code != 0 || !strings.Contains(stderr, "warning:") {
			t.Fatalf("exit code = %d, want 0 with a warning; stderr: %s", code, stderr)
		}
	})

	t.Run("no project exits 3 naming init", func(t *testing.T) {
		_, stderr, code := runIn(t, bin, t.TempDir(), "check")
		if code != 3 || !strings.Contains(stderr, "kraftwerk init") {
			t.Fatalf("exit code = %d, want 3 naming init; stderr: %s", code, stderr)
		}
	})

	t.Run("CLI flags override config files", func(t *testing.T) {
		root := projectDir(t, map[string]string{"config.toml": "version = 1\n[sandbox]\nkind = \"container\"\n"})
		_, stderr, code := runIn(t, bin, root, "check", "--sandbox", "none")
		if code != 3 || !strings.Contains(stderr, "i_accept_unsandboxed_risk") {
			t.Fatalf("exit code = %d, want 3 asking for the acknowledgement; stderr: %s", code, stderr)
		}
		_, stderr, code = runIn(t, bin, root, "check", "--sandbox", "none", "--i-accept-unsandboxed-risk",
			"--allow", "*", "--model", "cheap", "--max-parallel", "3")
		if code != 0 {
			t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
		}
		if !strings.Contains(stderr, "no Sandbox") || !strings.Contains(stderr, "any host") {
			t.Fatalf("stderr = %q, want warnings for no Sandbox and an open network", stderr)
		}
		_, stderr, code = runIn(t, bin, root, "check", "--max-parallel", "0")
		if code != 3 || !strings.Contains(stderr, "command line: max_parallel") {
			t.Fatalf("exit code = %d, want 3 blaming the command line; stderr: %s", code, stderr)
		}
	})

	t.Run("bad flag exits 3", func(t *testing.T) {
		root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})
		_, stderr, code := runIn(t, bin, root, "check", "--frobnicate")
		if code != 3 || !strings.Contains(stderr, "frobnicate") {
			t.Fatalf("exit code = %d, want 3 naming the flag; stderr: %s", code, stderr)
		}
	})
}
