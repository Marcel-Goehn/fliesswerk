package claudecode

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/Marcel-Goehn/fliesswerk/internal/harness"
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

// startCheckPrompt is what `check` starts Claude Code with. Without a
// credential it never reaches a model.
const startCheckPrompt = "fliesswerk check: does Claude Code start? Reply with OK."

// readStart tells from Claude Code's output whether it started: with no
// credential, it says it isn't logged in. One that can't start says so
// first.
func readStart(output string) harness.Start {
	switch {
	case strings.Contains(output, "could not start"):
		return harness.NotStarted
	case strings.Contains(output, "Not logged in"):
		return harness.Started
	}
	return harness.StartUnclear
}

const anthropicHost = "api.anthropic.com"

func anthropicEndpoints(map[string]string) []string { return []string{anthropicHost} }

var authMethods = []harness.AuthMethod{
	{
		Name:  "oauth_token",
		Label: "A Claude subscription",
		Hint:  "Get a long-lived token with `claude setup-token`.",
		// Anthropic's terms: developers "may not collect, store, or
		// intermediate Claude.ai credentials or session tokens"
		// (https://code.claude.com/docs/en/legal-and-compliance).
		Fields: []harness.AuthField{{
			Key: "secret", Secret: true, Prompt: "the token", StoreKey: "claude",
			NotStored:  "Anthropic's terms don't let other tools store a Claude subscription token",
			SuggestEnv: "CLAUDE_CODE_OAUTH_TOKEN",
		}},
		Env:       map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "secret"},
		Endpoints: anthropicEndpoints,
		// Claude Code's total_cost_usd is what the tokens would cost at
		// API prices; the subscription covers them.
		CostBasis:   harness.SubscriptionEstimate,
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

		FixedEnv: []string{envNoTraffic},
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

		StartCheck: &harness.StartCheck{
			Step:       harness.Step{Prompt: startCheckPrompt, ReportSchema: harness.ReportSchema},
			Read:       readStart,
			SkipOnHost: map[string]string{"darwin": "Claude Code keeps its own login in the Keychain, which a fresh home doesn't hide"},
			// Where its own login lives, and what switches it to a provider
			// that signs in from the user's own AWS or Google setup.
			LoginEnv: []string{
				"CLAUDE_CONFIG_DIR", "ANTHROPIC_BASE_URL",
				"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY",
				"AWS_PROFILE", "AWS_SHARED_CREDENTIALS_FILE", "AWS_CONFIG_FILE", "GOOGLE_APPLICATION_CREDENTIALS",
			},
		},

		Capabilities: harness.Capabilities{
			Prompt:       harness.Stdin,
			NativeSchema: true,
			EffortLevels: []string{"low", "medium", "high", "xhigh", "max"},
		},
	}
}
