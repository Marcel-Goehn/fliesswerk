package claudecode_test

import (
	"slices"
	"testing"

	"github.com/Marcel-Goehn/kraftwerk/internal/harness"
	"github.com/Marcel-Goehn/kraftwerk/internal/harness/claudecode"
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
		testURL   string
		credEnv   string
	}{
		{"oauth_token", map[string]string{"token": "t"}, []string{"api.anthropic.com"}, "https://api.anthropic.com/v1/models", "CLAUDE_CODE_OAUTH_TOKEN"},
		{"api_key", map[string]string{"key": "k"}, []string{"api.anthropic.com"}, "https://api.anthropic.com/v1/models", "ANTHROPIC_API_KEY"},
		{"bedrock", map[string]string{"region": "eu-central-1", "profile": "dev"}, []string{"bedrock-runtime.eu-central-1.amazonaws.com"}, "https://bedrock.eu-central-1.amazonaws.com/foundation-models", ""},
		{"gateway", map[string]string{"base_url": "https://llm.corp.example:8443/anthropic", "token": "t"}, []string{"llm.corp.example"}, "https://llm.corp.example:8443/anthropic/v1/models", "ANTHROPIC_AUTH_TOKEN"},
	}
	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			m := methods[tt.method]
			if got := m.Endpoints(tt.fields); !slices.Equal(got, tt.endpoints) {
				t.Errorf("endpoints = %q, want %q", got, tt.endpoints)
			}
			if got := m.TestCall(tt.fields); got.URL != tt.testURL || got.Method != "GET" {
				t.Errorf("test call = %s %s, want GET %s", got.Method, got.URL, tt.testURL)
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

	for _, name := range []string{"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "CLAUDE_CODE_SUBPROCESS_ENV_SCRUB"} {
		if !slices.Contains(d.FixedEnv, name) {
			t.Errorf("FixedEnv %q lacks %s", d.FixedEnv, name)
		}
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
