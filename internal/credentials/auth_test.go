package credentials_test

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/kraftwerk/internal/credentials"
	"github.com/Marcel-Goehn/kraftwerk/internal/harness"
	"github.com/Marcel-Goehn/kraftwerk/internal/harness/claudecode"
)

var claude = claudecode.ClaudeCode{}.Describe()

func TestCheckAuth(t *testing.T) {
	onlyOAuth := claude
	onlyOAuth.AuthMethods = claude.AuthMethods[:1]

	tests := []struct {
		name  string
		desc  harness.Description
		table map[string]any
		want  string // a problem contains it; "" for none
	}{
		{"nothing set uses the Harness's own login", claude, nil, ""},
		{"oauth_token with a secret", claude, map[string]any{"method": "oauth_token", "secret": map[string]any{"stored": "claude"}}, ""},
		{"gateway with a base_url", claude, map[string]any{"method": "gateway", "base_url": "https://llm.corp.example", "secret": map[string]any{"env": "T"}}, ""},
		{"a secret without a method", claude, map[string]any{"secret": map[string]any{"stored": "claude"}}, "set `method`"},
		{"a method the Harness doesn't declare", onlyOAuth, map[string]any{"method": "api_key", "secret": map[string]any{"env": "K"}}, `claude_code doesn't accept method = "api_key"; use "oauth_token"`},
		{"gateway without base_url", claude, map[string]any{"method": "gateway", "secret": map[string]any{"env": "T"}}, "method = \"gateway\" needs `base_url`"},
		{"gateway with a base_url that isn't a URL", claude, map[string]any{"method": "gateway", "base_url": "llm.corp", "secret": map[string]any{"env": "T"}}, "base_url must be an http or https URL"},
		{"a method without its secret", claude, map[string]any{"method": "api_key"}, "method = \"api_key\" needs `secret`"},
		{"a key the method doesn't use", claude, map[string]any{"method": "oauth_token", "secret": map[string]any{"env": "T"}, "base_url": "https://x"}, "`base_url` isn't used with method = \"oauth_token\""},
		{"bedrock with a profile", claude, map[string]any{"method": "bedrock", "region": "eu-central-1", "profile": "dev"}, ""},
		{"bedrock with keys and a role", claude, map[string]any{"method": "bedrock", "region": "eu-central-1", "access_key_id": map[string]any{"env": "A"}, "secret_access_key": map[string]any{"env": "S"}, "role_arn": "arn:aws:iam::1:role/x"}, ""},
		{"bedrock passing keys through", claude, map[string]any{"method": "bedrock", "region": "r", "profile": "dev", "i_accept_long_lived_keys_in_sandbox": true}, ""},
		{"bedrock without a region", claude, map[string]any{"method": "bedrock", "profile": "dev"}, "needs `region`"},
		{"bedrock with neither profile nor keys", claude, map[string]any{"method": "bedrock", "region": "r"}, "`profile`, or `access_key_id` and `secret_access_key`"},
		{"bedrock with both profile and keys", claude, map[string]any{"method": "bedrock", "region": "r", "profile": "dev", "access_key_id": map[string]any{"env": "A"}, "secret_access_key": map[string]any{"env": "S"}}, "not both"},
		{"bedrock with half a key pair", claude, map[string]any{"method": "bedrock", "region": "r", "access_key_id": map[string]any{"env": "A"}}, "`secret_access_key`"},
		{"bedrock with a role and pass-through", claude, map[string]any{"method": "bedrock", "region": "r", "profile": "dev", "role_arn": "arn:aws:iam::1:role/x", "i_accept_long_lived_keys_in_sandbox": true}, "`role_arn` or"},
		{"bedrock with a key as plain text", claude, map[string]any{"method": "bedrock", "region": "r", "access_key_id": "AKIA", "secret_access_key": map[string]any{"env": "S"}}, "must say where the secret comes from"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			problems := credentials.CheckAuth(tt.desc, tt.table)
			if tt.want == "" {
				if len(problems) != 0 {
					t.Errorf("problems = %q, want none", problems)
				}
				return
			}
			if !strings.Contains(strings.Join(problems, "\n"), tt.want) {
				t.Errorf("problems = %q, want one containing %q", problems, tt.want)
			}
		})
	}
}

func TestResolveAuthPutsTheSecretInTheStepEnv(t *testing.T) {
	r := project(t, ".env\n")
	if err := credentials.Store(r.Store, "claude", "oauth-secret"); err != nil {
		t.Fatal(err)
	}

	a, err := credentials.ResolveAuth(context.Background(), r, claude,
		map[string]any{"method": "oauth_token", "secret": map[string]any{"stored": "claude"}})

	if err != nil {
		t.Fatal(err)
	}
	if a.Env["CLAUDE_CODE_OAUTH_TOKEN"] != "oauth-secret" || len(a.Env) != 1 {
		t.Errorf("env = %v, want only CLAUDE_CODE_OAUTH_TOKEN", a.Env)
	}
	if len(a.Secrets) != 1 || a.Secrets[0] != "oauth-secret" {
		t.Errorf("secrets = %q, want the token", a.Secrets)
	}
}

func TestResolveAuthWithNothingSetIsNil(t *testing.T) {
	a, err := credentials.ResolveAuth(context.Background(), project(t, ".env\n"), claude, nil)
	if a != nil || err != nil {
		t.Errorf("ResolveAuth = %+v, %v; want nil, nil", a, err)
	}
}

func TestResolveAuthNamesTheFix(t *testing.T) {
	_, err := credentials.ResolveAuth(context.Background(), project(t, ".env\n"), claude,
		map[string]any{"method": "api_key", "secret": map[string]any{"stored": "anthropic"}})
	if err == nil || !strings.Contains(err.Error(), "kraftwerk auth set anthropic") {
		t.Errorf("err = %v, want it to name `kraftwerk auth set anthropic`", err)
	}
}

func TestAGatewaysStepEnvHoldsItsBaseURLAndToken(t *testing.T) {
	r := project(t, ".env\n")
	r.Getenv = func(string) string { return "good-token" }
	a, err := credentials.ResolveAuth(context.Background(), r, claude,
		map[string]any{"method": "gateway", "base_url": "https://llm.corp.example", "secret": map[string]any{"env": "GATEWAY_TOKEN"}})
	if err != nil {
		t.Fatal(err)
	}

	if a.Env["ANTHROPIC_BASE_URL"] != "https://llm.corp.example" || a.Env["ANTHROPIC_AUTH_TOKEN"] != "good-token" {
		t.Errorf("env = %v, want the base URL and the token", a.Env)
	}
}

func TestTLSEndpointKeepsAGatewaysPortAndSkipsPlainHTTP(t *testing.T) {
	tests := []struct {
		name  string
		table map[string]any
		want  string
	}{
		{"the Harness's own host", map[string]any{"method": "api_key", "secret": map[string]any{"stored": "k"}}, "api.anthropic.com"},
		{"a gateway on its own port", map[string]any{"method": "gateway", "base_url": "https://llm.corp.example:8443/v1", "secret": map[string]any{"stored": "k"}}, "llm.corp.example:8443"},
		{"a gateway on the default port", map[string]any{"method": "gateway", "base_url": "https://llm.corp.example/anthropic", "secret": map[string]any{"stored": "k"}}, "llm.corp.example"},
		{"a gateway without TLS", map[string]any{"method": "gateway", "base_url": "http://llm.corp.example", "secret": map[string]any{"stored": "k"}}, ""},
		{"a gateway without TLS on its own port", map[string]any{"method": "gateway", "base_url": "http://llm.corp.example:8080", "secret": map[string]any{"stored": "k"}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := project(t, ".env\n")
			if err := credentials.Store(r.Store, "k", "a-secret"); err != nil {
				t.Fatal(err)
			}
			a, err := credentials.ResolveAuth(context.Background(), r, claude, tt.table)
			if err != nil {
				t.Fatal(err)
			}
			if got := a.TLSEndpoint(); got != tt.want {
				t.Errorf("TLSEndpoint = %q, want %q", got, tt.want)
			}
		})
	}
}

// fakeSTSEndpoint is an STS that answers GetCallerIdentity with status, and
// records the request's form and Authorization header.
func fakeSTSEndpoint(t *testing.T, status int, body string) (form *url.Values, auth *string) {
	t.Helper()
	form, auth = &url.Values{}, new(string)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = req.ParseForm()
		*form, *auth = req.PostForm, req.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/xml")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("AWS_ENDPOINT_URL_STS", srv.URL)
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(t.TempDir(), "none"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(t.TempDir(), "none"))
	return form, auth
}

func bedrockAuth(t *testing.T, more map[string]any) *credentials.Auth {
	t.Helper()
	r := project(t, ".env\n")
	r.Getenv = func(name string) string {
		return map[string]string{"KEY_ID": "AKIAEXAMPLEKEY", "KEY_SECRET": "long-lived-secret"}[name] //nolint:gosec // test values
	}
	table := map[string]any{
		"method": "bedrock", "region": "eu-central-1",
		"access_key_id": map[string]any{"env": "KEY_ID"}, "secret_access_key": map[string]any{"env": "KEY_SECRET"},
	}
	maps.Copy(table, more)
	a, err := credentials.ResolveAuth(context.Background(), r, claude, table)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

const callerIdentity = `<GetCallerIdentityResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><GetCallerIdentityResult><Arn>arn:aws:iam::123:user/dev</Arn><UserId>U</UserId><Account>123</Account></GetCallerIdentityResult></GetCallerIdentityResponse>`

func TestBedrockIsTestedWithASignedGetCallerIdentity(t *testing.T) {
	form, auth := fakeSTSEndpoint(t, http.StatusOK, callerIdentity)
	a := bedrockAuth(t, nil)

	if err := a.Test(context.Background()); err != nil {
		t.Fatalf("Test = %v", err)
	}
	if got := form.Get("Action"); got != "GetCallerIdentity" {
		t.Errorf("Action = %q, want GetCallerIdentity", got)
	}
	if !strings.Contains(*auth, "Credential=AKIAEXAMPLEKEY/") || !strings.Contains(*auth, "/sts/aws4_request") {
		t.Errorf("Authorization = %q, want it signed for STS with the key", *auth)
	}
}

func TestABedrockCredentialAWSRefusesNamesTheFix(t *testing.T) {
	fakeSTSEndpoint(t, http.StatusForbidden, `<ErrorResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><Error><Type>Sender</Type><Code>InvalidClientTokenId</Code><Message>bad</Message></Error></ErrorResponse>`)
	a := bedrockAuth(t, nil)

	err := a.Test(context.Background())

	if err == nil {
		t.Fatal("Test = nil, want an error")
	}
	for _, want := range []string{"claude_code (bedrock)", "InvalidClientTokenId", "KEY_SECRET"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "long-lived-secret") {
		t.Errorf("err = %q holds the secret", err)
	}
}

func TestABedrockProfileThatFailsPointsAtTheProfile(t *testing.T) {
	fakeSTSEndpoint(t, http.StatusForbidden, `<ErrorResponse xmlns="https://sts.amazonaws.com/doc/2011-06-15/"><Error><Type>Sender</Type><Code>ExpiredToken</Code><Message>old</Message></Error></ErrorResponse>`)
	config := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(config, []byte("[profile dev]\naws_access_key_id = AKIAP\naws_secret_access_key = S\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AWS_CONFIG_FILE", config)
	a, err := credentials.ResolveAuth(context.Background(), project(t, ".env\n"), claude, map[string]any{"method": "bedrock", "region": "eu-central-1", "profile": "dev"})
	if err != nil {
		t.Fatal(err)
	}

	err = a.Test(context.Background())

	if err == nil || !strings.Contains(err.Error(), "aws sso login --profile dev") {
		t.Errorf("err = %v, want it to name the profile's fix", err)
	}
}

func TestBedrockEnvHoldsNoCredentialAndOnlyTheRuntimeEndpointIsAllowed(t *testing.T) {
	fakeSTSEndpoint(t, http.StatusOK, callerIdentity)
	a := bedrockAuth(t, nil)

	if a.Env["CLAUDE_CODE_USE_BEDROCK"] != "1" || a.Env["AWS_REGION"] != "eu-central-1" || len(a.Env) != 2 {
		t.Errorf("env = %v, want only the Bedrock switch and the region", a.Env)
	}
	if len(a.Endpoints) != 1 || a.Endpoints[0] != "bedrock-runtime.eu-central-1.amazonaws.com" {
		t.Errorf("endpoints = %q, want only bedrock-runtime", a.Endpoints)
	}
	if a.Bedrock == nil {
		t.Error("Bedrock = nil, want a client")
	}
	if slices.Contains(a.Secrets, "") || !slices.Contains(a.Secrets, "long-lived-secret") {
		t.Errorf("secrets = %q, want the resolved keys", a.Secrets)
	}
}
