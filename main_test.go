package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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
// if dir is empty), with a fake gh that is installed and logged in.
func runIn(t *testing.T, bin, dir string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	return runEnv(t, bin, dir, ghEnv(t, "ok"), args...)
}

// runEnv runs the binary like runIn, with env as its whole environment.
func runEnv(t *testing.T, bin, dir string, env []string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = env
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

var (
	fakeGhOnce sync.Once
	fakeGhPath string
	fakeGhErr  error
)

// fakeGhBinDir builds testdata/fakegh as "gh" once per test binary and
// returns the directory that holds it.
func fakeGhBinDir(t *testing.T) string {
	t.Helper()
	fakeGhOnce.Do(func() {
		dir, err := os.MkdirTemp("", "fakegh")
		if err != nil {
			fakeGhErr = err
			return
		}
		name := "gh"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", filepath.Join(dir, name), "./testdata/fakegh")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			fakeGhErr = fmt.Errorf("go build fakegh: %v\n%s", err, out)
			return
		}
		fakeGhPath = dir
	})
	if fakeGhErr != nil {
		t.Fatal(fakeGhErr)
	}
	return fakeGhPath
}

// ghEnv is an environment whose PATH holds only the fake gh, answering
// from testdata/gh/<fixtures>, with no token of the user's own.
func ghEnv(t *testing.T, fixtures string, extra ...string) []string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("testdata", "gh", fixtures))
	if err != nil {
		t.Fatal(err)
	}
	path := fakeGhBinDir(t) + string(os.PathListSeparator) + gitBinDir(t)
	return append(baseEnv(path), append([]string{"FAKEGH_DIR=" + abs, "XDG_CONFIG_HOME=" + t.TempDir()}, extra...)...)
}

// gitBinDir is a directory that holds git and nothing else of the
// user's (gh is often installed next to git). On Windows, it is git's own
// directory, since git.exe can't be linked elsewhere.
func gitBinDir(t *testing.T) string {
	t.Helper()
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	if runtime.GOOS == "windows" {
		return filepath.Dir(gitBin)
	}
	dir := t.TempDir()
	if err := os.Symlink(gitBin, filepath.Join(dir, "git")); err != nil {
		t.Fatal(err)
	}
	return dir
}

// baseEnv is the test's environment without any gh token, with PATH set to
// path alone.
func baseEnv(path string) []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(name) {
		case "PATH", "GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN":
			continue
		}
		env = append(env, kv)
	}
	return append(env, "PATH="+path)
}

// projectDir writes .kraftwerk/<name> for every file into a new git
// checkout whose .kraftwerk/.gitignore ignores .env (unless files has its
// own), and returns the project root.
func projectDir(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Skipf("no git: %v\n%s", err, out)
	}
	if _, ok := files[".gitignore"]; !ok {
		files[".gitignore"] = "config.local.toml\n.env\nstate/\nlogs/\n"
	}
	if err := os.MkdirAll(filepath.Join(root, ".kraftwerk"), 0o750); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		path := filepath.Join(root, ".kraftwerk", filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
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
	t.Run("gh missing exits 3 naming how to install it", func(t *testing.T) {
		root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})
		_, stderr, code := runEnv(t, bin, root, baseEnv(t.TempDir()+string(os.PathListSeparator)+gitBinDir(t)), "check")
		if code != 3 || !strings.Contains(stderr, "gh is not installed") {
			t.Fatalf("exit code = %d, want 3 saying gh is missing; stderr: %s", code, stderr)
		}
	})

	t.Run("gh too old exits 3 naming the minimum", func(t *testing.T) {
		root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})
		_, stderr, code := runEnv(t, bin, root, ghEnv(t, "old"), "check")
		if code != 3 || !strings.Contains(stderr, "gh 2.45.0 is older than 2.94.0") {
			t.Fatalf("exit code = %d, want 3 naming the versions; stderr: %s", code, stderr)
		}
	})

	t.Run("gh logged out exits 3 naming gh auth login", func(t *testing.T) {
		root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})
		_, stderr, code := runEnv(t, bin, root, ghEnv(t, "loggedout"), "check")
		if code != 3 || !strings.Contains(stderr, "gh auth login") {
			t.Fatalf("exit code = %d, want 3 naming gh auth login; stderr: %s", code, stderr)
		}
	})
}
