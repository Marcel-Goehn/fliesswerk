package claudecode_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/fliesswerk/internal/harness"
	"github.com/Marcel-Goehn/fliesswerk/internal/harness/claudecode"
)

func TestDescribeBinaryAndVersion(t *testing.T) {
	d := claudecode.ClaudeCode{}.Describe()

	if d.Name != "claude_code" || d.Binary != "claude" {
		t.Errorf("name, binary = %q, %q", d.Name, d.Binary)
	}
	if d.MinVersion == "" || !slices.Equal(d.VersionArgs, []string{"--version"}) {
		t.Errorf("min version %q, version args %q", d.MinVersion, d.VersionArgs)
	}
	v, err := d.ParseVersion("2.1.319 (Claude Code)\n")
	if err != nil || v != "2.1.319" {
		t.Errorf("ParseVersion = %q, %v; want 2.1.319", v, err)
	}
	if _, err := d.ParseVersion("command not found"); err == nil {
		t.Errorf("ParseVersion accepted output without a version")
	}
}

func TestCheckVersionWantsTheMinimum(t *testing.T) {
	d := claudecode.ClaudeCode{}.Describe()

	if err := d.CheckVersion("2.1.319 (Claude Code)\n"); err != nil {
		t.Errorf("the minimum itself: %v", err)
	}
	if err := d.CheckVersion("2.10.0 (Claude Code)\n"); err != nil {
		t.Errorf("a newer one, compared as numbers: %v", err)
	}
	err := d.CheckVersion("2.1.9 (Claude Code)\n")
	if err == nil || !strings.Contains(err.Error(), "2.1.9") || !strings.Contains(err.Error(), d.MinVersion) {
		t.Errorf("an older one: %v", err)
	}
	if err := d.CheckVersion("command not found"); err == nil {
		t.Error("output without a version passed")
	}
}

func TestDescribeInstall(t *testing.T) {
	d := claudecode.ClaudeCode{}.Describe()
	if d.ContainerInstall == "" {
		t.Error("no Containerfile snippet")
	}
	for _, goos := range []string{"linux", "darwin", "windows"} {
		if d.HostInstallHint[goos] == "" {
			t.Errorf("no host install hint for %s", goos)
		}
	}
}

func TestDescribeAuthMethods(t *testing.T) {
	d := claudecode.ClaudeCode{}.Describe()
	methods := map[string]harness.AuthMethod{}
	var names []string
	for _, m := range d.AuthMethods {
		methods[m.Name] = m
		names = append(names, m.Name)
	}
	if want := []string{"oauth_token", "api_key", "bedrock", "gateway"}; !slices.Equal(names, want) {
		t.Fatalf("auth methods = %q, want %q", names, want)
	}

	tests := []struct {
		method    string
		fields    map[string]string
		endpoints []string
		credEnv   string
	}{
		{"oauth_token", map[string]string{"secret": "t"}, []string{"api.anthropic.com"}, "CLAUDE_CODE_OAUTH_TOKEN"},
		{"api_key", map[string]string{"secret": "k"}, []string{"api.anthropic.com"}, "ANTHROPIC_API_KEY"},
		{"bedrock", map[string]string{"region": "eu-central-1", "profile": "dev"}, []string{"bedrock-runtime.eu-central-1.amazonaws.com"}, ""},
		{"gateway", map[string]string{"base_url": "https://llm.corp.example:8443/anthropic", "secret": "t"}, []string{"llm.corp.example"}, "ANTHROPIC_AUTH_TOKEN"},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			m := methods[tt.method]
			if got := m.Endpoints(tt.fields); !slices.Equal(got, tt.endpoints) {
				t.Errorf("endpoints = %q, want %q", got, tt.endpoints)
			}
			if tt.credEnv == "" {
				return
			}
			if _, ok := m.Env[tt.credEnv]; !ok {
				t.Errorf("env %v doesn't set %s", m.Env, tt.credEnv)
			}
			if !slices.Contains(d.RedactEnv, tt.credEnv) {
				t.Errorf("%s isn't redacted: %q", tt.credEnv, d.RedactEnv)
			}
		})
	}
}

func TestDescribeEnvAndHome(t *testing.T) {
	d := claudecode.ClaudeCode{}.Describe()

	if !slices.Contains(d.FixedEnv, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC") || slices.Contains(d.FixedEnv, "CLAUDE_CODE_SUBPROCESS_ENV_SCRUB") {
		t.Errorf("FixedEnv = %q, want CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC and never CLAUDE_CODE_SUBPROCESS_ENV_SCRUB", d.FixedEnv)
	}
	for _, name := range []string{"AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"} {
		if !slices.Contains(d.RedactEnv, name) {
			t.Errorf("RedactEnv %q lacks %s", d.RedactEnv, name)
		}
	}
	if d.CredentialFile == nil || !slices.Equal(d.CredentialFile.Methods, []string{"bedrock"}) {
		t.Errorf("credential file = %+v, want one for bedrock", d.CredentialFile)
	}
	if len(d.HomeSeed) == 0 || !slices.Contains(d.HomeWritable, ".claude") {
		t.Errorf("home seed %+v, writable %q", d.HomeSeed, d.HomeWritable)
	}
}

func TestDescribeCapabilities(t *testing.T) {
	c := claudecode.ClaudeCode{}.Describe().Capabilities
	if c.Prompt != harness.Stdin || !c.NativeSchema {
		t.Errorf("capabilities = %+v, want stdin and a native schema", c)
	}
	if want := []string{"low", "medium", "high", "xhigh", "max"}; !slices.Equal(c.EffortLevels, want) {
		t.Errorf("effort levels = %q, want %q", c.EffortLevels, want)
	}
}
