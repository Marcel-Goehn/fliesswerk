package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/kraftwerk/internal/tracker/fake"
)

// checkProject is a project with no Sandbox and a remote, whose local config
// gets extra appended.
func checkProject(t *testing.T, extra string) *runProject {
	t.Helper()
	p := newRunProject(t, buildTestKraftwerk(t), []fake.Issue{}, `{"sessions": []}`)
	writeFile(t, filepath.Join(p.work, ".kraftwerk", "config.local.toml"), `[sandbox]
kind = "none"
i_accept_unsandboxed_risk = true
`+extra)
	return p
}

func mustContain(t *testing.T, what, got string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("%s lacks %q:\n%s", what, want, got)
		}
	}
}

func TestCheckCommandsReportsEachCommandOnTheBaseBranch(t *testing.T) {
	p := checkProject(t, `
[commands]
test = "echo all good"

[[hooks.sandbox_created]]
run = "echo created"
`)

	if code := p.kwk(t, "check", "--commands"); code != exitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	mustContain(t, "stdout", p.stdout, "sandbox_created: `echo created` worked", "`test` passes on main")
}

func TestCheckCommandsReportsEveryCommandAlreadyRedOnTheBaseBranch(t *testing.T) {
	p := checkProject(t, `
[commands]
lint = "echo lint is unhappy && exit 3"
test = "echo all good"
typecheck = "echo types are wrong && exit 4"
`)

	code := p.kwk(t, "check", "--commands")

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, exitConfigError, p.stdout, p.stderr)
	}
	mustContain(t, "stderr", p.stderr, "`lint` is already failing on main", "lint is unhappy", "`typecheck` is already failing on main", "types are wrong", "already fail on main")
	mustContain(t, "stdout", p.stdout, "`test` passes on main")
}

func TestCheckCommandsRunOnTheBaseBranchNotTheCheckout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the command is for sh")
	}
	p := checkProject(t, `
[commands]
pushed = "test -f README.md && test ! -f unpushed.txt"
`)
	writeFile(t, filepath.Join(p.work, "unpushed.txt"), "only here\n")
	gitIn(t, p.work, "add", "unpushed.txt")
	gitIn(t, p.work, "commit", "-q", "-m", "Not pushed")

	if code := p.kwk(t, "check", "--commands"); code != exitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
}

func TestCheckCommandsStopAtAFailedSandboxCreatedHook(t *testing.T) {
	p := checkProject(t, `
[commands]
test = "echo must not run"

[[hooks.sandbox_created]]
run = "echo install broke && exit 1"
`)

	code := p.kwk(t, "check", "--commands")

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", code, exitConfigError, p.stderr)
	}
	mustContain(t, "stderr", p.stderr, "install broke", "the Feedback commands weren't run")
	if strings.Contains(p.stdout, "passes on main") {
		t.Errorf("a Feedback command ran:\n%s", p.stdout)
	}
}

func TestCheckCommandsLeavesNoWorktreeBehind(t *testing.T) {
	p := checkProject(t, "\n[commands]\ntest = \"echo fine\"\n")

	if code := p.kwk(t, "check", "--commands"); code != exitOK {
		t.Fatalf("exit code = %d\nstderr: %s", code, p.stderr)
	}
	if got := gitIn(t, p.work, "worktree", "list"); strings.Contains(got, "probe") {
		t.Errorf("a worktree was left:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(p.work, ".kraftwerk", "state", "probe")); !os.IsNotExist(err) {
		t.Errorf("the probe directory is still there: %v", err)
	}
}

func TestCheckCommandsWithoutFeedbackCommandsSaysSo(t *testing.T) {
	p := checkProject(t, "")

	if code := p.kwk(t, "check", "--commands"); code != exitOK {
		t.Fatalf("exit code = %d\nstderr: %s", code, p.stderr)
	}
	mustContain(t, "stdout", p.stdout, "[commands] is empty")
}

func TestCheckCommandsRefusesWhileARunIsInProgress(t *testing.T) {
	p := checkProject(t, "\n[commands]\ntest = \"echo fine\"\n")
	holdRunLock(t, p.work)

	code := p.kwk(t, "check", "--commands")

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", code, exitConfigError, p.stderr)
	}
	mustContain(t, "stderr", p.stderr, "kraftwerk check --commands")
}

func TestCheckDoesARealDryRunPush(t *testing.T) {
	bin := buildKraftwerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})
	remote := gitIn(t, root, "remote", "get-url", "origin")

	stdout, stderr, code := runIn(t, bin, root, checkNoSandbox...)

	if code != exitOK {
		t.Fatalf("exit code = %d\nstderr: %s", code, stderr)
	}
	mustContain(t, "stdout", stdout, "dry-run push")
	if refs := gitIn(t, remote, "for-each-ref", "--format=%(refname)"); strings.Contains(refs, "kraftwerk-check") {
		t.Errorf("the dry run created a branch:\n%s", refs)
	}
}

func TestCheckNamesTheFixForARemoteItCannotUse(t *testing.T) {
	bin := buildKraftwerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})
	gitIn(t, root, "remote", "remove", "origin")

	_, stderr, code := runIn(t, bin, root, checkNoSandbox...)

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", code, exitConfigError, stderr)
	}
	mustContain(t, "stderr", stderr, "base branch main", "origin", "branches.base_branch")
}

func TestCheckTellsWhenTheBaseBranchIsNotOnTheRemote(t *testing.T) {
	bin := buildKraftwerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n[branches]\nbase_branch = \"trunk\"\n"})

	_, stderr, code := runIn(t, bin, root, checkNoSandbox...)

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", code, exitConfigError, stderr)
	}
	mustContain(t, "stderr", stderr, "base branch trunk")
}

// fakeTool puts a script called name that prints output ahead of env's PATH.
func fakeTool(t *testing.T, env []string, name, output string) []string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake tool is a shell script")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\necho '"+output+"'\n"), 0o700); err != nil { //nolint:gosec // an executable the test runs
		t.Fatal(err)
	}
	path := dir
	for _, kv := range env {
		if p, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = dir + string(os.PathListSeparator) + p
		}
	}
	return append(env, "PATH="+path)
}

func TestCheckWantsGitAtItsMinimumVersion(t *testing.T) {
	bin := buildKraftwerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})

	_, stderr, code := runEnv(t, bin, root, fakeTool(t, ghEnv(t, "ok"), "git", "git version 2.30.0"), checkNoSandbox...)

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", code, exitConfigError, stderr)
	}
	mustContain(t, "stderr", stderr, "git 2.30.0 is older than 2.38.0", "install")
}

func TestCheckWantsTheHarnessAtItsMinimumVersionOnTheHostWithNoSandbox(t *testing.T) {
	bin := buildKraftwerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})

	_, stderr, code := runEnv(t, bin, root, fakeTool(t, ghEnv(t, "ok"), "claude", "1.0.0 (Claude Code)"), checkNoSandbox...)

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", code, exitConfigError, stderr)
	}
	mustContain(t, "stderr", stderr, "claude 1.0.0 is older than", "Install it:")
}

func TestCheckNamesTheFixWhenThereIsNoEngine(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake engines are shell scripts")
	}
	bin := buildKraftwerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})
	failing := t.TempDir()
	for _, name := range []string{"docker", "podman"} {
		if err := os.WriteFile(filepath.Join(failing, name), []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil { //nolint:gosec // an executable the test runs
			t.Fatal(err)
		}
	}
	env := ghEnv(t, "ok")
	for _, kv := range env {
		if p, ok := strings.CutPrefix(kv, "PATH="); ok {
			env = append(env, "PATH="+failing+string(os.PathListSeparator)+p)
		}
	}

	_, stderr, code := runEnv(t, bin, root, env, "check")

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", code, exitConfigError, stderr)
	}
	mustContain(t, "stderr", stderr, "needs a running Docker or Podman", "sandbox.kind", "docs.docker.com")
}

func TestCheckCatchesACorporateProxyOnTheHostsOwnLocalhost(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake docker is a shell script")
	}
	bin := buildKraftwerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})
	env := append(withFakeDocker(t, ghEnv(t, "ok"), "linux x86_64"), "HTTPS_PROXY=http://127.0.0.1:3128")

	_, stderr, code := runEnv(t, bin, root, env, "check")

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", code, exitConfigError, stderr)
	}
	mustContain(t, "stderr", stderr, "127.0.0.1:3128", "own localhost", "upstream_proxy", "kind = \"native\"")
}

func TestCheckWarnsAndSkipsTheProbeWhileARunIsInProgress(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake docker is a shell script")
	}
	bin := buildKraftwerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})
	holdRunLock(t, root)

	_, stderr, code := runEnv(t, bin, root, withFakeDocker(t, ghEnv(t, "ok"), "linux x86_64"), "check")

	if code != exitOK {
		t.Fatalf("exit code = %d\nstderr: %s", code, stderr)
	}
	mustContain(t, "stderr", stderr, "a Run is in progress")
}
