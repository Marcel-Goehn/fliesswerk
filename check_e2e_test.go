package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/fliesswerk/internal/tracker/fake"
)

// checkProject is a project with no Sandbox and a remote, whose local config
// gets extra appended.
func checkProject(t *testing.T, extra string) *runProject {
	t.Helper()
	p := newRunProject(t, buildTestFliesswerk(t), []fake.Issue{}, `{"sessions": []}`)
	writeFile(t, filepath.Join(p.work, ".fliesswerk", "config.local.toml"), `[sandbox]
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

	if code := p.fwk(t, "check", "--commands"); code != exitOK {
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

	code := p.fwk(t, "check", "--commands")

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

	if code := p.fwk(t, "check", "--commands"); code != exitOK {
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

	code := p.fwk(t, "check", "--commands")

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

	if code := p.fwk(t, "check", "--commands"); code != exitOK {
		t.Fatalf("exit code = %d\nstderr: %s", code, p.stderr)
	}
	if got := gitIn(t, p.work, "worktree", "list"); strings.Contains(got, "probe") {
		t.Errorf("a worktree was left:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(p.work, ".fliesswerk", "state", "probe")); !os.IsNotExist(err) {
		t.Errorf("the probe directory is still there: %v", err)
	}
}

func TestCheckCommandsWithoutFeedbackCommandsSaysSo(t *testing.T) {
	p := checkProject(t, "")

	if code := p.fwk(t, "check", "--commands"); code != exitOK {
		t.Fatalf("exit code = %d\nstderr: %s", code, p.stderr)
	}
	mustContain(t, "stdout", p.stdout, "[commands] is empty")
}

func TestCheckCommandsRefusesWhileARunIsInProgress(t *testing.T) {
	p := checkProject(t, "\n[commands]\ntest = \"echo fine\"\n")
	holdRunLock(t, p.work)

	code := p.fwk(t, "check", "--commands")

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", code, exitConfigError, p.stderr)
	}
	mustContain(t, "stderr", p.stderr, "fliesswerk check --commands")
}

func TestCheckDoesARealDryRunPush(t *testing.T) {
	bin := buildFliesswerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})
	remote := gitIn(t, root, "remote", "get-url", "origin")

	stdout, stderr, code := runIn(t, bin, root, checkNoSandbox...)

	if code != exitOK {
		t.Fatalf("exit code = %d\nstderr: %s", code, stderr)
	}
	mustContain(t, "stdout", stdout, "dry-run push")
	if refs := gitIn(t, remote, "for-each-ref", "--format=%(refname)"); strings.Contains(refs, "fliesswerk-check") {
		t.Errorf("the dry run created a branch:\n%s", refs)
	}
}

func TestCheckNamesTheFixForARemoteItCannotUse(t *testing.T) {
	bin := buildFliesswerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})
	gitIn(t, root, "remote", "remove", "origin")

	_, stderr, code := runIn(t, bin, root, checkNoSandbox...)

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", code, exitConfigError, stderr)
	}
	mustContain(t, "stderr", stderr, "base branch main", "origin", "branches.base_branch")
}

func TestCheckTellsWhenTheBaseBranchIsNotOnTheRemote(t *testing.T) {
	bin := buildFliesswerk(t, "")
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
	bin := buildFliesswerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})

	_, stderr, code := runEnv(t, bin, root, fakeTool(t, ghEnv(t, "ok"), "git", "git version 2.30.0"), checkNoSandbox...)

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", code, exitConfigError, stderr)
	}
	mustContain(t, "stderr", stderr, "git 2.30.0 is older than 2.38.0", "install")
}

func TestCheckWantsTheHarnessAtItsMinimumVersionOnTheHostWithNoSandbox(t *testing.T) {
	bin := buildFliesswerk(t, "")
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
	bin := buildFliesswerk(t, "")
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
	mustContain(t, "stderr", stderr, "sandbox.engine is docker, and it didn't respond", "start it")
	if strings.Contains(stderr, "podman") {
		t.Errorf("fell back to Podman, which is on PATH as well:\n%s", stderr)
	}
}

func TestCheckCatchesACorporateProxyOnTheHostsOwnLocalhost(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake docker is a shell script")
	}
	bin := buildFliesswerk(t, "")
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
	bin := buildFliesswerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})
	holdRunLock(t, root)

	_, stderr, code := runEnv(t, bin, root, withFakeDocker(t, ghEnv(t, "ok"), "linux x86_64"), "check")

	if code != exitOK {
		t.Fatalf("exit code = %d\nstderr: %s", code, stderr)
	}
	mustContain(t, "stderr", stderr, "a Run is in progress")
}

func TestCheckRefusesWhileATodoLineIsLeft(t *testing.T) {
	p := checkProject(t, "")
	writeFile(t, filepath.Join(p.work, ".fliesswerk", "Containerfile"), "FROM scratch\n# TODO(fliesswerk): install Ruby\n")

	code := p.fwk(t, "check")

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", code, exitConfigError, p.stderr)
	}
	mustContain(t, "stderr", p.stderr, ".fliesswerk/Containerfile:2", "TODO(fliesswerk): install Ruby")
}

func TestCheckFailsWhenTheHarnessCannotStartWithAStepsScrubbing(t *testing.T) {
	p := checkProject(t, "")
	p.extraEnv = []string{"FWK_FAKE_START=needs_bwrap"}

	code := p.fwk(t, "check")

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, exitConfigError, p.stdout, p.stderr)
	}
	mustContain(t, "stderr", p.stderr, "bubblewrap is required for subprocess env scrubbing", "scrub_credentials = false", "can read the Harness's credential and the project secrets")
	if runtime.GOOS == "linux" {
		mustContain(t, "stderr", p.stderr, "sudo apt-get install -y bubblewrap")
	}
}

func TestCheckSaysTheHarnessStartsWhenItOnlyLacksALogin(t *testing.T) {
	p := checkProject(t, "")

	if code := p.fwk(t, "check"); code != exitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	mustContain(t, "stdout", p.stdout, "claude ok\n", "claude starts where Steps run")
}

func TestCheckOnlyWarnsWhenItCannotTellWhetherTheHarnessStarts(t *testing.T) {
	for _, start := range []string{"unclear", "hang"} {
		t.Run(start, func(t *testing.T) {
			p := checkProject(t, "")
			p.extraEnv = []string{"FWK_FAKE_START=" + start}

			if code := p.fwk(t, "check"); code != exitOK {
				t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
			}
			if start == "unclear" {
				mustContain(t, "stderr", p.stderr, "warning: can't tell whether claude starts where Steps run", "something nobody expected")
			} else {
				mustContain(t, "stderr", p.stderr, "warning: claude didn't finish starting where Steps run within")
			}
			if strings.Contains(p.stdout, "claude starts where Steps run") {
				t.Errorf("check says it started:\n%s", p.stdout)
			}
		})
	}
}

func TestCheckStartsTheHarnessWithAStepsScrubbingAndNoCredential(t *testing.T) {
	for _, scrub := range []bool{true, false} {
		t.Run(fmt.Sprintf("scrub_credentials = %t", scrub), func(t *testing.T) {
			p := checkProject(t, fmt.Sprintf(`scrub_credentials = %t

[sandbox.secrets]
DB_PASSWORD = { env = "FWK_TEST_DB_PASSWORD" }

[harnesses.claude_code]
method = "oauth_token"
secret = { env = "CLAUDE_CODE_OAUTH_TOKEN" }
`, scrub))
			logPath := filepath.Join(t.TempDir(), "harness.jsonl")
			userHome := t.TempDir()
			p.extraEnv = []string{"HOME=" + userHome, "XDG_CONFIG_HOME=" + filepath.Join(userHome, ".config"), "AppData=" + filepath.Join(userHome, "AppData"),
				"FWK_FAKE_HARNESS_LOG=" + logPath, "FWK_FAKE_START=needs_bwrap",
				"CLAUDE_CODE_OAUTH_TOKEN=a-claude-credential", "ANTHROPIC_API_KEY=an-api-key",
				"CLAUDE_CODE_USE_BEDROCK=1", "CLAUDE_CONFIG_DIR=" + filepath.Join(userHome, ".claude"),
				"FWK_TEST_DB_PASSWORD=s3cr3t-hunter2", "FWK_FAKE_LOG_ENV=DB_PASSWORD,FWK_TEST_DB_PASSWORD,HOME"}

			code := p.fwk(t, "check")

			if want := map[bool]int{true: exitConfigError, false: exitOK}[scrub]; code != want {
				t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, want, p.stdout, p.stderr)
			}
			calls := harnessCalls(t, logPath)
			if len(calls) != 1 {
				t.Fatalf("the Harness was started %d times, want once", len(calls))
			}
			got := calls[0]
			if v, ok := got.Env["CLAUDE_CODE_SUBPROCESS_ENV_SCRUB"]; ok != scrub || ok && v != "1" {
				t.Errorf("CLAUDE_CODE_SUBPROCESS_ENV_SCRUB = %q (set: %t), want it set exactly when scrubbing", v, ok)
			}
			for _, name := range []string{"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CONFIG_DIR", "DB_PASSWORD", "FWK_TEST_DB_PASSWORD"} {
				if v, ok := got.Env[name]; ok {
					t.Errorf("the Harness got %s=%q", name, v)
				}
			}
			if strings.Contains(strings.Join(got.Argv, " "), "awsCredentialExport") {
				t.Errorf("the Harness got a credential command: %q", got.Argv)
			}
			if home := got.Env["HOME"]; home == "" || strings.HasPrefix(home, userHome) {
				t.Errorf("HOME = %q, want a fresh one, not the user's own", home)
			}
		})
	}
}
