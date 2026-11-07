package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/tracker/fake"
)

var (
	fakeClaudeOnce sync.Once
	fakeClaudeDir  string
	fakeClaudeErr  error
)

// fakeClaudeBinDir builds the scripted fake Harness as "claude" once per
// test binary and returns the directory that holds it.
func fakeClaudeBinDir(t *testing.T) string {
	t.Helper()
	fakeClaudeOnce.Do(func() {
		dir, err := os.MkdirTemp("", "fakeclaude")
		if err != nil {
			fakeClaudeErr = err
			return
		}
		name := "claude"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		cmd := exec.Command("go", "build", "-tags", "kwkfake", "-o", filepath.Join(dir, name), "./internal/harness/fakeclaude")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			fakeClaudeErr = fmt.Errorf("go build fakeclaude: %v\n%s", err, out)
			return
		}
		fakeClaudeDir = dir
	})
	if fakeClaudeErr != nil {
		t.Fatal(fakeClaudeErr)
	}
	return fakeClaudeDir
}

// buildTestKraftwerk compiles kraftwerk with the test-only `kwkfake` tag,
// which lets $KWK_FAKE_TRACKER stand in for the Tracker.
func buildTestKraftwerk(t *testing.T) string {
	t.Helper()
	name := "kraftwerk"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-tags", "kwkfake", "-o", bin, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = isolatedGitEnv(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

var (
	gitConfigOnce sync.Once
	gitConfigPath string
)

// isolatedGitEnv is the test's environment with the user's git config left
// out and a fixed commit identity.
func isolatedGitEnv(t *testing.T) []string {
	t.Helper()
	gitConfigOnce.Do(func() {
		f, err := os.CreateTemp("", "gitconfig")
		if err == nil {
			gitConfigPath = f.Name()
			_ = f.Close()
		}
	})
	return append(os.Environ(), gitIdentity()...)
}

func gitIdentity() []string {
	return []string{
		"GIT_CONFIG_GLOBAL=" + gitConfigPath, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com",
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// runProject is a fixture repo for a Run: a bare remote, the user's
// checkout cloned from it with a committed .kraftwerk/, a fake Tracker
// and a fake Harness script.
type runProject struct {
	remote, work   string
	trackerPath    string
	scriptPath     string
	baseSHA        string
	bin            string
	extraEnv       []string
	stdout, stderr string
}

func newRunProject(t *testing.T, bin string, issues []fake.Issue, script string) *runProject {
	t.Helper()
	dir := t.TempDir()
	p := &runProject{
		remote: filepath.Join(dir, "remote.git"), work: filepath.Join(dir, "work"),
		trackerPath: filepath.Join(dir, "tracker.json"), scriptPath: filepath.Join(dir, "script.json"), bin: bin,
	}
	gitIn(t, dir, "init", "-q", "--bare", "-b", "main", p.remote)
	gitIn(t, dir, "clone", "-q", p.remote, p.work)
	gitIn(t, p.work, "symbolic-ref", "HEAD", "refs/heads/main")
	writeFile(t, filepath.Join(p.work, ".kraftwerk", "config.toml"), "version = 1\n[tracker]\nrepo = \"acme/shop\"\n")
	writeFile(t, filepath.Join(p.work, ".kraftwerk", ".gitignore"), "config.local.toml\nstate/\nlogs/\n.env\n")
	writeFile(t, filepath.Join(p.work, "README.md"), "shop\n")
	gitIn(t, p.work, "add", ".")
	gitIn(t, p.work, "commit", "-q", "-m", "Initial commit")
	gitIn(t, p.work, "push", "-q", "origin", "main")
	p.baseSHA = gitIn(t, p.work, "rev-parse", "HEAD")
	writeFile(t, filepath.Join(p.work, ".kraftwerk", "config.local.toml"), "[sandbox]\nkind = \"none\"\ni_accept_unsandboxed_risk = true\n")

	state, err := json.Marshal(map[string]any{"ready_label": "ready-for-agent", "issues": issues})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, p.trackerPath, string(state))
	writeFile(t, p.scriptPath, script)
	return p
}

// run runs `kraftwerk run` in the user's checkout, with only the fake
// Harness and git on PATH.
func (p *runProject) run(t *testing.T, args ...string) int {
	t.Helper()
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	path := fakeClaudeBinDir(t) + string(os.PathListSeparator) + filepath.Dir(gitBin)
	env := append(baseEnv(path), gitIdentity()...)
	env = append(env, "KWK_FAKE_TRACKER="+p.trackerPath, "KWK_FAKE_HARNESS_SCRIPT="+p.scriptPath)
	env = append(env, p.extraEnv...)
	var code int
	p.stdout, p.stderr, code = runEnv(t, p.bin, p.work, env, append([]string{"run"}, args...)...)
	return code
}

// issues reads the fake Tracker's issues back, by number.
func (p *runProject) issues(t *testing.T) map[int]fake.Issue {
	t.Helper()
	data, err := os.ReadFile(p.trackerPath)
	if err != nil {
		t.Fatal(err)
	}
	var st struct {
		Issues []fake.Issue `json:"issues"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatal(err)
	}
	out := map[int]fake.Issue{}
	for _, i := range st.Issues {
		out[i.Number] = i
	}
	return out
}

func cartIssues() []fake.Issue {
	return []fake.Issue{
		{Number: 1, Title: "Spec: Shopping cart", Body: "Customers need a cart.", Labels: []string{"ready-for-agent"}},
		{Number: 2, Title: "Add the cart", Body: "Make a cart package.", Labels: []string{"ready-for-agent"}, Parent: 1},
	}
}

func TestRunImplementsATicketEndToEnd(t *testing.T) {
	bin := buildTestKraftwerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": [{"match": "Ticket #2", "actions": [
		{"write": "cart/cart.go", "content": "package cart\n"},
		{"run": ["git", "add", "cart/cart.go"]},
		{"run": ["git", "commit", "-q", "-m", "Add the cart package"]}
	], "report": {"outcome": "done", "summary": "Added the cart package."}}]}`)
	// The user works on a branch of their own, with a dirty tree.
	gitIn(t, p.work, "checkout", "-q", "-b", "my-work")
	writeFile(t, filepath.Join(p.work, "README.md"), "unsaved edit\n")

	code := p.run(t)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	spec := "kraftwerk/spec-1-spec-shopping-cart"
	tip := gitIn(t, p.remote, "for-each-ref", "--format=%(objectname)", "refs/heads/"+spec)
	if tip == "" {
		t.Fatalf("%s wasn't pushed; the remote has:\n%s\nstdout: %s", spec, gitIn(t, p.remote, "branch"), p.stdout)
	}
	if got := gitIn(t, p.remote, "log", "--format=%s", p.baseSHA+".."+tip); got != "Add the cart package" {
		t.Errorf("the pushed Spec branch adds %q to main, want the agent's commit", got)
	}
	if got := gitIn(t, p.remote, "for-each-ref", "--format=%(refname:short)"); got != "kraftwerk/spec-1-spec-shopping-cart\nmain" {
		t.Errorf("branches on the remote = %q, want main and the Spec branch only", got)
	}
	if got := gitIn(t, p.remote, "rev-parse", "main"); got != p.baseSHA {
		t.Errorf("main moved on the remote")
	}

	issues := p.issues(t)
	if !issues[2].Closed || issues[1].Closed {
		t.Fatalf("closed: #1 %v, #2 %v; want only #2 closed", issues[1].Closed, issues[2].Closed)
	}
	last := issues[2].Comments[len(issues[2].Comments)-1].Body
	for _, want := range []string{"Added the cart package.", tip[:7], "Add the cart package", spec} {
		if !strings.Contains(last, want) {
			t.Errorf("close comment lacks %q:\n%s", want, last)
		}
	}

	if got := gitIn(t, p.work, "rev-parse", "--abbrev-ref", "HEAD"); got != "my-work" {
		t.Errorf("the user's checkout moved to %s", got)
	}
	if got := gitIn(t, p.work, "status", "--porcelain"); got != "M README.md" {
		t.Errorf("the user's tree changed: %q", got)
	}
	if got := gitIn(t, p.work, "worktree", "list"); strings.Count(got, "\n") != 0 {
		t.Errorf("Factory worktrees were left:\n%s", got)
	}
	if !strings.Contains(p.stderr, "no Sandbox") {
		t.Errorf("stderr lacks the no-Sandbox warning:\n%s", p.stderr)
	}
}

func TestRunWithAFailedTicketExits1AndLeavesItOpen(t *testing.T) {
	bin := buildTestKraftwerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": [{"match": "Ticket #2", "report": {"outcome": "done", "summary": "Nothing to do."}}]}`)

	code := p.run(t)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	if !strings.Contains(p.stdout, "reported done without changes") {
		t.Errorf("stdout lacks the reason:\n%s", p.stdout)
	}
	if issues := p.issues(t); issues[2].Closed || len(issues[2].Comments) != 0 {
		t.Errorf("#2 = %+v, want it open and untouched", issues[2])
	}
	if got := gitIn(t, p.remote, "for-each-ref", "--format=%(refname:short)"); got != "main" {
		t.Errorf("branches on the remote = %q, want main only", got)
	}
}

func TestTheFakeTrackerNeverShips(t *testing.T) {
	bin := buildKraftwerk(t, "")
	p := newRunProject(t, bin, cartIssues(), `{"sessions": []}`)
	p.extraEnv = []string{"FAKEGH_DIR=" + t.TempDir()}

	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	env := append(baseEnv(fakeGhBinDir(t)+string(os.PathListSeparator)+filepath.Dir(gitBin)), "KWK_FAKE_TRACKER="+p.trackerPath)
	env = append(env, p.extraEnv...)
	_, stderr, code := runEnv(t, bin, p.work, env, "run", "--dry-run")

	if code != 2 || !strings.Contains(stderr, "fakegh") {
		t.Fatalf("exit code = %d, want 2 from gh, not the fake Tracker; stderr: %s", code, stderr)
	}
}

func TestRunStopsASilentStepAtItsConfiguredIdleTimeout(t *testing.T) {
	bin := buildTestKraftwerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": [{"match": "Ticket #2", "actions": [{"sleep": "1m"}],
		"report": {"outcome": "done", "summary": "ok"}}]}`)
	writeFile(t, filepath.Join(p.work, ".kraftwerk", "config.local.toml"),
		"[sandbox]\nkind = \"none\"\ni_accept_unsandboxed_risk = true\n[steps.implement]\nidle_timeout = \"1s\"\n")

	start := time.Now()
	code := p.run(t)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	if elapsed := time.Since(start); elapsed > 30*time.Second {
		t.Errorf("the Run took %s; the configured idle timeout didn't apply", elapsed)
	}
	if !strings.Contains(p.stdout, "no output for 1s (idle timeout)") {
		t.Errorf("stdout lacks the idle timeout:\n%s", p.stdout)
	}
}
