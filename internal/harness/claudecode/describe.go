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

var authMethods = []harness.AuthMethod{
	{
		Name:        "oauth_token",
		Label:       "A Claude subscription",
		Hint:        "Get a long-lived token with `claude setup-token`.",
		Fields:      []harness.AuthField{{Key: "secret", Secret: true, Prompt: "the token", StoreKey: "claude"}},
		Env:         map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "secret"},
		Endpoints:   anthropicEndpoints,
		RejectedFix: "get a new token with `claude setup-token`",
	},
	{
		Name:        "api_key",
		Label:       "An Anthropic API key",
		Fields:      []harness.AuthField{{Key: "secret", Secret: true, Prompt: "the API key", StoreKey: "claude_api_key"}},
		Env:         map[string]string{"ANTHROPIC_API_KEY": "secret"},
		Endpoints:   anthropicEndpoints,
		RejectedFix: "check your API key in the Anthropic Console, and that its account has credit",
	},
	{
		// The Factory mints temporary credentials on the host and hands
		// them over through CredentialFile. Only the user's own keys, passed
		// through on their acknowledgement, go in the env. The free test
		// call is STS GetCallerIdentity, which the AWS SDK signs on the host:
		// the host needs those credentials itself, to mint the Sandbox's.
		Name:  "bedrock",
		Label: "Amazon Bedrock",
		Fields: []harness.AuthField{
			{Key: "region", Prompt: "Which AWS region?"},
			{Key: "profile", Optional: true, Prompt: "Which AWS profile? Enter to give access keys instead"},
			{Key: "access_key_id", Secret: true, Optional: true, Prompt: "the AWS access key ID", StoreKey: "aws_access_key_id", UnlessSet: "profile"},
			{Key: "secret_access_key", Secret: true, Optional: true, Prompt: "the AWS secret access key", StoreKey: "aws_secret_access_key", UnlessSet: "profile"},
			{Key: "role_arn", Optional: true},
			{Key: "i_accept_long_lived_keys_in_sandbox", Flag: true, Optional: true},
		},
		Env: map[string]string{"CLAUDE_CODE_USE_BEDROCK": "=1", "AWS_REGION": "region"},
		Endpoints: func(f map[string]string) []string {
			return []string{"bedrock-runtime." + f["region"] + ".amazonaws.com"}
		},
		RejectedFix: "check that the AWS credentials may call Bedrock in `region`",
	},
	{
		Name:  "gateway",
		Label: "A gateway that speaks Anthropic's API",
		Fields: []harness.AuthField{
			{Key: "base_url", Prompt: "What is the gateway's URL?"},
			{Key: "secret", Secret: true, Prompt: "the gateway token", StoreKey: "claude_gateway"},
		},
		Env: map[string]string{"ANTHROPIC_BASE_URL": "base_url", "ANTHROPIC_AUTH_TOKEN": "secret"},
		Endpoints: func(f map[string]string) []string {
			u, err := url.Parse(f["base_url"])
			if err != nil || u.Hostname() == "" {
				return nil
			}
			return []string{u.Hostname()}
		},
		RejectedFix: "check the gateway token, and that `base_url` is the gateway's",
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

		AuthQuestion: "How do you sign in to Claude?",
		AuthMethods:  authMethods,
		LoginFix:     "sign Claude Code in again by running `claude`, or set a credential in `[harnesses.claude_code]`",
		ImportAuth:   importAuth,

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
