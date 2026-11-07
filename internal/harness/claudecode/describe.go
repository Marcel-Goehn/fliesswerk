package claudecode

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/Marcel-Goehn/kraftwerk/internal/harness"
)

const (
	binary = "claude"
	// minVersion is the oldest Claude Code the pinned flags were checked
	// against (`--permission-prompts` is recent). Lower it only after
	// checking an older one.
	minVersion = "2.1.319"
)

var versionRE = regexp.MustCompile(`^\s*(\d+\.\d+\.\d+)`)

func parseVersion(output string) (string, error) {
	m := versionRE.FindStringSubmatch(output)
	if m == nil {
		return "", fmt.Errorf("no version in %q", strings.TrimSpace(output))
	}
	return m[1], nil
}

const anthropicHost = "api.anthropic.com"

func anthropicEndpoints(map[string]string) []string { return []string{anthropicHost} }

// Listing models is free: it proves the credential without spending
// tokens.
func listModels(base string, header map[string]string) harness.Request {
	header["anthropic-version"] = "2023-06-01"
	return harness.Request{Method: "GET", URL: strings.TrimSuffix(base, "/") + "/v1/models", Header: header}
}

var authMethods = []harness.AuthMethod{
	{
		Name:      "oauth_token",
		Fields:    []harness.AuthField{{Key: "secret", Secret: true}},
		Env:       map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "secret"},
		Endpoints: anthropicEndpoints,
		TestCall: func(f map[string]string) harness.Request {
			return listModels("https://"+anthropicHost, map[string]string{
				"Authorization":  "Bearer " + f["secret"],
				"anthropic-beta": "oauth-2025-04-20",
			})
		},
	},
	{
		Name:      "api_key",
		Fields:    []harness.AuthField{{Key: "secret", Secret: true}},
		Env:       map[string]string{"ANTHROPIC_API_KEY": "secret"},
		Endpoints: anthropicEndpoints,
		TestCall: func(f map[string]string) harness.Request {
			return listModels("https://"+anthropicHost, map[string]string{"x-api-key": f["secret"]})
		},
	},
	{
		// The Factory mints temporary credentials on the host and hands
		// them over through CredentialFile, never through the env.
		Name: "bedrock",
		Fields: []harness.AuthField{
			{Key: "region"},
			{Key: "profile", Optional: true},
			{Key: "access_key_id", Secret: true, Optional: true},
			{Key: "secret_access_key", Secret: true, Optional: true},
			{Key: "role_arn", Optional: true},
		},
		Env: map[string]string{"CLAUDE_CODE_USE_BEDROCK": "=1", "AWS_REGION": "region"},
		Endpoints: func(f map[string]string) []string {
			return []string{"bedrock-runtime." + f["region"] + ".amazonaws.com"}
		},
		TestCall: func(f map[string]string) harness.Request {
			return harness.Request{Method: "GET", URL: "https://bedrock." + f["region"] + ".amazonaws.com/foundation-models", SignAWS: "bedrock"}
		},
	},
	{
		Name:   "gateway",
		Fields: []harness.AuthField{{Key: "base_url"}, {Key: "secret", Secret: true}},
		Env:    map[string]string{"ANTHROPIC_BASE_URL": "base_url", "ANTHROPIC_AUTH_TOKEN": "secret"},
		Endpoints: func(f map[string]string) []string {
			u, err := url.Parse(f["base_url"])
			if err != nil || u.Hostname() == "" {
				return nil
			}
			return []string{u.Hostname()}
		},
		TestCall: func(f map[string]string) harness.Request {
			return listModels(f["base_url"], map[string]string{"Authorization": "Bearer " + f["secret"]})
		},
	},
}

// Describe declares what Claude Code needs.
func (ClaudeCode) Describe() harness.Description {
	return harness.Description{
		Name:         "claude_code",
		Binary:       binary,
		MinVersion:   minVersion,
		VersionArgs:  []string{"--version"},
		ParseVersion: parseVersion,

		ContainerInstall: "RUN curl -fsSL https://claude.ai/install.sh | bash\n" +
			"ENV PATH=\"/home/agent/.local/bin:${PATH}\"\n",
		HostInstallHint: map[string]string{
			"linux":   "curl -fsSL https://claude.ai/install.sh | bash",
			"darwin":  "curl -fsSL https://claude.ai/install.sh | bash",
			"windows": "irm https://claude.ai/install.ps1 | iex",
		},

		AuthMethods: authMethods,

		FixedEnv: []string{envNoTraffic, envScrub},
		RedactEnv: []string{
			"CLAUDE_CODE_OAUTH_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN",
			"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN", "AWS_BEARER_TOKEN_BEDROCK",
		},

		CredentialFile: &harness.CredentialFile{
			Methods:   []string{"bedrock"},
			Mechanism: "--settings sets awsCredentialExport to a command that prints the read-only credential file, which holds AWS credentials as JSON ({\"Credentials\": {…}})",
		},

		// Skips the first-run onboarding in a fresh home.
		HomeSeed:     []harness.HomeFile{{Path: ".claude.json", Content: `{"hasCompletedOnboarding":true}`}},
		HomeWritable: []string{".claude", ".claude.json"},

		Capabilities: harness.Capabilities{
			Prompt:       harness.Stdin,
			NativeSchema: true,
			EffortLevels: []string{"low", "medium", "high", "xhigh", "max"},
		},
	}
}
