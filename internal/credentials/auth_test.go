package credentials_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		{"bedrock isn't here yet", claude, map[string]any{"method": "bedrock", "region": "eu-central-1"}, "bedrock"},
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

// gateway serves /v1/models, accepting only the bearer token want.
func gateway(t *testing.T, want string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/models" || req.Header.Get("Authorization") != "Bearer "+want {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"data": []}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func gatewayAuth(t *testing.T, base, token string) *credentials.Auth {
	t.Helper()
	r := project(t, ".env\n")
	r.Getenv = func(string) string { return token }
	a, err := credentials.ResolveAuth(context.Background(), r, claude,
		map[string]any{"method": "gateway", "base_url": base, "secret": map[string]any{"env": "GATEWAY_TOKEN"}})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestTheTestCallAcceptsAWorkingCredential(t *testing.T) {
	srv := gateway(t, "good-token")
	a := gatewayAuth(t, srv.URL, "good-token")

	if a.Env["ANTHROPIC_BASE_URL"] != srv.URL || a.Env["ANTHROPIC_AUTH_TOKEN"] != "good-token" {
		t.Errorf("env = %v, want the base URL and the token", a.Env)
	}
	if err := a.Test(context.Background(), srv.Client()); err != nil {
		t.Errorf("Test = %v, want nil", err)
	}
}

func TestTheTestCallRefusesABadCredentialWithAFix(t *testing.T) {
	srv := gateway(t, "good-token")
	a := gatewayAuth(t, srv.URL, "bad-token")

	err := a.Test(context.Background(), srv.Client())

	if err == nil {
		t.Fatal("Test = nil, want an error")
	}
	for _, want := range []string{"refused", "401", "GATEWAY_TOKEN"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "bad-token") {
		t.Errorf("err = %q holds the secret", err)
	}
}

func TestTheTestCallSaysWhenItCantReachTheProvider(t *testing.T) {
	srv := gateway(t, "t")
	base := srv.URL
	srv.Close()
	a := gatewayAuth(t, base, "t")

	err := a.Test(context.Background(), http.DefaultClient)
	if err == nil || !strings.Contains(err.Error(), "can't reach") {
		t.Errorf("Test = %v, want it to say the provider can't be reached", err)
	}
}

func TestTheOAuthTestCallListsModelsAtAnthropic(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		got = req
		_, _ = w.Write([]byte(`{"data": []}`))
	}))
	defer srv.Close()
	r := project(t, ".env\n")
	r.Getenv = func(string) string { return "oauth-secret" }
	a, err := credentials.ResolveAuth(context.Background(), r, claude,
		map[string]any{"method": "oauth_token", "secret": map[string]any{"env": "T"}})
	if err != nil {
		t.Fatal(err)
	}

	if err := a.Test(context.Background(), &http.Client{Transport: redirect(srv.URL)}); err != nil {
		t.Fatalf("Test = %v", err)
	}
	if got.Host != "api.anthropic.com" || got.URL.Path != "/v1/models" || got.Header.Get("Authorization") != "Bearer oauth-secret" {
		t.Errorf("request = %s %s%s %v, want GET api.anthropic.com/v1/models with the bearer token", got.Method, got.Host, got.URL.Path, got.Header)
	}
}

// redirect sends every request to base, keeping its Host.
type redirect string

func (base redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	u, _ := url.Parse(string(base))
	out := req.Clone(req.Context())
	out.URL.Scheme, out.URL.Host, out.Host = u.Scheme, u.Host, req.URL.Host
	return http.DefaultTransport.RoundTrip(out)
}
