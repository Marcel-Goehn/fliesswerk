package main

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// configHome is an environment whose user config directory (where `auth
// set` keeps the credentials file) is a fresh temp dir, on every OS.
func configHome(t *testing.T) []string {
	t.Helper()
	home := t.TempDir()
	return []string{"HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"), "AppData=" + filepath.Join(home, "AppData")}
}

// authSet runs `kraftwerk auth set key` with value piped to stdin.
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
	bin := buildKraftwerk(t, "")
	env := configHome(t)

	stdout, stderr, code := authSet(t, bin, env, "claude", "sk-ant-oat01-secret")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr: %s", code, stderr)
	}
	if strings.Contains(stdout+stderr, "sk-ant-oat01-secret") {
		t.Errorf("the secret was echoed:\n%s%s", stdout, stderr)
	}
	if !strings.Contains(stdout, `secret = { stored = "claude" }`) {
		t.Errorf("stdout = %q, want it to say how to use the key", stdout)
	}
	home := strings.TrimPrefix(env[0], "HOME=")
	path := findCredentialsFile(t, home)
	data, err := os.ReadFile(path) //nolint:gosec // the test's own temp file
	if err != nil || !strings.Contains(string(data), "sk-ant-oat01-secret") {
		t.Errorf("credentials file = %q, %v; want it to hold the secret", data, err)
	}
	if fi, err := os.Stat(path); runtime.GOOS != "windows" && (err != nil || fi.Mode().Perm() != 0o600) {
		t.Errorf("credentials file mode = %v, %v; want 0600", fi.Mode().Perm(), err)
	}
}

func TestAuthSetRefusesBadInput(t *testing.T) {
	bin := buildKraftwerk(t, "")
	env := configHome(t)

	for _, tt := range []struct {
		name, key, value, want string
	}{
		{"an empty secret", "claude", "", "empty"},
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

func TestCheckRefusesADotenvThatIsNotGitIgnored(t *testing.T) {
	bin := buildKraftwerk(t, "")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n", ".gitignore": "config.local.toml\n"})

	_, stderr, code := runIn(t, bin, root, "check")

	if code != 3 || !strings.Contains(stderr, ".kraftwerk/.env is not git-ignored") {
		t.Fatalf("exit code = %d, want 3 refusing the .env; stderr: %s", code, stderr)
	}
}

// fakeGateway serves /v1/models to the bearer token want only, and counts
// the calls.
func fakeGateway(t *testing.T, want string) (*httptest.Server, *int) {
	t.Helper()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls++
		if req.URL.Path != "/v1/models" || req.Header.Get("Authorization") != "Bearer "+want {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data": []}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func gatewayConfig(base string) string {
	return "[harnesses.claude_code]\nmethod = \"gateway\"\nbase_url = \"" + base + "\"\nsecret = { stored = \"gw\" }\n"
}

func TestCheckTestsTheHarnessCredential(t *testing.T) {
	bin := buildKraftwerk(t, "")
	srv, calls := fakeGateway(t, "good-token")

	t.Run("a working credential passes", func(t *testing.T) {
		env := configHome(t)
		authSet(t, bin, env, "gw", "good-token")
		root := projectDir(t, map[string]string{"config.toml": "version = 1\n", "config.local.toml": gatewayConfig(srv.URL)})

		stdout, stderr, code := runEnv(t, bin, root, append(ghEnv(t, "ok"), env...), "check")

		if code != 0 || !strings.Contains(stdout, "claude_code credential (gateway) ok") {
			t.Fatalf("exit code = %d, stdout = %q, want 0 with the credential ok; stderr: %s", code, stdout, stderr)
		}
		if *calls == 0 {
			t.Error("the test call never reached the gateway")
		}
	})

	t.Run("a refused credential fails with the fix", func(t *testing.T) {
		env := configHome(t)
		authSet(t, bin, env, "gw", "bad-token")
		root := projectDir(t, map[string]string{"config.toml": "version = 1\n", "config.local.toml": gatewayConfig(srv.URL)})

		_, stderr, code := runEnv(t, bin, root, append(ghEnv(t, "ok"), env...), "check")

		if code != 3 || !strings.Contains(stderr, "refused the credential (HTTP 401)") || !strings.Contains(stderr, "kraftwerk auth set gw") {
			t.Fatalf("exit code = %d, want 3 naming the refusal and the fix; stderr: %s", code, stderr)
		}
		if strings.Contains(stderr, "bad-token") {
			t.Errorf("stderr holds the secret: %s", stderr)
		}
	})

	t.Run("a missing credential names how to store it", func(t *testing.T) {
		root := projectDir(t, map[string]string{"config.toml": "version = 1\n", "config.local.toml": gatewayConfig(srv.URL)})
		_, stderr, code := runEnv(t, bin, root, append(ghEnv(t, "ok"), configHome(t)...), "check")
		if code != 3 || !strings.Contains(stderr, "kraftwerk auth set gw") {
			t.Fatalf("exit code = %d, want 3 naming `kraftwerk auth set gw`; stderr: %s", code, stderr)
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
	bin := buildTestKraftwerk(t)
	srv, _ := fakeGateway(t, "gw-secret-token")
	// The agent leaks both secrets: into a tool call the terminal shows,
	// and into its report, which becomes the close comment.
	p := newRunProject(t, bin, cartIssues(), `{"sessions": [`+approves+`, {"match": "Ticket #2", "expand": true, "actions": [
		{"run": ["git", "commit", "-q", "--allow-empty", "-m", "token $ANTHROPIC_AUTH_TOKEN db $DB_PASSWORD"]}
	], "report": {"outcome": "done", "summary": "Used $ANTHROPIC_AUTH_TOKEN and $DB_PASSWORD."}}]}`)
	writeFile(t, filepath.Join(p.work, ".kraftwerk", "config.local.toml"), noSandbox+"scrub_credentials = false\n"+
		"[sandbox.secrets]\nDB_PASSWORD = { dotenv = \"DB_PASSWORD\" }\n"+gatewayConfig(srv.URL))
	writeFile(t, filepath.Join(p.work, ".kraftwerk", ".env"), "DB_PASSWORD=db-secret-pw\n")
	logPath := filepath.Join(t.TempDir(), "harness.jsonl")
	p.extraEnv = append(configHome(t), "KWK_FAKE_HARNESS_LOG="+logPath, "KWK_FAKE_LOG_ENV=DB_PASSWORD")
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

func TestWithScrubbingOnTheAgentGetsNoProjectSecretOrTrackerToken(t *testing.T) {
	bin := buildTestKraftwerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": [`+approves+`, {"match": "Ticket #2", "actions": [
		{"run": ["git", "commit", "-q", "--allow-empty", "-m", "work"]}
	], "report": {"outcome": "done", "summary": "ok"}}]}`)
	// Both secrets come from the Factory's own env, which the Step would
	// otherwise inherit.
	writeFile(t, filepath.Join(p.work, ".kraftwerk", "config.local.toml"), noSandbox+
		"[sandbox.secrets]\nDB_PASSWORD = { env = \"DB_PASSWORD\" }\n[tracker]\ntoken = { env = \"KWK_GH_PAT\" }\n")
	logPath := filepath.Join(t.TempDir(), "harness.jsonl")
	p.extraEnv = append(configHome(t), "KWK_FAKE_HARNESS_LOG="+logPath, "KWK_FAKE_LOG_ENV=DB_PASSWORD,KWK_GH_PAT,GH_TOKEN",
		"DB_PASSWORD=db-secret-pw", "KWK_GH_PAT=gh-secret-pat", "GH_TOKEN=gh-secret-env")

	if code := p.run(t); code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	calls := harnessCalls(t, logPath)
	if len(calls) != 2 {
		t.Fatalf("the Harness ran %d times, want twice: implement and review", len(calls))
	}
	for _, c := range calls {
		for _, name := range []string{"DB_PASSWORD", "KWK_GH_PAT", "GH_TOKEN"} {
			if _, ok := c.Env[name]; ok {
				t.Errorf("the agent got %s with scrubbing on", name)
			}
		}
	}
}

func TestARunWithARefusedCredentialStopsBeforeAnyStep(t *testing.T) {
	bin := buildTestKraftwerk(t)
	srv, _ := fakeGateway(t, "good-token")
	p := newRunProject(t, bin, cartIssues(), `{"sessions": [{"report": {"outcome": "done", "summary": "ok"}}]}`)
	writeFile(t, filepath.Join(p.work, ".kraftwerk", "config.local.toml"), noSandbox+gatewayConfig(srv.URL))
	logPath := filepath.Join(t.TempDir(), "harness.jsonl")
	p.extraEnv = append(configHome(t), "KWK_FAKE_HARNESS_LOG="+logPath)
	authSet(t, bin, p.extraEnv, "gw", "expired-token")

	code := p.run(t)

	if code != 2 || !strings.Contains(p.stderr, "refused the credential") || !strings.Contains(p.stderr, "kraftwerk auth set gw") {
		t.Fatalf("exit code = %d, want 2 naming the refusal and the fix\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	if calls := harnessCalls(t, logPath); len(calls) != 0 {
		t.Errorf("the Harness ran %d times, want never", len(calls))
	}
	if issues := p.issues(t); issues[2].Closed || len(issues[2].Comments) != 0 {
		t.Errorf("#2 = %+v, want it untouched", issues[2])
	}
}

func TestARunRefusesADotenvThatIsNotGitIgnored(t *testing.T) {
	bin := buildTestKraftwerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": []}`)
	writeFile(t, filepath.Join(p.work, ".kraftwerk", ".gitignore"), "config.local.toml\nstate/\n")
	p.extraEnv = configHome(t)

	code := p.run(t)

	if code != 3 || !strings.Contains(p.stderr, ".kraftwerk/.env is not git-ignored") {
		t.Fatalf("exit code = %d, want 3 refusing the .env\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
}

func TestAnAuthErrorDuringARunStopsItWithExit2(t *testing.T) {
	bin := buildTestKraftwerk(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": [{"match": "Ticket #2", "error": "auth"}]}`)
	p.extraEnv = configHome(t)

	code := p.run(t)

	if code != 2 || !strings.Contains(p.stderr, "auth") {
		t.Fatalf("exit code = %d, want 2 for the auth error\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	if issues := p.issues(t); issues[2].Closed || len(issues[2].Comments) != 0 || len(issues[2].Labels) != 1 {
		t.Errorf("#2 = %+v, want it untouched", issues[2])
	}
}
