package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const stsNamespace = `xmlns="https://sts.amazonaws.com/doc/2011-06-15/"`

// fakeSTS answers the STS calls the Factory makes. Every request's Action
// lands in the returned list, once the handler has run.
func fakeSTS(t *testing.T) (url string, actions *[]string) {
	t.Helper()
	actions = &[]string{}
	expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = req.ParseForm()
		action := req.PostForm.Get("Action")
		*actions = append(*actions, action)
		w.Header().Set("Content-Type", "text/xml")
		switch action {
		case "GetCallerIdentity":
			_, _ = w.Write([]byte(`<GetCallerIdentityResponse ` + stsNamespace + `><GetCallerIdentityResult><Arn>arn:aws:iam::1:user/d</Arn><UserId>U</UserId><Account>1</Account></GetCallerIdentityResult></GetCallerIdentityResponse>`))
		case "GetFederationToken":
			_, _ = w.Write([]byte(`<GetFederationTokenResponse ` + stsNamespace + `><GetFederationTokenResult><Credentials><AccessKeyId>ASIAMINTED</AccessKeyId><SecretAccessKey>minted-secret</SecretAccessKey><SessionToken>minted-token</SessionToken><Expiration>` + expires + `</Expiration></Credentials></GetFederationTokenResult></GetFederationTokenResponse>`))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, actions
}

const bedrockKeys = "[harnesses.claude_code]\nmethod = \"bedrock\"\nregion = \"eu-central-1\"\n" +
	"access_key_id = { env = \"KWK_TEST_KEY_ID\" }\nsecret_access_key = { env = \"KWK_TEST_KEY_SECRET\" }\n"

// awsEnv points the AWS SDK at the fake STS and away from the machine's own
// AWS config.
func awsEnv(t *testing.T, stsURL string) []string {
	t.Helper()
	none := filepath.Join(t.TempDir(), "none")
	return []string{
		"AWS_ENDPOINT_URL_STS=" + stsURL, "AWS_CONFIG_FILE=" + none, "AWS_SHARED_CREDENTIALS_FILE=" + none,
		"KWK_TEST_KEY_ID=AKIALONGLIVED", "KWK_TEST_KEY_SECRET=long-lived-secret",
	}
}

func TestCheckTestsABedrockCredentialWithoutMintingAnything(t *testing.T) {
	bin := buildKraftwerk(t, "")
	stsURL, actions := fakeSTS(t)
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n", "config.local.toml": bedrockKeys})

	stdout, stderr, code := runEnv(t, bin, root, append(append(ghEnv(t, "ok"), configHome(t)...), awsEnv(t, stsURL)...), "check")

	if !strings.Contains(stdout, "claude_code credential (bedrock) ok") {
		t.Fatalf("exit code = %d, stdout = %q, want the credential ok; stderr: %s", code, stdout, stderr)
	}
	if len(*actions) != 1 || (*actions)[0] != "GetCallerIdentity" {
		t.Errorf("STS calls = %q, want only GetCallerIdentity", *actions)
	}
}

func TestARunHandsBedrockCredentialsOverInAReadableFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the scripted agent copies the file with /bin/cp")
	}
	bin := buildTestKraftwerk(t)
	stsURL, actions := fakeSTS(t)
	copied := filepath.Join(t.TempDir(), "seen.json")
	p := newRunProject(t, bin, cartIssues(), `{"sessions": [`+approves+`, {"match": "Ticket #2", "expand": true, "actions": [
		{"run": ["/bin/cp", "$KRAFTWERK_CREDENTIAL_FILE", "`+copied+`"]},
		{"run": ["git", "commit", "-q", "--allow-empty", "-m", "done"]}
	], "report": {"outcome": "done", "summary": "Done."}}]}`)
	writeFile(t, filepath.Join(p.work, ".kraftwerk", "config.local.toml"), noSandbox+bedrockKeys)
	logPath := filepath.Join(t.TempDir(), "harness.jsonl")
	p.extraEnv = append(append(configHome(t), awsEnv(t, stsURL)...), "KWK_FAKE_HARNESS_LOG="+logPath,
		"KWK_FAKE_LOG_ENV=KRAFTWERK_CREDENTIAL_FILE,AWS_ACCESS_KEY_ID,AWS_SECRET_ACCESS_KEY,AWS_SESSION_TOKEN,AWS_REGION,KWK_TEST_KEY_SECRET")

	code := p.run(t)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	if got := strings.Join(*actions, " "); got != "GetCallerIdentity GetFederationToken" {
		t.Errorf("STS calls = %q, want the test call, then one mint", got)
	}
	seen, err := os.ReadFile(copied) //nolint:gosec // a path of the test's own
	if err != nil {
		t.Fatalf("the agent couldn't read the credential file: %v", err)
	}
	for _, want := range []string{`"AccessKeyId":"ASIAMINTED"`, `"SecretAccessKey":"minted-secret"`, `"SessionToken":"minted-token"`, `"Expiration"`} {
		if !strings.Contains(string(seen), want) {
			t.Errorf("the file = %s, want %s", seen, want)
		}
	}
	for _, c := range harnessCalls(t, logPath) {
		for _, name := range []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "KWK_TEST_KEY_SECRET"} {
			if v := c.Env[name]; v != "" {
				t.Errorf("the Step's env has %s = %q, want no AWS credential in it", name, v)
			}
		}
		if c.Env["CLAUDE_CODE_USE_BEDROCK"] != "1" || c.Env["AWS_REGION"] != "eu-central-1" {
			t.Errorf("the Step's env = %v, want Bedrock switched on in the region", c.Env)
		}
		argv := strings.Join(c.Argv, " ")
		if !strings.Contains(argv, "awsCredentialExport") {
			t.Errorf("argv = %q, want awsCredentialExport in --settings", argv)
		}
		if strings.Contains(argv, "minted-secret") || strings.Contains(argv, "long-lived-secret") {
			t.Errorf("a secret is on the command line: %q", argv)
		}
	}
	if _, err := os.Stat(filepath.Join(p.work, ".kraftwerk", "state", "credentials")); !os.IsNotExist(err) {
		t.Errorf("stat the credential directory after the Run: %v, want it gone", err)
	}
}

func TestPassingLongLivedKeysThroughWarnsAndUsesTheStepEnv(t *testing.T) {
	bin := buildTestKraftwerk(t)
	stsURL, actions := fakeSTS(t)
	p := newRunProject(t, bin, cartIssues(), `{"sessions": [`+approves+`, {"match": "Ticket #2", "actions": [
		{"run": ["git", "commit", "-q", "--allow-empty", "-m", "done"]}
	], "report": {"outcome": "done", "summary": "Done."}}]}`)
	writeFile(t, filepath.Join(p.work, ".kraftwerk", "config.local.toml"), noSandbox+bedrockKeys+"i_accept_long_lived_keys_in_sandbox = true\n")
	logPath := filepath.Join(t.TempDir(), "harness.jsonl")
	p.extraEnv = append(append(configHome(t), awsEnv(t, stsURL)...), "KWK_FAKE_HARNESS_LOG="+logPath,
		"KWK_FAKE_LOG_ENV=AWS_ACCESS_KEY_ID,AWS_SECRET_ACCESS_KEY")

	code := p.run(t)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
	if !strings.Contains(p.stdout+p.stderr, "warning: i_accept_long_lived_keys_in_sandbox is set") {
		t.Errorf("no warning about the keys:\n%s%s", p.stdout, p.stderr)
	}
	if got := strings.Join(*actions, " "); got != "GetCallerIdentity" {
		t.Errorf("STS calls = %q, want only the test call", got)
	}
	for _, c := range harnessCalls(t, logPath) {
		if c.Env["AWS_ACCESS_KEY_ID"] != "AKIALONGLIVED" || c.Env["AWS_SECRET_ACCESS_KEY"] != "long-lived-secret" {
			t.Errorf("the Step's env = %v, want the keys", c.Env)
		}
		if strings.Contains(strings.Join(c.Argv, " "), "awsCredentialExport") {
			t.Errorf("argv = %q, want no awsCredentialExport: Claude Code refuses a file with no session token", c.Argv)
		}
	}
}
