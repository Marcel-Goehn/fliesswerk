package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/kraftwerk/internal/config"
)

// project writes .kraftwerk/config.toml (and config.local.toml, if local is
// not empty) into a fresh directory and returns that directory.
func project(t *testing.T, cfg, local string) string {
	t.Helper()
	dir := t.TempDir()
	kw := filepath.Join(dir, ".kraftwerk")
	if err := os.MkdirAll(kw, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kw, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if local != "" {
		if err := os.WriteFile(filepath.Join(kw, "config.local.toml"), []byte(local), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func load(t *testing.T, cfg, local string, overrides ...config.Override) config.Result {
	t.Helper()
	return config.Load(project(t, cfg, local), overrides)
}

func messages(r config.Result, sev config.Severity) []string {
	var out []string
	for _, d := range r.Diagnostics {
		if d.Severity == sev {
			out = append(out, d.String())
		}
	}
	return out
}

// wantOne asserts exactly one diagnostic of the given severity, containing
// every fragment.
func wantOne(t *testing.T, r config.Result, sev config.Severity, fragments ...string) {
	t.Helper()
	got := messages(r, sev)
	if len(got) != 1 {
		t.Fatalf("got %d %s diagnostics, want 1:\n%s", len(got), sev, strings.Join(messages(r, config.Error), "\n")+"\n"+strings.Join(messages(r, config.Warning), "\n"))
	}
	for _, f := range fragments {
		if !strings.Contains(got[0], f) {
			t.Errorf("diagnostic %q does not contain %q", got[0], f)
		}
	}
}

func wantClean(t *testing.T, r config.Result) {
	t.Helper()
	if len(r.Diagnostics) != 0 {
		var all []string
		for _, d := range r.Diagnostics {
			all = append(all, d.String())
		}
		t.Fatalf("want no diagnostics, got:\n%s", strings.Join(all, "\n"))
	}
}

func TestMinimalConfigIsValid(t *testing.T) {
	r := load(t, "version = 1\n", "")
	wantClean(t, r)
	if !r.OK() {
		t.Fatal("OK() = false for a valid config")
	}
}

func TestVersionIsRequired(t *testing.T) {
	r := load(t, "model = \"x\"\n", "")
	wantOne(t, r, config.Error, ".kraftwerk/config.toml", "version = 1")
	if r.OK() {
		t.Fatal("OK() = true despite an error")
	}
}

func TestNewerVersionIsRefused(t *testing.T) {
	// Keys of a newer version are unknown to this binary; only the version
	// is reported.
	r := load(t, "version = 2\nshiny_new_key = true\n", "")
	wantOne(t, r, config.Error, "version 2", "kraftwerk update")
}

func TestMissingConfigNamesInit(t *testing.T) {
	r := config.Load(t.TempDir(), nil)
	wantOne(t, r, config.Error, "config.toml", "kraftwerk init")
}

func TestUnknownKeySuggestsTheClosestKnownOne(t *testing.T) {
	r := load(t, "version = 1\nmax_paralel = 2\n", "")
	wantOne(t, r, config.Error, ".kraftwerk/config.toml", "max_paralel", "unknown key", `did you mean "max_parallel"?`)
}

func TestUnknownKeyInNestedTableSuggests(t *testing.T) {
	r := load(t, "version = 1\n[steps.reveiw]\nenabled = true\n", "")
	wantOne(t, r, config.Error, "steps.reveiw", `did you mean "review"?`)
}

func TestUnknownKeyFarFromAnyKnownOneHasNoSuggestion(t *testing.T) {
	r := load(t, "version = 1\nfrobnicate = 2\n", "")
	wantOne(t, r, config.Error, "frobnicate", "unknown key")
	if strings.Contains(messages(r, config.Error)[0], "did you mean") {
		t.Fatalf("unexpected suggestion: %s", messages(r, config.Error)[0])
	}
}

func TestWrongTypeIsAnError(t *testing.T) {
	r := load(t, "version = 1\nmax_parallel = \"2\"\n", "")
	wantOne(t, r, config.Error, "max_parallel", "must be a whole number", `"2"`)
}

func TestWrongTypeInListIsAnError(t *testing.T) {
	r := load(t, "version = 1\nverify = [\"test\", 3]\n[commands]\ntest = \"go test\"\n", "")
	wantOne(t, r, config.Error, "verify[1]", "must be a string")
}

func TestUnknownEnumValueSuggests(t *testing.T) {
	r := load(t, "version = 1\nharness = \"claude-code\"\nmodel = \"m\"\n", "")
	wantOne(t, r, config.Error, "harness", `did you mean "claude_code"?`)
}

func TestVersionMustBeAWholeNumber(t *testing.T) {
	r := load(t, "version = \"1\"\n", "")
	wantOne(t, r, config.Error, "version", "must be a whole number")
}

func TestFreeFormTablesAcceptAnyName(t *testing.T) {
	r := load(t, `version = 1
verify = ["test", "e2e"]
[commands]
test = "go test ./..."
e2e  = "make e2e"
[services]
compose_file = "compose.yaml"
[services.postgres]
shared = true
env = { DATABASE_URL = "postgres://u:p@{host}:{port}/app" }
[sandbox.env]
RUST_LOG = "debug"
`, "")
	wantClean(t, r)
}

func TestFullSpecShapeIsValid(t *testing.T) {
	r := load(t, fullConfig, `[tracker]
token = { stored = "github" }
[harnesses.claude_code]
secret = { stored = "claude" }
`)
	wantClean(t, r)
}

func TestLayersMergeKeyByKeyAndListsReplace(t *testing.T) {
	r := load(t, `version = 1
model = "team-model"
max_parallel = 2
protected_paths = [".claude/", "infra/"]
[branches]
base_branch = "main"
prefix = "kraftwerk"
`, `model = "local-model"
protected_paths = ["secrets/"]
[branches]
prefix = "mine"
`, config.Override{Key: "max_parallel", Value: int64(4)})
	wantClean(t, r)

	branches := r.Values["branches"].(map[string]any)
	got := map[string]any{
		"model":        r.Values["model"],
		"max_parallel": r.Values["max_parallel"],
		"base_branch":  branches["base_branch"],
		"prefix":       branches["prefix"],
	}
	want := map[string]any{"model": "local-model", "max_parallel": int64(4), "base_branch": "main", "prefix": "mine"}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s = %v, want %v", k, got[k], w)
		}
	}
	paths := r.Values["protected_paths"].([]any)
	if len(paths) != 1 || paths[0] != "secrets/" {
		t.Errorf("protected_paths = %v, want [secrets/] (lists replace)", paths)
	}
}

func TestArraysOfTablesReplace(t *testing.T) {
	r := load(t, `version = 1
[[hooks.run_start]]
run = "a"
[[hooks.run_start]]
run = "b"
`, `[[hooks.run_start]]
run = "c"
`)
	wantClean(t, r)
	hooks := r.Values["hooks"].(map[string]any)["run_start"].([]any)
	if len(hooks) != 1 || hooks[0].(map[string]any)["run"] != "c" {
		t.Fatalf("hooks.run_start = %v, want only the local entry", hooks)
	}
}

func TestErrorsNameTheLayerTheyComeFrom(t *testing.T) {
	r := load(t, "version = 1\n", "max_iterations = 0\n")
	wantOne(t, r, config.Error, ".kraftwerk/config.local.toml", "max_iterations", "at least 1")

	r = load(t, "version = 1\n", "", config.Override{Key: "sandbox.kind", Value: "nope"})
	wantOne(t, r, config.Error, "command line", "sandbox.kind")
}

func TestLocalOnlyKeysAreRejectedInConfigToml(t *testing.T) {
	cases := map[string]struct{ cfg, key string }{
		"no sandbox":     {"[sandbox]\nkind = \"none\"\ni_accept_unsandboxed_risk = true\n", "sandbox.kind"},
		"open network":   {"[sandbox.network]\nallow = [\"registry.npmjs.org\", \"*\"]\n", "sandbox.network.allow"},
		"tracker token":  {"[tracker]\ntoken = { stored = \"github\" }\n", "tracker.token"},
		"harness secret": {"[harnesses.claude_code]\nsecret = { stored = \"claude\" }\n", "harnesses.claude_code.secret"},
		"bedrock key":    {"[harnesses.claude_code]\naccess_key_id = { env = \"AWS_ACCESS_KEY_ID\" }\n", "harnesses.claude_code.access_key_id"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			r := load(t, "version = 1\n"+tc.cfg, "")
			found := false
			for _, d := range r.Diagnostics {
				if d.Severity == config.Error && d.Layer == config.ProjectFile && d.Key == tc.key && strings.Contains(d.Message, "config.local.toml") {
					found = true
				}
			}
			if !found {
				t.Fatalf("want a local-only error for %s in config.toml, got %v", tc.key, messages(r, config.Error))
			}
		})
	}
}

func TestLocalOnlyKeysAreAcceptedLocallyAndAsFlags(t *testing.T) {
	r := load(t, "version = 1\n", `[tracker]
token = { stored = "github" }
[harnesses.claude_code]
secret = { env = "CLAUDE_CODE_OAUTH_TOKEN" }
[sandbox.network]
allow = ["*"]
`)
	if !r.OK() {
		t.Fatalf("want OK, got %v", messages(r, config.Error))
	}
	wantOne(t, r, config.Warning, "allow", "any host")

	r = load(t, "version = 1\n", "",
		config.Override{Key: "sandbox.kind", Value: "none"},
		config.Override{Key: "sandbox.i_accept_unsandboxed_risk", Value: true})
	if !r.OK() {
		t.Fatalf("want OK, got %v", messages(r, config.Error))
	}
	wantOne(t, r, config.Warning, "no Sandbox")
}

func TestNoSandboxNeedsTheAcknowledgement(t *testing.T) {
	r := load(t, "version = 1\n", "[sandbox]\nkind = \"none\"\n")
	wantOne(t, r, config.Error, "sandbox.kind", "i_accept_unsandboxed_risk = true")
}

func TestSecretsAreReferencesNeverValues(t *testing.T) {
	r := load(t, "version = 1\n", "[tracker]\ntoken = \"ghp_abc\"\n")
	wantOne(t, r, config.Error, "tracker.token", "where the secret comes from")
	if strings.Contains(messages(r, config.Error)[0], "ghp_abc") {
		t.Fatalf("the message repeats the secret: %s", messages(r, config.Error)[0])
	}

	r = load(t, "version = 1\n", "[tracker]\ntoken = { stored = \"a\", env = \"B\" }\n")
	wantOne(t, r, config.Error, "tracker.token", "exactly one of")
}

func TestStepWithHarnessNeedsModel(t *testing.T) {
	r := load(t, "version = 1\n[steps.review]\nharness = \"claude_code\"\n", "")
	wantOne(t, r, config.Error, "steps.review", "`model`")

	// The model may come from another layer.
	r = load(t, "version = 1\n[steps.review]\nharness = \"claude_code\"\n", "[steps.review]\nmodel = \"cheap\"\n")
	wantClean(t, r)
}

func TestKeepDaysAcceptsAPositiveNumberOrForever(t *testing.T) {
	for _, ok := range []string{"30", "1", `"forever"`} {
		wantClean(t, load(t, "version = 1\n[logs]\nkeep_days = "+ok+"\n", ""))
	}
	for _, bad := range []string{"0", "-3", "1.5", `"30"`, `"never"`} {
		r := load(t, "version = 1\n[logs]\nkeep_days = "+bad+"\n", "")
		wantOne(t, r, config.Error, "logs.keep_days", "keep_days = 30", `keep_days = "forever"`)
	}
}

func TestSecretLookingSandboxEnvWarns(t *testing.T) {
	cases := map[string]string{ //nolint:gosec // fake secrets, for the detector to find
		"by name":        `API_TOKEN = "abc"`,
		"github token":   `GREETING = "ghp_0123456789abcdef"`,
		"aws key":        `ID = "AKIAABCDEFGHIJKLMNOP"`,
		"url with login": `DB = "postgres://app:hunter2@db:5432/app"`,
	}
	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			r := load(t, "version = 1\n[sandbox.env]\n"+line+"\n", "")
			if !r.OK() {
				t.Fatalf("a warning must not fail validation: %v", messages(r, config.Error))
			}
			wantOne(t, r, config.Warning, "sandbox.env.", "looks like a secret", "[sandbox.secrets]")
			for _, secret := range []string{"ghp_0123", "AKIAABCD", "hunter2"} {
				if strings.Contains(messages(r, config.Warning)[0], secret) {
					t.Fatalf("the warning repeats the value: %s", messages(r, config.Warning)[0])
				}
			}
		})
	}

	wantClean(t, load(t, "version = 1\n[sandbox.env]\nRUST_LOG = \"debug\"\nNODE_ENV = \"test\"\n", ""))
}

func TestVerifyNamesFeedbackCommands(t *testing.T) {
	r := load(t, "version = 1\nverify = [\"tset\"]\n[commands]\ntest = \"go test ./...\"\n", "")
	wantOne(t, r, config.Error, "verify[0]", "[commands]", `did you mean "test"?`)
}

func TestHookEntriesSetRunOrParallel(t *testing.T) {
	r := load(t, "version = 1\n[[hooks.sandbox_created]]\nwhere = \"sandbox\"\n", "")
	wantOne(t, r, config.Error, "hooks.sandbox_created[0]", "exactly one of `run` or `parallel`")

	r = load(t, "version = 1\n[[hooks.run_end]]\nrun = \"x\"\nwhere = \"sandbox\"\n", "")
	wantOne(t, r, config.Error, "hooks.run_end[0].where", `"host"`)

	r = load(t, "version = 1\n[[hooks.run_end]]\nrun = \"x\"\ntimeout = \"5 minutes\"\n", "")
	wantOne(t, r, config.Error, "hooks.run_end[0].timeout", `"5m"`)
}

func TestFreeFormTableNearMissOfKnownKeyIsATypo(t *testing.T) {
	r := load(t, "version = 1\n[services]\ncompose_fiel = \"compose.yaml\"\n", "")
	wantOne(t, r, config.Error, "services.compose_fiel", `did you mean "compose_file"?`)
}

func TestVersionBelowOneIsAnError(t *testing.T) {
	r := load(t, "version = 0\n", "")
	wantOne(t, r, config.Error, "version", "must be 1")
}

// fullConfig is a config.toml that sets every key of the Spec's shape.
const fullConfig = `version = 1
factory = "1"
harness = "claude_code"
model   = "claude-opus-5-5"
effort  = "high"
max_parallel   = 1
max_iterations = 10
verify = ["test", "typecheck"]
standards = "docs/coding-standards.md"
protected_paths = [".claude/"]

[tracker]
kind = "github"
repo = "owner/repo"
ready_label       = "ready-for-agent"
in_progress_label = "in-progress"
cannot_do_label   = "needs-investigation"

[branches]
base_branch = "main"
prefix      = "kraftwerk"

[sandbox]
kind = "container"
run_args = []
read_only_paths = []
writable_paths  = []
scrub_credentials = true

[sandbox.network]
allow = ["registry.npmjs.org"]

[sandbox.env]
[sandbox.secrets]
TEST_DB_PASSWORD = { env = "TEST_DB_PASSWORD" }
GH_TOKEN = { stored = "github" }

[network]
ca_certs = []
upstream_proxy = "http://proxy:8080"

[services]
compose_file = "compose.yaml"
[services.postgres]
shared = false

[commands]
test      = "pnpm test"
typecheck = "pnpm typecheck"

[[hooks.run_start]]
run = "echo start"

[[hooks.sandbox_created]]
parallel = ["cd web && pnpm install --frozen-lockfile", "cd api && uv sync --locked"]
where    = "sandbox"
timeout  = "5m"

[[hooks.spec_finished]]
run = "gh pr create --fill"
secrets = ["GH_TOKEN"]

[steps.implement]
idle_timeout = "10m"
[steps.review]
enabled = true
harness = "claude_code"
model = "claude-sonnet-5-5"
effort = "medium"
timeout = "60m"

[harnesses.claude_code]
method = "bedrock"
region = "eu-central-1"
profile = "dev"

[logs]
keep_days = 30
`

func TestWrongTypeSuggestsASiblingKeyOfThatType(t *testing.T) {
	// [harness.<name>] would clash with `harness = "…"`; settings live
	// under [harnesses.<name>].
	r := load(t, "version = 1\n[harness.claude_code]\nmethod = \"api_key\"\n", "")
	wantOne(t, r, config.Error, "harness", "must be a string", `did you mean "harnesses"?`)
}

func TestOrdinaryEnvNamesDontLookLikeSecrets(t *testing.T) {
	wantClean(t, load(t, "version = 1\n[sandbox.env]\nPWD = \"/app\"\nTOKEN_URL = \"https://auth.example/token\"\n", ""))
}

func TestHostHookSecretsNameProjectSecrets(t *testing.T) {
	cfg := "version = 1\n[sandbox.secrets]\nGH_TOKEN = { stored = \"github\" }\n" +
		"[[hooks.spec_finished]]\nrun = \"gh pr create --fill\"\nsecrets = [\"GH_TOKEN\", \"GH_TOKN\"]\n"
	r := load(t, cfg, "")
	wantOne(t, r, config.Error, "hooks.spec_finished[0].secrets[1]", `"GH_TOKN" is not in [sandbox.secrets]`, `did you mean "GH_TOKEN"?`)
}
