package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const apiKeySecret = "sk-ant-secret-value" //nolint:gosec // a made-up key

func (p *runProject) dotenvPath() string {
	return filepath.Join(p.work, ".fliesswerk", ".env")
}

// noSecretIn fails if any output of the last init holds one of secrets.
func (p *runProject) noSecretIn(t *testing.T, secrets ...string) {
	t.Helper()
	for what, text := range map[string]string{"stdout": p.stdout, "stderr": p.stderr} {
		for _, s := range secrets {
			if strings.Contains(text, s) {
				t.Errorf("%s holds a secret:\n%s", what, text)
			}
		}
	}
}

func TestInitWritesAnAPIKeyKeptInDotenv(t *testing.T) {
	p := teamProject(t, map[string]string{"config.toml": teamConfig})

	p.init(t, p.initEnv(t), mine("api_key", "dotenv", "ANTHROPIC_API_KEY", apiKeySecret, "", gitHubSecret)...)

	mustContain(t, "stdout", p.stdout, "Paste the API key, Enter to put it in yourself later (input is hidden)")
	if got := p.fliesswerkFile(t, ".env"); got != "ANTHROPIC_API_KEY="+apiKeySecret+"\n" {
		t.Errorf(".env = %q", got)
	}
	if got := valueAt(p.localConfig(t), "harnesses", "claude_code", "secret", "dotenv"); got != "ANTHROPIC_API_KEY" {
		t.Errorf("secret.dotenv = %v", got)
	}
	mustContain(t, "stdout", p.stdout, "credential (api_key) found")
	p.noSecretIn(t, apiKeySecret, gitHubSecret)
}

const subscriptionSecret = "sk-ant-oat-secret-value" //nolint:gosec // a made-up token

// initWaiting runs `fliesswerk init`, typing answers, then waits for it to
// say "Press Enter", calls meanwhile and only then presses Enter.
func (p *runProject) initWaiting(t *testing.T, env []string, meanwhile func(), answers ...string) int {
	t.Helper()
	cmd := exec.Command(p.bin, "init") //nolint:gosec // the binary under test
	cmd.Dir, cmd.Env = p.work, env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut syncBuffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(stdin, strings.Join(answers, "\n")+"\n")
	for deadline := time.Now().Add(60 * time.Second); !strings.Contains(out.String(), "Press Enter"); {
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			t.Fatalf("init never waited:\n%s\n%s", out.String(), errOut.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	meanwhile()
	_, _ = io.WriteString(stdin, "\n")
	_ = stdin.Close()
	err = cmd.Wait()
	p.stdout, p.stderr = out.String(), errOut.String()
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	if err != nil {
		t.Fatalf("run init: %v", err)
	}
	return 0
}

// subscriptionInDotenv are the personal answers of someone who keeps a
// subscription token in .fliesswerk/.env under the suggested name.
var subscriptionInDotenv = []string{"oauth_token", "dotenv", "", "", gitHubSecret}

func TestInitWaitsForASubscriptionTokenPastedIntoDotenv(t *testing.T) {
	p := teamProject(t, map[string]string{"config.toml": teamConfig})
	var whileWaiting string

	p.initWaiting(t, p.initEnv(t), func() {
		whileWaiting = p.fliesswerkFile(t, ".env")
		writeFile(t, p.dotenvPath(), "CLAUDE_CODE_OAUTH_TOKEN="+subscriptionSecret+"\n")
	}, mine(subscriptionInDotenv...)...)

	mustContain(t, "stdout", p.stdout, "Under which name is it in .fliesswerk/.env? [CLAUDE_CODE_OAUTH_TOKEN]", p.dotenvPath(), "claude setup-token")
	signIn, _, _ := strings.Cut(p.stdout, "personal-access-tokens/new") // the Tracker's questions follow
	if strings.Contains(signIn, "Paste the token") {
		t.Errorf("asked for the subscription token:\n%s", p.stdout)
	}
	if whileWaiting != "CLAUDE_CODE_OAUTH_TOKEN=\n" {
		t.Errorf(".env while waiting = %q", whileWaiting)
	}
	mustContain(t, "stdout", p.stdout, "credential (oauth_token) found")
	p.noSecretIn(t, subscriptionSecret, gitHubSecret)
}

func TestInitFinishesWhenASubscriptionTokenIsLeftOut(t *testing.T) {
	p := teamProject(t, map[string]string{"config.toml": teamConfig})

	code := p.initWaiting(t, p.initEnv(t), func() {}, mine(subscriptionInDotenv...)...)

	if code == exitOK {
		t.Fatalf("init passed with no token:\n%s", p.stdout)
	}
	mustContain(t, "stderr", p.stderr, ".fliesswerk/.env has no CLAUDE_CODE_OAUTH_TOKEN")
	if !p.hasFliesswerkFile("config.local.toml") {
		t.Error("init didn't finish")
	}
}

func TestInitAppendsToAnExistingDotenvAndKeepsEveryLine(t *testing.T) {
	p := teamProject(t, map[string]string{"config.toml": teamConfig})
	existing := "# mine\nDB_PASSWORD='hunter 2'\nexport OTHER=x" // no newline at the end
	writeFile(t, p.dotenvPath(), existing)

	p.init(t, p.initEnv(t), mine("api_key", "dotenv", "ANTHROPIC_API_KEY", apiKeySecret, "", gitHubSecret)...)

	if got := p.fliesswerkFile(t, ".env"); got != existing+"\nANTHROPIC_API_KEY="+apiKeySecret+"\n" {
		t.Errorf(".env = %q", got)
	}
}

func TestInitUsesAKeyThatIsInDotenvAlready(t *testing.T) {
	for name, tt := range map[string]struct{ method, key string }{
		"subscription": {"oauth_token", "CLAUDE_CODE_OAUTH_TOKEN"},
		"API key":      {"api_key", "ANTHROPIC_API_KEY"},
	} {
		t.Run(name, func(t *testing.T) {
			p := teamProject(t, map[string]string{"config.toml": teamConfig})
			existing := "A=1\n" + tt.key + "=already-there\n"
			writeFile(t, p.dotenvPath(), existing)

			p.init(t, p.initEnv(t), mine(tt.method, "dotenv", tt.key, "", gitHubSecret)...)

			signIn, _, _ := strings.Cut(p.stdout, "personal-access-tokens/new")
			mustContain(t, "stdout", signIn, "init uses the value there")
			if strings.Contains(signIn, "Paste the") || strings.Contains(p.stdout, "Press Enter") {
				t.Errorf("asked or waited for a key that is there:\n%s", p.stdout)
			}
			if got := p.fliesswerkFile(t, ".env"); got != existing {
				t.Errorf(".env = %q", got)
			}
			mustContain(t, "stdout", p.stdout, "credential ("+tt.method+") found")
			p.noSecretIn(t, "already-there")
		})
	}
}

func TestInitMakesANewDotenvReadableOnlyByTheUser(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has an ACL, not a mode")
	}
	p := teamProject(t, map[string]string{"config.toml": teamConfig})

	p.init(t, p.initEnv(t), mine("api_key", "dotenv", "ANTHROPIC_API_KEY", apiKeySecret, "", gitHubSecret)...)

	info, err := os.Stat(p.dotenvPath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf(".env mode = %v", info.Mode().Perm())
	}
}

func TestInitWritesNoSecretIntoADotenvGitDoesNotIgnore(t *testing.T) {
	p := teamProject(t, map[string]string{"config.toml": teamConfig, ".gitignore": "config.local.toml\n"})

	p.init(t, p.initEnv(t), mine("api_key", "dotenv", "ANTHROPIC_API_KEY", apiKeySecret, "", gitHubSecret)...)

	if p.hasFliesswerkFile(".env") {
		t.Errorf("wrote a .env git doesn't ignore: %q", p.fliesswerkFile(t, ".env"))
	}
	mustContain(t, "stdout", p.stdout, "Wrote nothing to .fliesswerk/.env", "not git-ignored")
	p.noSecretIn(t, apiKeySecret)
}

func TestInitFinishesWhenAnAPIKeyIsLeftOut(t *testing.T) {
	p := teamProject(t, map[string]string{"config.toml": teamConfig})

	code := p.init(t, p.initEnv(t), mine("api_key", "dotenv", "ANTHROPIC_API_KEY", "", "", gitHubSecret)...)

	if code == exitOK {
		t.Fatalf("init passed with no key:\n%s", p.stdout)
	}
	if !p.hasFliesswerkFile("config.local.toml") {
		t.Error("init didn't finish")
	}
	mustContain(t, "stderr", p.stderr, ".fliesswerk/.env", "ANTHROPIC_API_KEY")
}

func TestInitWaitsForAGitHubTokenPutIntoDotenv(t *testing.T) {
	p := teamProject(t, map[string]string{"config.toml": teamConfig})
	srv := fakeGateway(t)
	var whileWaiting string

	p.initWaiting(t, p.initEnv(t), func() {
		whileWaiting = p.fliesswerkFile(t, ".env")
		writeFile(t, p.dotenvPath(), "GH_TOKEN="+gitHubSecret+"\n")
	}, mine("gateway", srv.URL, "", gatewaySecret, "dotenv", "")...) // the suggested name

	_, tracker, _ := strings.Cut(p.stdout, "personal-access-tokens/new")
	mustContain(t, "stdout", tracker, "Under which name is it in .fliesswerk/.env? [GH_TOKEN]", p.dotenvPath())
	if strings.Contains(tracker, "Paste the token") {
		t.Errorf("asked for the GitHub token:\n%s", p.stdout)
	}
	if whileWaiting != "GH_TOKEN=\n" {
		t.Errorf(".env while waiting = %q", whileWaiting)
	}
	if got := valueAt(p.localConfig(t), "tracker", "token", "dotenv"); got != "GH_TOKEN" {
		t.Errorf("tracker.token = %v", got)
	}
	p.noSecretIn(t, gatewaySecret, gitHubSecret)
}
