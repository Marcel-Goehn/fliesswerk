package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// initProject is a repo that has never seen `init`: a bare remote and a
// checkout of it holding files, pushed.
func initProject(t *testing.T, files map[string]string) *runProject {
	t.Helper()
	dir := t.TempDir()
	p := &runProject{
		remote: filepath.Join(dir, "remote.git"), work: filepath.Join(dir, "work"),
		trackerPath: filepath.Join(dir, "tracker.json"), scriptPath: filepath.Join(dir, "script.json"),
		bin: buildTestKraftwerk(t),
	}
	gitIn(t, dir, "init", "-q", "--bare", "-b", "main", p.remote)
	gitIn(t, dir, "clone", "-q", p.remote, p.work)
	gitIn(t, p.work, "symbolic-ref", "HEAD", "refs/heads/main")
	writeFile(t, filepath.Join(p.work, "README.md"), "shop\n")
	for name, content := range files {
		writeFile(t, filepath.Join(p.work, filepath.FromSlash(name)), content)
	}
	gitIn(t, p.work, "add", ".")
	gitIn(t, p.work, "commit", "-q", "-m", "Initial commit")
	gitIn(t, p.work, "push", "-q", "origin", "main")
	writeFile(t, p.trackerPath, `{"ready_label": "ready-for-agent", "issues": []}`)
	writeFile(t, p.scriptPath, `{"sessions": []}`)
	return p
}

// initEnv is what `init` runs with: the fake Tracker, the fake Harness and
// git, with no container engine and none of the user's tools. Each of bins
// is added as a tool that succeeds silently.
func (p *runProject) initEnv(t *testing.T, bins ...string) []string {
	t.Helper()
	tools := t.TempDir()
	for _, name := range bins {
		if err := os.WriteFile(filepath.Join(tools, name), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil { //nolint:gosec // an executable the test runs
			t.Fatal(err)
		}
	}
	path := strings.Join([]string{tools, fakeClaudeBinDir(t), gitBinDir(t)}, string(os.PathListSeparator))
	env := append(baseEnv(path), gitIdentity()...)
	env = append(env, "KRAFTWERK_ENGINE=") // CI picks one, and these have neither
	p.home = t.TempDir()
	return append(env, "KWK_FAKE_TRACKER="+p.trackerPath, "KWK_FAKE_HARNESS_SCRIPT="+p.scriptPath,
		"HOME="+p.home, "USERPROFILE="+p.home, "XDG_CONFIG_HOME="+filepath.Join(p.home, ".config"), "AppData="+filepath.Join(p.home, "AppData"))
}

// init runs `kraftwerk init` in env, typing one line per answer.
func (p *runProject) init(t *testing.T, env []string, answers ...string) int {
	t.Helper()
	return p.initWith(t, env, nil, answers...)
}

// initWith is init with arguments after `init`.
func (p *runProject) initWith(t *testing.T, env, args []string, answers ...string) int {
	t.Helper()
	cmd := exec.Command(p.bin, append([]string{"init"}, args...)...) //nolint:gosec // the binary under test
	cmd.Dir = p.work
	cmd.Env = env
	cmd.Stdin = strings.NewReader(strings.Join(answers, "\n") + "\n")
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	p.stdout, p.stderr = out.String(), errOut.String()
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	if err != nil {
		t.Fatalf("run init: %v", err)
	}
	return 0
}

// thenSignIn are the project's answers, then the personal ones: a gateway
// that accepts the token, and a token for GitHub.
func (p *runProject) thenSignIn(t *testing.T, project []string) []string {
	t.Helper()
	srv := fakeGateway(t)
	return append(slices.Clone(project), viaGateway(srv)...)
}

func (p *runProject) kraftwerkFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(p.work, ".kraftwerk", filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func (p *runProject) config(t *testing.T) map[string]any {
	t.Helper()
	var values map[string]any
	if err := toml.Unmarshal([]byte(p.kraftwerkFile(t, "config.toml")), &values); err != nil {
		t.Fatalf("config.toml isn't TOML: %v", err)
	}
	return values
}

func nodePostgres() map[string]string {
	pkg, _ := json.Marshal(map[string]any{"scripts": map[string]string{
		"test": "vitest", "typecheck": "tsc --noEmit", "lint": "eslint .", "build": "vite build",
	}})
	return map[string]string{ //nolint:gosec // a made-up database URL in a fixture
		"package.json":          string(pkg),
		"pnpm-lock.yaml":        "lockfileVersion: 9\n",
		".nvmrc":                "v22.11.0\n",
		"compose.yaml":          "services:\n  postgres:\n    image: postgres:17\n    ports: [\"5432:5432\"]\n",
		".env.example":          "DATABASE_URL=postgres://app:app@localhost:5432/app\n",
		".claude/settings.json": "{}\n",
	}
}

// acceptAll answers every project question with its suggestion, after
// naming the repo, which a local remote can't suggest.
var acceptAll = []string{
	"", "acme/shop", "", "", // tracker, repo, base branch, ready label
	"",             // languages
	"", "", "", "", // test, typecheck, lint, format
	"", "", "", "", // Services, Sandbox kind, standards file, protect .claude/
}

func TestInitNodePostgresWritesATeamConfigFromTheRepo(t *testing.T) {
	p := initProject(t, nodePostgres())

	code := p.init(t, p.initEnv(t), p.thenSignIn(t, acceptAll)...)

	if code != exitConfigError { // no container engine in the test's PATH
		t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, exitConfigError, p.stdout, p.stderr)
	}
	values := p.config(t)
	if got := valueAt(values, "tracker", "repo"); got != "acme/shop" {
		t.Errorf("tracker.repo = %v", got)
	}
	if got := valueAt(values, "branches", "base_branch"); got != "main" {
		t.Errorf("base_branch = %v", got)
	}
	if got := valueAt(values, "harness"); got != "claude_code" {
		t.Errorf("harness = %v", got)
	}
	if got := valueAt(values, "sandbox", "kind"); got != "container" {
		t.Errorf("sandbox.kind = %v", got)
	}
	if got := stringsOf(valueAt(values, "sandbox", "network", "allow")); len(got) != 1 || got[0] != "registry.npmjs.org" {
		t.Errorf("allow = %v", got)
	}
	if got := stringsOf(valueAt(values, "verify")); strings.Join(got, ",") != "test,typecheck,lint" {
		t.Errorf("verify = %v", got)
	}
	if got := stringsOf(valueAt(values, "protected_paths")); len(got) != 1 || got[0] != ".claude/" {
		t.Errorf("protected_paths = %v", got)
	}
	for key, want := range map[string]string{"test": "pnpm test", "typecheck": "pnpm typecheck", "lint": "pnpm lint"} {
		if got := valueAt(values, "commands", key); got != want {
			t.Errorf("commands.%s = %v, want %q", key, got, want)
		}
	}
	if got := valueAt(values, "services", "compose_file"); got != "compose.yaml" {
		t.Errorf("compose_file = %v", got)
	}
	if got := valueAt(values, "services", "postgres", "shared"); got != false {
		t.Errorf("services.postgres.shared = %v", got)
	}
	mustContain(t, "config.toml", p.kraftwerkFile(t, "config.toml"),
		`# env = { DATABASE_URL = "postgres://app:app@{host}:{port}/app" }`)

	hooks, _ := valueAt(values, "hooks", "sandbox_created").([]any)
	if len(hooks) != 1 {
		t.Fatalf("sandbox_created = %v", hooks)
	}
	hook, _ := hooks[0].(map[string]any)
	if hook["run"] != "pnpm install --frozen-lockfile" || hook["where"] != "sandbox" {
		t.Errorf("hook = %v", hook)
	}

	mustContain(t, "stderr", p.stderr, "Docker or Podman", "fix the lines above")
	if strings.Contains(p.stdout, "Ready:") {
		t.Errorf("said Ready after a failed check:\n%s", p.stdout)
	}
}

func TestInitWritesEveryDefaultWithAOneLineComment(t *testing.T) {
	p := initProject(t, nodePostgres())
	p.init(t, p.initEnv(t), p.thenSignIn(t, acceptAll)...)

	text := p.kraftwerkFile(t, "config.toml")
	mustContain(t, "config.toml", text, "max_parallel", "max_iterations", "in_progress_label", "cannot_do_label",
		"prefix", "run_args", "read_only_paths", "writable_paths", "scrub_credentials", "ca_certs", "upstream_proxy",
		"keep_days", "idle_timeout", "[sandbox.env]", "[sandbox.secrets]", "[steps.review]", "[steps.implement]", "[steps.merge]")

	key := regexp.MustCompile(`^[a-z_]+\s*=`)
	lines := strings.Split(text, "\n")
	section := ""
	for i, line := range lines {
		if strings.HasPrefix(line, "[") {
			section = line
		}
		if section == "[commands]" || !key.MatchString(line) || strings.Contains(line, " # ") {
			continue
		}
		if i == 0 || !strings.HasPrefix(lines[i-1], "#") && !key.MatchString(lines[i-1]) {
			t.Errorf("line %d has no comment: %s", i+1, line)
		}
		if i > 0 && key.MatchString(lines[i-1]) && !strings.Contains(lines[i-1], " # ") {
			t.Errorf("line %d has no comment of its own: %s", i+1, line)
		}
	}
}

func TestInitContainerfileIsBuiltInOrderAtThePinnedVersion(t *testing.T) {
	p := initProject(t, nodePostgres())
	p.init(t, p.initEnv(t), p.thenSignIn(t, acceptAll)...)

	file := p.kraftwerkFile(t, "Containerfile")
	mustContain(t, "Containerfile", file, "node:22.11.0-bookworm-slim", "corepack enable pnpm", "(from .nvmrc)")
	last := -1
	for _, want := range []string{"debian:bookworm-slim", "git ca-certificates curl ripgrep", "id=kraftwerk-ca", "node:22.11.0", "system packages", "useradd", "USER agent", "claude.ai/install.sh"} {
		at := strings.Index(file, want)
		if at < 0 || at < last {
			t.Fatalf("%q is missing or out of order:\n%s", want, file)
		}
		last = at
	}
	if regexp.MustCompile(`(?m)^[^#].*\bgh\b`).MatchString(file) {
		t.Errorf("the agent's image installs gh:\n%s", file)
	}
	if strings.Contains(file, "TODO(kraftwerk)") {
		t.Errorf("a known language left a TODO:\n%s", file)
	}
}

func TestInitWritesThePromptsAndItsOwnGitignoreOnly(t *testing.T) {
	p := initProject(t, nodePostgres())
	writeFile(t, filepath.Join(p.work, ".gitignore"), "node_modules\n")
	p.init(t, p.initEnv(t), p.thenSignIn(t, acceptAll)...)

	for _, step := range []string{"implement", "review", "merge"} {
		mustContain(t, step, p.kraftwerkFile(t, "prompts/"+step+".md"), "kraftwerk: based on the built-in "+step+" prompt v")
	}
	mustContain(t, ".gitignore", p.kraftwerkFile(t, ".gitignore"), "config.local.toml", ".env", "logs/", "state/")
	root, err := os.ReadFile(filepath.Join(p.work, ".gitignore"))
	if err != nil || string(root) != "node_modules\n" {
		t.Errorf("the root .gitignore changed: %q, %v", root, err)
	}
}

func TestInitAsksTheProjectQuestionsInOrder(t *testing.T) {
	p := initProject(t, nodePostgres())
	p.init(t, p.initEnv(t), p.thenSignIn(t, acceptAll)...)

	last := -1
	for _, q := range []string{"Where do your Tickets live?", "Which GitHub repository", "Which branch should", "Which label marks",
		"What does your project use?", "Which commands tell", "Which compose file", "Where should the agent run?", "Coding standards", "Stop agents from changing .claude/"} {
		at := strings.Index(p.stdout, q)
		if at < 0 || at < last {
			t.Fatalf("%q is missing or out of order:\n%s", q, p.stdout)
		}
		last = at
	}
	mustContain(t, "stdout", p.stdout, "1) In a container", "2) Directly on this machine, fenced in", "3) Directly on this machine, not fenced in")
}

func TestInitNodeGoMonorepoInstallsBothInParallel(t *testing.T) {
	p := initProject(t, map[string]string{
		"web/package.json":         `{"scripts": {"test": "jest", "lint": "eslint ."}, "engines": {"node": ">=20"}}`,
		"web/package-lock.json":    "{}\n",
		"api/go.mod":               "module acme/api\n\ngo 1.23\n",
		"docker-compose.yml":       "services:\n  db:\n    image: postgres:17\n",
		"docs/coding-standards.md": "be kind\n",
	})
	// The remote says github.com, and the git config makes that the fixture.
	gitIn(t, p.work, "push", "-q", "origin", "main:develop")
	gitIn(t, p.work, "fetch", "-q", "origin")
	gitIn(t, p.work, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/develop")
	gitIn(t, p.work, "config", "remote.origin.url", "git@github.com:acme/platform.git")
	gitIn(t, p.work, "config", "url."+p.remote+".insteadOf", "git@github.com:acme/platform.git")

	p.init(t, p.initEnv(t), p.thenSignIn(t, []string{"", "", "", "", "", "", "", "", "", "", "", ""})...)

	values := p.config(t)
	if got := valueAt(values, "tracker", "repo"); got != "acme/platform" {
		t.Errorf("repo = %v\n%s", got, p.stdout)
	}
	if got := valueAt(values, "branches", "base_branch"); got != "develop" {
		t.Errorf("base_branch = %v", got)
	}
	if got := valueAt(values, "standards"); got != "docs/coding-standards.md" {
		t.Errorf("standards = %v", got)
	}
	if got := valueAt(values, "services", "compose_file"); got != "docker-compose.yml" {
		t.Errorf("compose_file = %v", got)
	}
	hooks, _ := valueAt(values, "hooks", "sandbox_created").([]any)
	if len(hooks) != 1 {
		t.Fatalf("sandbox_created = %v", hooks)
	}
	hook, _ := hooks[0].(map[string]any)
	if got := strings.Join(stringsOf(hook["parallel"]), "|"); got != "cd api && go mod download|cd web && npm ci" {
		t.Errorf("parallel = %q", got)
	}
	if got := valueAt(values, "commands", "test"); got != "cd api && go test ./... && cd .. && cd web && npm test" {
		t.Errorf("commands.test = %v", got)
	}
	if got := strings.Join(stringsOf(valueAt(values, "sandbox", "network", "allow")), ","); got != "proxy.golang.org,registry.npmjs.org,sum.golang.org" {
		t.Errorf("allow = %v", got)
	}
	file := p.kraftwerkFile(t, "Containerfile")
	mustContain(t, "Containerfile", file, "node:20-bookworm-slim", "(from package.json engines)", "golang:1.23-bookworm", "(from go.mod)")
	if strings.Contains(file, "corepack") {
		t.Errorf("npm needs no corepack:\n%s", file)
	}
}

func TestInitPythonAndRustGetTheirRecipes(t *testing.T) {
	p := initProject(t, map[string]string{
		"pyproject.toml":      "[project]\nname = \"etl\"\ndependencies = [\"pytest\", \"ruff\"]\n",
		"uv.lock":             "version = 1\n",
		".python-version":     "3.12\n",
		"Cargo.toml":          "[package]\nname = \"etl\"\n",
		"Cargo.lock":          "version = 4\n",
		"rust-toolchain.toml": "[toolchain]\nchannel = \"1.82.0\"\n",
	})

	p.init(t, p.initEnv(t), p.thenSignIn(t, []string{"", "acme/etl", "", "", "", "", "", "", "", "", "", ""})...)

	file := p.kraftwerkFile(t, "Containerfile")
	mustContain(t, "Containerfile", file, "uv python install 3.12", "--default-toolchain 1.82.0")
	values := p.config(t)
	hooks, _ := valueAt(values, "hooks", "sandbox_created").([]any)
	hook, _ := hooks[0].(map[string]any)
	if got := strings.Join(stringsOf(hook["parallel"]), "|"); got != "uv sync --locked|cargo fetch --locked" {
		t.Errorf("parallel = %q", got)
	}
	if got := strings.Join(stringsOf(valueAt(values, "sandbox", "network", "allow")), ","); got != "files.pythonhosted.org,index.crates.io,pypi.org,static.crates.io" {
		t.Errorf("allow = %v", got)
	}
	if got := valueAt(values, "commands", "test"); got != "uv run pytest && cargo test" {
		t.Errorf("commands.test = %v", got)
	}
}

func TestInitUnknownLanguageLeavesATodoThatCheckRefuses(t *testing.T) {
	p := initProject(t, map[string]string{"Gemfile": "source 'https://rubygems.org'\n", "compose.yml": "services:\n  db:\n    image: postgres:17\n"})

	code := p.init(t, p.initEnv(t), p.thenSignIn(t, []string{"", "acme/legacy", "", "", "", "", "", "", "", "", "", ""})...)

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d\nstderr: %s", code, exitConfigError, p.stderr)
	}
	mustContain(t, "Containerfile", p.kraftwerkFile(t, "Containerfile"), "TODO(kraftwerk)", "Gemfile")
	mustContain(t, "stderr", p.stderr, ".kraftwerk/Containerfile:", "TODO(kraftwerk)", "fix the lines above")
	if strings.Contains(p.stdout, "Ready:") {
		t.Errorf("said Ready with a TODO left:\n%s", p.stdout)
	}
}

func TestInitNoSandboxAsksAgainAndStaysOutOfTheTeamConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake pnpm is a shell script")
	}
	p := initProject(t, nodePostgres())
	answers := slices.Clone(acceptAll)
	answers = slices.Insert(answers, 10, "3", "y") // kind, then the second asking
	answers = slices.Delete(answers, 12, 13)

	code := p.init(t, p.initEnv(t, "pnpm"), p.thenSignIn(t, answers)...)

	if code != exitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	mustContain(t, "stdout", p.stdout, "SSH keys", "`test` passes on main", "Ready: commit `.kraftwerk/`, label a Ticket, run `kraftwerk run`")
	if got := valueAt(p.config(t), "sandbox", "kind"); got != "container" {
		t.Errorf("config.toml says kind = %v", got)
	}
	if strings.Contains(p.kraftwerkFile(t, "config.toml"), "i_accept") {
		t.Error("the acknowledgement is in config.toml")
	}
	mustContain(t, "config.local.toml", p.kraftwerkFile(t, "config.local.toml"), `kind = "none"`, "i_accept_unsandboxed_risk = true", `method = "gateway"`)
}

func TestInitDecliningNoSandboxKeepsTheContainerSandbox(t *testing.T) {
	p := initProject(t, nodePostgres())
	answers := slices.Clone(acceptAll)
	answers = slices.Insert(answers, 10, "none", "n")
	answers = slices.Delete(answers, 12, 13)

	p.init(t, p.initEnv(t), p.thenSignIn(t, answers)...)

	if got := valueAt(p.config(t), "sandbox", "kind"); got != "container" {
		t.Errorf("kind = %v", got)
	}
	if strings.Contains(p.kraftwerkFile(t, "config.local.toml"), "i_accept_unsandboxed_risk") {
		t.Errorf("config.local.toml accepts the risk:\n%s", p.kraftwerkFile(t, "config.local.toml"))
	}
}

func TestInitNativeSandboxGetsNoContainerfile(t *testing.T) {
	p := initProject(t, nodePostgres())
	answers := slices.Clone(acceptAll)
	answers[10] = "7" // not an option: asked again
	answers = slices.Insert(answers, 11, "2")

	p.init(t, p.initEnv(t), p.thenSignIn(t, answers)...)

	mustContain(t, "stdout", p.stdout, "Type a number from 1 to 3")
	if got := valueAt(p.config(t), "sandbox", "kind"); got != "native" {
		t.Errorf("kind = %v", got)
	}
	if _, err := os.Stat(filepath.Join(p.work, ".kraftwerk", "Containerfile")); !os.IsNotExist(err) {
		t.Errorf("a native project got a Containerfile: %v", err)
	}
}

func TestInitWritesNothingUntilEveryQuestionIsAnswered(t *testing.T) {
	p := initProject(t, nodePostgres())

	code := p.init(t, p.initEnv(t), "", "acme/shop")

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d", code, exitConfigError)
	}
	mustContain(t, "stderr", p.stderr, "nothing was written")
	if _, err := os.Stat(filepath.Join(p.work, ".kraftwerk")); !os.IsNotExist(err) {
		t.Errorf(".kraftwerk was created: %v", err)
	}
}

func TestInitNeverOverwritesAFile(t *testing.T) {
	p := initProject(t, nodePostgres())
	writeFile(t, filepath.Join(p.work, ".kraftwerk", ".gitignore"), "mine\n.env\nconfig.local.toml\n")

	p.init(t, p.initEnv(t), p.thenSignIn(t, acceptAll)...)

	if got := p.kraftwerkFile(t, ".gitignore"); got != "mine\n.env\nconfig.local.toml\n" {
		t.Errorf(".gitignore = %q", got)
	}
	mustContain(t, "stdout", p.stdout, ".kraftwerk/.gitignore (already there")
}

func TestInitNeedsAGitRepository(t *testing.T) {
	p := initProject(t, nodePostgres())
	p.work = t.TempDir()

	if code := p.init(t, p.initEnv(t), p.thenSignIn(t, acceptAll)...); code != exitConfigError {
		t.Fatalf("exit code = %d, want %d", code, exitConfigError)
	}
	mustContain(t, "stderr", p.stderr, "git")
}

func TestInitYarnProjectsMayReachYarnsRegistry(t *testing.T) {
	for name, lock := range map[string]string{"classic": "# yarn lockfile v1\n", "berry": "__metadata:\n  version: 8\n"} {
		t.Run(name, func(t *testing.T) {
			p := initProject(t, map[string]string{"package.json": `{"scripts": {}}`, "yarn.lock": lock})

			p.init(t, p.initEnv(t), p.thenSignIn(t, []string{"", "acme/shop", "", "", "", "", "", "", "", "", "", ""})...)

			if got := stringsOf(valueAt(p.config(t), "sandbox", "network", "allow")); !slices.Contains(got, "registry.yarnpkg.com") {
				t.Errorf("allow = %v", got)
			}
		})
	}
}

func TestInitPointsAtTheSettingsThatAreNotTheTeams(t *testing.T) {
	p := initProject(t, nodePostgres())
	p.init(t, p.initEnv(t), p.thenSignIn(t, acceptAll)...)

	mustContain(t, "config.toml", p.kraftwerkFile(t, "config.toml"),
		"# token = { stored", "# [harnesses.claude_code]", "# effort = ", "# factory = ")
}
