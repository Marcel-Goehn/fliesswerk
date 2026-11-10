package main

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

const teamConfig = "version = 1\n\n[tracker]\nkind = \"github\"\nrepo = \"acme/shop\"\n"

// teamProject is a repo somebody already set up and committed, as a teammate
// finds it after cloning. Each of files goes below .fliesswerk/.
func teamProject(t *testing.T, files map[string]string) *runProject {
	t.Helper()
	all := nodePostgres()
	for name, content := range files {
		all[".fliesswerk/"+name] = content
	}
	return initProject(t, all)
}

func (p *runProject) hasFliesswerkFile(name string) bool {
	_, err := os.Stat(filepath.Join(p.work, ".fliesswerk", filepath.FromSlash(name)))
	return err == nil
}

func TestInitRerunCreatesOnlyWhatIsMissing(t *testing.T) {
	p := teamProject(t, map[string]string{"config.toml": teamConfig, "config.local.toml": "# mine\n"})

	p.init(t, p.initEnv(t))

	if got := p.fliesswerkFile(t, "config.toml"); got != teamConfig {
		t.Errorf("config.toml = %q", got)
	}
	if got := p.fliesswerkFile(t, "config.local.toml"); got != "# mine\n" {
		t.Errorf("config.local.toml = %q", got)
	}
	mustContain(t, ".gitignore", p.fliesswerkFile(t, ".gitignore"), "config.local.toml", ".env")
	mustContain(t, "Containerfile", p.fliesswerkFile(t, "Containerfile"), "node:22.11.0-bookworm-slim", "USER agent")
	mustContain(t, "stdout", p.stdout, "+ .fliesswerk/.gitignore", "+ .fliesswerk/Containerfile")
	if p.hasFliesswerkFile("prompts/implement.md") {
		t.Error("a deleted prompt came back")
	}
}

func TestInitRerunWithNothingMissingSaysAlreadySetUpAndChecks(t *testing.T) {
	p := teamProject(t, map[string]string{
		"config.toml": teamConfig, "config.local.toml": "# mine\n",
		".gitignore": "mine\n.env\nconfig.local.toml\n", "Containerfile": "FROM scratch\n",
	})

	p.init(t, p.initEnv(t))

	mustContain(t, "stdout", p.stdout, "Already set up", "fliesswerk check: the config is valid")
	if got := p.fliesswerkFile(t, ".gitignore"); got != "mine\n.env\nconfig.local.toml\n" {
		t.Errorf(".gitignore = %q", got)
	}
	if got := p.fliesswerkFile(t, "Containerfile"); got != "FROM scratch\n" {
		t.Errorf("Containerfile = %q", got)
	}
}

func TestInitNativeRerunGetsNoContainerfile(t *testing.T) {
	p := teamProject(t, map[string]string{
		"config.toml": "version = 1\n\n[sandbox]\nkind = \"native\"\n", "config.local.toml": "# mine\n",
	})

	p.init(t, p.initEnv(t))

	if p.hasFliesswerkFile("Containerfile") {
		t.Error("a native project got a Containerfile")
	}
}

func TestInitHasNoForce(t *testing.T) {
	p := teamProject(t, map[string]string{"config.toml": teamConfig, "config.local.toml": "# mine\n"})

	code := p.initWith(t, p.initEnv(t), []string{"--force"})

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d", code, exitConfigError)
	}
	mustContain(t, "stderr", p.stderr, "--force", "only ever creates what's missing")
}

// viaGateway are the personal answers of someone with a gateway and no gh:
// sign-in at srv with the default key place, then a GitHub token.
func viaGateway(srv *httptest.Server) []string {
	return []string{"gateway", srv.URL, "", gatewaySecret, "", gitHubSecret}
}

const (
	gatewaySecret = "gw-secret-value" //nolint:gosec // a made-up token
	gitHubSecret  = "pat-secret-value"
)

func (p *runProject) localConfig(t *testing.T) map[string]any {
	t.Helper()
	var values map[string]any
	if err := toml.Unmarshal([]byte(p.fliesswerkFile(t, "config.local.toml")), &values); err != nil {
		t.Fatalf("config.local.toml isn't TOML: %v\n%s", err, p.fliesswerkFile(t, "config.local.toml"))
	}
	return values
}

// stored is the user-level credentials file, by key.
func (p *runProject) stored(t *testing.T) map[string]string {
	t.Helper()
	file := findCredentialsFile(t, p.home)
	if info, err := os.Stat(file); err != nil {
		t.Fatal(err)
	} else if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("credentials file mode = %v", info.Mode().Perm())
	}
	data, err := os.ReadFile(file) //nolint:gosec // the test's own temp file
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	if err := toml.Unmarshal(data, &values); err != nil {
		t.Fatal(err)
	}
	return values
}

func TestInitTeammateIsAskedOnlyPersonalQuestions(t *testing.T) {
	p := teamProject(t, map[string]string{
		"config.toml": teamConfig, ".gitignore": "config.local.toml\n.env\n", "Containerfile": "FROM scratch\n",
	})
	srv := fakeGateway(t)

	p.init(t, p.initEnv(t), viaGateway(srv)...)

	if strings.Contains(p.stdout, "Which branch should") || strings.Contains(p.stdout, "Where do your Tickets live?") {
		t.Errorf("a teammate got a project question:\n%s", p.stdout)
	}
	mustContain(t, "stdout", p.stdout, "How do you sign in to Claude?", "Where is", "personal-access-tokens/new", "acme/shop", "Read and write")
	if got := p.fliesswerkFile(t, "config.toml"); got != teamConfig {
		t.Errorf("config.toml changed: %q", got)
	}
	local := p.localConfig(t)
	if got := valueAt(local, "harnesses", "claude_code", "method"); got != "gateway" {
		t.Errorf("method = %v", got)
	}
	if got := valueAt(local, "harnesses", "claude_code", "base_url"); got != srv.URL {
		t.Errorf("base_url = %v", got)
	}
	if got := valueAt(local, "harnesses", "claude_code", "secret", "stored"); got != "claude_gateway" {
		t.Errorf("secret = %v", got)
	}
	if got := valueAt(local, "tracker", "token", "stored"); got != "github.acme.shop" {
		t.Errorf("tracker.token = %v", got)
	}
	stored := p.stored(t)
	if stored["claude_gateway"] != gatewaySecret || stored["github.acme.shop"] != gitHubSecret {
		t.Errorf("stored = %v", stored)
	}
	for what, text := range map[string]string{"stdout": p.stdout, "stderr": p.stderr, "config.local.toml": p.fliesswerkFile(t, "config.local.toml")} {
		if strings.Contains(text, gatewaySecret) || strings.Contains(text, gitHubSecret) {
			t.Errorf("%s holds a secret:\n%s", what, text)
		}
	}
	mustContain(t, "stdout", p.stdout, "the config is valid", "credential (gateway) found, not tested ahead of time")
}

// behindNothing keeps a test off the network: whatever init would ask of an
// https endpoint fails at once.
func behindNothing(env []string) []string {
	return append(env, "HTTPS_PROXY=http://127.0.0.1:1", "NO_PROXY=", "no_proxy=")
}

func (p *runProject) claudeSettings(t *testing.T, content string) {
	t.Helper()
	writeFile(t, filepath.Join(p.home, ".claude", "settings.json"), content)
}

const bedrockSettings = `{"env": {"CLAUDE_CODE_USE_BEDROCK": "1", "AWS_REGION": "eu-central-1", "AWS_PROFILE": "work"}}`

func TestInitImportsAClaudeCodeBedrockSetup(t *testing.T) {
	p := teamProject(t, map[string]string{"config.toml": teamConfig})
	env := behindNothing(p.initEnv(t))
	p.claudeSettings(t, bedrockSettings)

	p.init(t, env, "", "", gitHubSecret) // take the import, then the GitHub token

	mustContain(t, "stdout", p.stdout, "eu-central-1", "work")
	if strings.Contains(p.stdout, "How do you sign in") || strings.Contains(p.stdout, "Which AWS region") {
		t.Errorf("asked what the import answers:\n%s", p.stdout)
	}
	claude, _ := valueAt(p.localConfig(t), "harnesses", "claude_code").(map[string]any)
	if len(claude) != 3 || claude["method"] != "bedrock" || claude["region"] != "eu-central-1" || claude["profile"] != "work" {
		t.Errorf("[harnesses.claude_code] = %v", claude)
	}
}

func TestInitAsksWhatABedrockImportLacks(t *testing.T) {
	p := teamProject(t, map[string]string{"config.toml": teamConfig})
	env := behindNothing(p.initEnv(t))
	p.claudeSettings(t, `{"env": {"CLAUDE_CODE_USE_BEDROCK": "true", "AWS_REGION": "us-east-1"}}`)

	p.init(t, env, "y", "sso", "", gitHubSecret)

	mustContain(t, "stdout", p.stdout, "Which AWS profile")
	if strings.Contains(p.stdout, "Which AWS region") {
		t.Errorf("asked for the imported region:\n%s", p.stdout)
	}
	local := p.localConfig(t)
	if got := valueAt(local, "harnesses", "claude_code", "region"); got != "us-east-1" {
		t.Errorf("region = %v", got)
	}
	if got := valueAt(local, "harnesses", "claude_code", "profile"); got != "sso" {
		t.Errorf("profile = %v", got)
	}
}

func TestInitDecliningTheBedrockImportAsksAsUsual(t *testing.T) {
	p := teamProject(t, map[string]string{"config.toml": teamConfig})
	env := p.initEnv(t)
	p.claudeSettings(t, bedrockSettings)
	srv := fakeGateway(t)

	p.init(t, env, append([]string{"n"}, viaGateway(srv)...)...)

	if got := valueAt(p.localConfig(t), "harnesses", "claude_code", "method"); got != "gateway" {
		t.Errorf("method = %v", got)
	}
}

func TestInitOffersNoImportWithoutABedrockSetup(t *testing.T) {
	for name, settings := range map[string]string{
		"another provider": `{"env": {"AWS_REGION": "eu-central-1"}}`,
		"switched off":     `{"env": {"CLAUDE_CODE_USE_BEDROCK": "0", "AWS_REGION": "eu-central-1"}}`,
		"not JSON":         "{",
	} {
		t.Run(name, func(t *testing.T) {
			p := teamProject(t, map[string]string{"config.toml": teamConfig})
			env := p.initEnv(t)
			p.claudeSettings(t, settings)
			srv := fakeGateway(t)

			p.init(t, env, viaGateway(srv)...)

			if got := valueAt(p.localConfig(t), "harnesses", "claude_code", "method"); got != "gateway" {
				t.Errorf("method = %v\n%s", got, p.stdout)
			}
		})
	}
}

func TestInitWithAGhLoginOffersItAndWritesNoToken(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a shell script")
	}
	p := teamProject(t, map[string]string{"config.toml": teamConfig})
	srv := fakeGateway(t)

	p.init(t, p.initEnv(t, "gh"), "gateway", srv.URL, "", gatewaySecret, "")

	mustContain(t, "stdout", p.stdout, "`gh` login", "fine-grained")
	if strings.Contains(p.stdout, "personal-access-tokens/new") {
		t.Errorf("walked through a token nobody asked for:\n%s", p.stdout)
	}
	if got := valueAt(p.localConfig(t), "tracker", "token"); got != nil {
		t.Errorf("tracker.token = %v", got)
	}
}

func TestInitWithAGhLoginCanStillTakeAToken(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake gh is a shell script")
	}
	p := teamProject(t, map[string]string{"config.toml": teamConfig})
	srv := fakeGateway(t)

	p.init(t, p.initEnv(t, "gh"), "gateway", srv.URL, "", gatewaySecret, "token", "", gitHubSecret)

	mustContain(t, "stdout", p.stdout, "personal-access-tokens/new")
	if got := valueAt(p.localConfig(t), "tracker", "token", "stored"); got != "github.acme.shop" {
		t.Errorf("tracker.token = %v", got)
	}
}

func TestInitKeepsAKeyWhereThePersonSays(t *testing.T) {
	for _, tt := range []struct{ answer, ref, source string }{
		{"2", "GW_TOKEN", "env"},
		{"command", "pass show gw", "command"},
		{"dotenv", "GW", "dotenv"},
	} {
		t.Run(tt.source, func(t *testing.T) {
			p := teamProject(t, map[string]string{"config.toml": teamConfig})
			srv := fakeGateway(t)

			p.init(t, p.initEnv(t), "gateway", srv.URL, tt.answer, tt.ref, "", gitHubSecret)

			if got := valueAt(p.localConfig(t), "harnesses", "claude_code", "secret", tt.source); got != tt.ref {
				t.Errorf("secret.%s = %v\n%s", tt.source, got, p.fliesswerkFile(t, "config.local.toml"))
			}
			if _, ok := p.stored(t)["claude_gateway"]; ok {
				t.Error("stored a key kept elsewhere")
			}
		})
	}
}

func TestInitKeepsASubscriptionTokenOutOfTheCredentialsFile(t *testing.T) {
	p := teamProject(t, map[string]string{"config.toml": teamConfig})

	p.init(t, p.initEnv(t), "oauth_token", "", "", "", gitHubSecret) // env, then the suggested variable

	signIn, _, _ := strings.Cut(p.stdout, "personal-access-tokens/new") // the Tracker's questions follow
	mustContain(t, "stdout", signIn, "Where is the token kept?", "Which variable holds it? [CLAUDE_CODE_OAUTH_TOKEN]", "Anthropic's terms")
	for _, unwanted := range []string{"credentials file, outside every repo", "Paste the token"} {
		if strings.Contains(signIn, unwanted) {
			t.Errorf("signing in with a subscription says %q:\n%s", unwanted, signIn)
		}
	}
	if got := valueAt(p.localConfig(t), "harnesses", "claude_code", "secret", "env"); got != "CLAUDE_CODE_OAUTH_TOKEN" {
		t.Errorf("secret.env = %v\n%s", got, p.fliesswerkFile(t, "config.local.toml"))
	}
	if _, ok := p.stored(t)["claude"]; ok {
		t.Error("stored the subscription token")
	}
}

func TestInitCanUseAKeyAlreadyStored(t *testing.T) {
	p := teamProject(t, map[string]string{"config.toml": teamConfig})
	env := p.initEnv(t)
	if _, stderr, code := authSet(t, p.bin, env, "claude_gateway", "from-another-project"); code != 0 {
		t.Fatalf("auth set: %s", stderr)
	}
	srv := fakeGateway(t)

	p.init(t, env, "gateway", srv.URL, "", "", "", gitHubSecret) // the "" after the URL: stored, then keep it

	if got := p.stored(t)["claude_gateway"]; got != "from-another-project" {
		t.Errorf("stored = %q", got)
	}
}

func TestInitSavesNothingWhenPersonalAnswersRunOut(t *testing.T) {
	p := teamProject(t, map[string]string{"config.toml": teamConfig})
	srv := fakeGateway(t)

	code := p.init(t, p.initEnv(t), "gateway", srv.URL, "", gatewaySecret)

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d", code, exitConfigError)
	}
	mustContain(t, "stderr", p.stderr, "nothing was written")
	if p.hasFliesswerkFile("config.local.toml") || p.hasFliesswerkFile(".gitignore") {
		t.Error("files were written")
	}
	if err := filepath.WalkDir(p.home, func(path string, d os.DirEntry, err error) error {
		if err == nil && d.Name() == "credentials" {
			t.Errorf("a secret was stored at %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestInitBringsBackTheGitignoreEvenWithABrokenConfig(t *testing.T) {
	p := teamProject(t, map[string]string{"config.toml": "version = [\n", "config.local.toml": "# mine\n"})

	p.init(t, p.initEnv(t))

	mustContain(t, ".gitignore", p.fliesswerkFile(t, ".gitignore"), "config.local.toml", ".env")
}

func TestInitImportsABedrockSetupWithoutARegion(t *testing.T) {
	p := teamProject(t, map[string]string{"config.toml": teamConfig})
	env := behindNothing(p.initEnv(t))
	p.claudeSettings(t, `{"env": {"CLAUDE_CODE_USE_BEDROCK": "1", "AWS_PROFILE": "work"}}`)

	p.init(t, env, "y", "eu-west-1", "", gitHubSecret)

	local := p.localConfig(t)
	if got := valueAt(local, "harnesses", "claude_code", "region"); got != "eu-west-1" {
		t.Errorf("region = %v\n%s", got, p.stdout)
	}
	if got := valueAt(local, "harnesses", "claude_code", "profile"); got != "work" {
		t.Errorf("profile = %v\n%s", got, p.stdout)
	}
}
