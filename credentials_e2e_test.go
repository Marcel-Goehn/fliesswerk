package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Marcel-Goehn/fliesswerk/internal/tracker/fake"
)

// configHome is an environment whose user config directory (where `auth
// set` keeps the credentials file) is a fresh temp dir, on every OS.
func configHome(t *testing.T) []string {
	t.Helper()
	home := t.TempDir()
	return []string{"HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "AppData=" + filepath.Join(home, "AppData")}
}

// authSet runs `fliesswerk auth set key` with value piped to stdin.
func authSet(t *testing.T, bin string, env []string, key, value string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(bin, "auth", "set", key)
	cmd.Env = append(baseEnv(""), env...)
	cmd.Stdin = strings.NewReader(value + "\n")
	var out, errOut strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return out.String(), errOut.String(), code
}

// findCredentialsFile finds the one credentials file under dir.
func findCredentialsFile(t *testing.T, dir string) string {
	t.Helper()
	var found []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "credentials" {
			found = append(found, p)
		}
		return nil
	})
	if len(found) != 1 {
		t.Fatalf("credentials files under %s: %q, want one", dir, found)
	}
	return found[0]
}

func TestAuthSetStoresASecretInAUserOnlyFile(t *testing.T) {
	bin := buildFliesswerk(t, "")
	env := configHome(t)

	stdout, stderr, code := authSet(t, bin, env, "claude_api_key", "sk-ant-api03-secret")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
	}
	if strings.Contains(stdout+stderr, "sk-ant-api03-secret") {
		t.Errorf("the secret was echoed:\n%s%s", stdout, stderr)
	}
	if !strings.Contains(stdout, `secret = { stored = "claude_api_key" }`) {
		t.Errorf("stdout = %q, want it to say how to use the key", stdout)
	}
	home := strings.TrimPrefix(env[0], "HOME=")
	path := findCredentialsFile(t, home)
	data, err := os.ReadFile(path) //nolint:gosec // the test's own temp file
	if err != nil || !strings.Contains(string(data), "sk-ant-api03-secret") {
		t.Errorf("credentials file = %q, %v; want it to hold the secret", data, err)
	}
	if fi, err := os.Stat(path); runtime.GOOS != "windows" && (err != nil || fi.Mode().Perm() != 0o600) {
		t.Errorf("credentials file mode = %v, %v; want 0600", fi.Mode().Perm(), err)
	}
}

func TestAuthSetRefusesBadInput(t *testing.T) {
	bin := buildFliesswerk(t, "")
	env := configHome(t)

	for _, tt := range []struct {
		name, key, value, want string
	}{
		{"an empty secret", "claude_api_key", "", "empty"},
		{"a key that can't name a secret", "my key", "v", "can't name a secret"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, stderr, code := authSet(t, bin, env, tt.key, tt.value)
			if code != 3 || !strings.Contains(stderr, tt.want) {
				t.Errorf("exit code = %d, stderr = %q; want 3 saying %q", code, stderr, tt.want)
			}
		})
	}
}

func TestAuthSetRefusesASubscriptionTokenAndWritesNothing(t *testing.T) {
	bin := buildFliesswerk(t, "")
	env := configHome(t)
	if _, stderr, code := authSet(t, bin, env, "claude_api_key", "an-api-key"); code != 0 {
		t.Fatalf("auth set: %s", stderr)
	}
	path := findCredentialsFile(t, strings.TrimPrefix(env[0], "HOME="))
	before, err := os.ReadFile(path) //nolint:gosec // the test's own temp file
	if err != nil {
		t.Fatal(err)
	}

	_, stderr, code := authSet(t, bin, env, "claude", "sk-ant-oat01-secret")

	if code != 3 {
		t.Errorf("exit code = %d, want 3; stderr: %s", code, stderr)
	}
	for _, want := range []string{"`fliesswerk auth set` doesn't take it", `{ env = "CLAUDE_CODE_OAUTH_TOKEN" }`} {
		mustContain(t, "stderr", stderr, want)
	}
	if after, err := os.ReadFile(path); err != nil || string(after) != string(before) { //nolint:gosec // the test's own temp file
		t.Errorf("credentials file = %q, %v; want it unchanged", after, err)
	}
}

func TestCheckRefusesADotenvThatIsNotGitIgnored(t *testing.T) {
	bin := buildFliesswerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n", ".gitignore": "config.local.toml\n"})

	_, stderr, code := runIn(t, bin, root, "check")

	if code != 3 || !strings.Contains(stderr, ".fliesswerk/.env is not git-ignored") {
		t.Fatalf("exit code = %d, want 3 refusing the .env; stderr: %s", code, stderr)
	}
}

// fakeAnthropic is a local HTTPS server standing in for Anthropic or a
// gateway, and a proxy every other HTTPS request of the Factory goes
// through. It counts the requests that reach either.
func fakeAnthropic(t *testing.T) (endpoint, proxy string, requests *atomic.Int32) {
	t.Helper()
	requests = &atomic.Int32{}
	count := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	})
	tls := httptest.NewTLSServer(count)
	plain := httptest.NewServer(count)
	t.Cleanup(tls.Close)
	t.Cleanup(plain.Close)
	return tls.URL, plain.URL, requests
}

// fakeGateway is a gateway that fails the test on any request: only Claude
// Code sends one, and the fake Harness never does.
func fakeGateway(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		t.Errorf("the Factory sent %s %s to the gateway, want no request", req.Method, req.URL.Path)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func gatewayConfig(base string) string {
	return "[harnesses.claude_code]\nmethod = \"gateway\"\nbase_url = \"" + base + "\"\nsecret = { stored = \"gw\" }\n"
}

func TestCheckSendsNoClaudeCredentialAndSaysClaudeCodeChecksIt(t *testing.T) {
	bin := buildFliesswerk(t, "")
	endpoint, proxy, requests := fakeAnthropic(t)
	for _, tt := range []struct{ method, config string }{
		{"oauth_token", "[harnesses.claude_code]\nmethod = \"oauth_token\"\nsecret = { env = \"CLAUDE_CODE_OAUTH_TOKEN\" }\n"},
		{"api_key", "[harnesses.claude_code]\nmethod = \"api_key\"\nsecret = { stored = \"gw\" }\n"},
		{"gateway", gatewayConfig(endpoint)},
	} {
		t.Run(tt.method, func(t *testing.T) {
			env := configHome(t)
			authSet(t, bin, env, "gw", "a-claude-credential")
			root := projectDir(t, map[string]string{"config.toml": "version = 1\n", "config.local.toml": tt.config})
			env = append(append(ghEnv(t, "ok"), env...), "CLAUDE_CODE_OAUTH_TOKEN=a-claude-credential", "HTTPS_PROXY="+proxy, "HTTP_PROXY="+proxy, "NO_PROXY=", "https_proxy="+proxy, "http_proxy="+proxy, "no_proxy=")
			requests.Store(0)

			stdout, stderr, code := runEnv(t, bin, root, env, checkNoSandbox...)

			if code != 0 {
				t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
			}
			if n := requests.Load(); n != 0 {
				t.Errorf("the Factory sent %d requests to the provider, want none", n)
			}
			mustContain(t, "stdout", stdout, "claude_code credential ("+tt.method+") found, not tested ahead of time", "Claude Code checks it on the first Step")
			if strings.Contains(stdout, "credential ("+tt.method+") ok") {
				t.Errorf("stdout claims the credential works:\n%s", stdout)
			}
		})
	}

	t.Run("a missing credential still stops check and names how to store it", func(t *testing.T) {
		root := projectDir(t, map[string]string{"config.toml": "version = 1\n", "config.local.toml": gatewayConfig(endpoint)})
		_, stderr, code := runEnv(t, bin, root, append(ghEnv(t, "ok"), configHome(t)...), "check")
		if code != 3 || !strings.Contains(stderr, "fliesswerk auth set gw") {
			t.Fatalf("exit code = %d, want 3 naming `fliesswerk auth set gw`; stderr: %s", code, stderr)
		}
	})

	t.Run("a stored subscription token is rejected", func(t *testing.T) {
		root := projectDir(t, map[string]string{"config.toml": "version = 1\n",
			"config.local.toml": "[harnesses.claude_code]\nmethod = \"oauth_token\"\nsecret = { stored = \"claude\" }\n"})
		_, stderr, code := runEnv(t, bin, root, append(ghEnv(t, "ok"), configHome(t)...), "check")
		if code != 3 || !strings.Contains(stderr, `{ env = "CLAUDE_CODE_OAUTH_TOKEN" }`) {
			t.Fatalf("exit code = %d, want 3 naming where the token can come from; stderr: %s", code, stderr)
		}
	})

	t.Run("gateway without base_url is rejected", func(t *testing.T) {
		root := projectDir(t, map[string]string{"config.toml": "version = 1\n",
			"config.local.toml": "[harnesses.claude_code]\nmethod = \"gateway\"\nsecret = { stored = \"gw\" }\n"})
		_, stderr, code := runEnv(t, bin, root, append(ghEnv(t, "ok"), configHome(t)...), "check")
		if code != 3 || !strings.Contains(stderr, "needs `base_url`") {
			t.Fatalf("exit code = %d, want 3 asking for base_url; stderr: %s", code, stderr)
		}
	})
}

// harnessCalls reads every invocation the fake Harness logged.
func harnessCalls(t *testing.T, path string) []struct {
	Argv []string          `json:"argv"`
	Env  map[string]string `json:"env"`
} {
	t.Helper()
	var calls []struct {
		Argv []string          `json:"argv"`
		Env  map[string]string `json:"env"`
	}
	f, err := os.Open(path) //nolint:gosec // the test's own temp file
	if os.IsNotExist(err) {
		return calls
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var c struct {
			Argv []string          `json:"argv"`
			Env  map[string]string `json:"env"`
		}
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		calls = append(calls, c)
	}
	return calls
}

const noSandbox = "[sandbox]\nkind = \"none\"\ni_accept_unsandboxed_risk = true\n"

func TestARunDeliversSecretsToTheStepAndRedactsThemEverywhere(t *testing.T) {
	bin := buildTestFliesswerk(t)
	srv := fakeGateway(t)
	// The agent leaks both secrets: into a tool call the terminal shows,
	// and into its report, which becomes the close comment.
	p := newRunProject(t, bin, cartIssues(), `{"sessions": [`+approves+`, {"match": "Ticket #2", "expand": true, "actions": [
		{"run": ["git", "commit", "-q", "--allow-empty", "-m", "token $ANTHROPIC_AUTH_TOKEN db $DB_PASSWORD"]}
	], "report": {"outcome": "done", "summary": "Used $ANTHROPIC_AUTH_TOKEN and $DB_PASSWORD."}}]}`)
	writeFile(t, filepath.Join(p.work, ".fliesswerk", "config.local.toml"), noSandbox+"withhold_secrets = false\n"+
		"[sandbox.secrets]\nDB_PASSWORD = { dotenv = \"DB_PASSWORD\" }\n"+gatewayConfig(srv.URL))
	writeFile(t, filepath.Join(p.work, ".fliesswerk", ".env"), "DB_PASSWORD=db-secret-pw\n")
	logPath := filepath.Join(t.TempDir(), "harness.jsonl")
	p.extraEnv = append(configHome(t), "FWK_FAKE_HARNESS_LOG="+logPath, "FWK_FAKE_LOG_ENV=DB_PASSWORD")
	authSet(t, bin, p.extraEnv, "gw", "gw-secret-token")

	code := p.run(t)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	calls := harnessCalls(t, logPath)
	if len(calls) != 2 {
		t.Fatalf("the Harness ran %d times, want twice: implement and review", len(calls))
	}
	for _, c := range calls {
		env := c.Env
		if env["ANTHROPIC_AUTH_TOKEN"] != "gw-secret-token" || env["ANTHROPIC_BASE_URL"] != srv.URL || env["DB_PASSWORD"] != "db-secret-pw" {
			t.Errorf("the Step's env = %v, want the gateway credential and the project secret", env)
		}
		mustNotScrub(t, env)
		for _, a := range c.Argv {
			if strings.Contains(a, "gw-secret-token") || strings.Contains(a, "db-secret-pw") {
				t.Errorf("a secret is on the command line: %q", c.Argv)
			}
		}
	}

	out := p.stdout + p.stderr
	if strings.Contains(out, "gw-secret-token") || strings.Contains(out, "db-secret-pw") || !strings.Contains(out, "token *** db ***") {
		t.Errorf("the terminal shows a secret, or not the redacted tool call:\n%s", out)
	}
	comments := p.issues(t)[2].Comments
	last := comments[len(comments)-1].Body
	if strings.Contains(last, "gw-secret-token") || strings.Contains(last, "db-secret-pw") || !strings.Contains(last, "Used *** and ***.") {
		t.Errorf("the close comment isn't redacted:\n%s", last)
	}
}

func TestWithSecretsWithheldTheAgentGetsNoProjectSecretOrTrackerToken(t *testing.T) {
	bin := buildTestFliesswerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": [`+approves+`, {"match": "Ticket #2", "actions": [
		{"run": ["git", "commit", "-q", "--allow-empty", "-m", "work"]}
	], "report": {"outcome": "done", "summary": "ok"}}]}`)
	// Both secrets come from the Factory's own env, which the Step would
	// otherwise inherit.
	writeFile(t, filepath.Join(p.work, ".fliesswerk", "config.local.toml"), noSandbox+
		"[sandbox.secrets]\nDB_PASSWORD = { env = \"DB_PASSWORD\" }\n[tracker]\ntoken = { env = \"FWK_GH_PAT\" }\n")
	logPath := filepath.Join(t.TempDir(), "harness.jsonl")
	p.extraEnv = append(configHome(t), "FWK_FAKE_HARNESS_LOG="+logPath, "FWK_FAKE_LOG_ENV=DB_PASSWORD,FWK_GH_PAT,GH_TOKEN",
		"DB_PASSWORD=db-secret-pw", "FWK_GH_PAT=gh-secret-pat", "GH_TOKEN=gh-secret-env")

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	calls := harnessCalls(t, logPath)
	if len(calls) != 2 {
		t.Fatalf("the Harness ran %d times, want twice: implement and review", len(calls))
	}
	for _, c := range calls {
		for _, name := range []string{"DB_PASSWORD", "FWK_GH_PAT", "GH_TOKEN"} {
			if _, ok := c.Env[name]; ok {
				t.Errorf("the agent got %s with withhold_secrets on", name)
			}
		}
		mustNotScrub(t, c.Env)
	}
}

// rejectedRun is a Run whose credential is the subscription token in
// CLAUDE_CODE_OAUTH_TOKEN, and that Claude Code rejects at every Step script matches.
func rejectedRun(t *testing.T, issues []fake.Issue, script string) (*runProject, string) {
	t.Helper()
	bin := buildTestFliesswerk(t)
	p := newRunProject(t, bin, issues, script)
	writeFile(t, filepath.Join(p.work, ".fliesswerk", "config.local.toml"), noSandbox+
		"[harnesses.claude_code]\nmethod = \"oauth_token\"\nsecret = { env = \"CLAUDE_CODE_OAUTH_TOKEN\" }\n")
	logPath := filepath.Join(t.TempDir(), "harness.jsonl")
	p.extraEnv = append(configHome(t), "FWK_FAKE_HARNESS_LOG="+logPath, "CLAUDE_CODE_OAUTH_TOKEN=an-expired-token")
	return p, logPath
}

// stoppedForTheCredential checks that the Run stopped once, naming the fix,
// and held nothing against a Ticket. Callers count the Steps, to see none
// was retried.
func (p *runProject) stoppedForTheCredential(t *testing.T, code int) {
	t.Helper()
	if code != exitStopped {
		t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, exitStopped, p.stdout, p.stderr)
	}
	mustContain(t, "stderr", p.stderr, "reported an auth error", "claude setup-token", "set the environment variable CLAUDE_CODE_OAUTH_TOKEN")
	if n := strings.Count(p.stderr, "fliesswerk run: stopped:"); n != 1 {
		t.Errorf("the Run says it stopped %d times, want once:\n%s", n, p.stderr)
	}
	for n, i := range p.issues(t) {
		if slices.Contains(i.Labels, "needs-investigation") {
			t.Errorf("#%d got needs-investigation: the Ticket isn't at fault", n)
		}
	}
	p.untouched(t)
}

func TestAStepWhoseCredentialClaudeCodeRejectsStopsTheRunWithTheFix(t *testing.T) {
	p, logPath := rejectedRun(t, cartIssues(), `{"sessions": [{"match": "Ticket #2", "error": "auth"}]}`)

	code := p.run(t)

	p.stoppedForTheCredential(t, code)
	if n := len(harnessCalls(t, logPath)); n != 1 {
		t.Errorf("the Harness ran %d times, want once: no retry", n)
	}
}

func TestParallelStepsWhoseCredentialClaudeCodeRejectsStopTheRunOnce(t *testing.T) {
	dir := t.TempDir()
	two, three := filepath.Join(dir, "2"), filepath.Join(dir, "3")
	rejected := func(match, mine, theirs string) string {
		return fmt.Sprintf(`{"match": %q, "actions": [{"touch": %q}, {"await": %q}], "error": "auth"}`, match, mine, theirs)
	}
	p, logPath := rejectedRun(t, twoTicketsOfASpec(), `{"sessions": [`+
		rejected("Implement Ticket #2", two, three)+`, `+rejected("Implement Ticket #3", three, two)+`]}`)

	code := p.run(t, "--max-parallel", "2")

	p.stoppedForTheCredential(t, code)
	if n := len(harnessCalls(t, logPath)); n != 2 {
		t.Errorf("the Harness ran %d times, want once per Sandbox: no retry", n)
	}
}

func TestARunWithAMissingCredentialStopsBeforeAnyStep(t *testing.T) {
	bin := buildTestFliesswerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": [{"report": {"outcome": "done", "summary": "ok"}}]}`)
	writeFile(t, filepath.Join(p.work, ".fliesswerk", "config.local.toml"), noSandbox+gatewayConfig(fakeGateway(t).URL))
	logPath := filepath.Join(t.TempDir(), "harness.jsonl")
	p.extraEnv = append(configHome(t), "FWK_FAKE_HARNESS_LOG="+logPath)

	code := p.run(t)

	if code != exitConfigError || !strings.Contains(p.stderr, "fliesswerk auth set gw") {
		t.Fatalf("exit code = %d, want %d naming the fix\nstdout: %s\nstderr: %s", code, exitConfigError, p.stdout, p.stderr)
	}
	if calls := harnessCalls(t, logPath); len(calls) != 0 {
		t.Errorf("the Harness ran %d times, want never", len(calls))
	}
	p.untouched(t)
}

func TestARunRefusesADotenvThatIsNotGitIgnored(t *testing.T) {
	bin := buildTestFliesswerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": []}`)
	writeFile(t, filepath.Join(p.work, ".fliesswerk", ".gitignore"), "config.local.toml\nstate/\n")
	p.extraEnv = configHome(t)

	code := p.run(t)

	if code != 3 || !strings.Contains(p.stderr, ".fliesswerk/.env is not git-ignored") {
		t.Fatalf("exit code = %d, want 3 refusing the .env\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
}
