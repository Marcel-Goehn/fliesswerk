package credentials_test

import (
	"context"
	"maps"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/kraftwerk/internal/credentials"
)

func TestProjectSecretsUseTheSameSources(t *testing.T) {
	r := project(t, ".env\n")
	r.Getenv = func(name string) string { return map[string]string{"CI_DB": "postgres://u:pw@db/app"}[name] } //nolint:gosec // a made-up secret
	if err := credentials.Store(r.Store, "github", "gh-secret"); err != nil {
		t.Fatal(err)
	}

	got, err := credentials.ProjectSecrets(context.Background(), r, map[string]any{
		"DATABASE_URL": map[string]any{"env": "CI_DB"},
		"GH_TOKEN":     map[string]any{"stored": "github"},
	})

	want := map[string]string{"DATABASE_URL": "postgres://u:pw@db/app", "GH_TOKEN": "gh-secret"} //nolint:gosec // made-up secrets
	if err != nil || !maps.Equal(got, want) {
		t.Errorf("ProjectSecrets = %v, %v; want %v", got, err, want)
	}
}

func TestProjectSecretsNameTheOneThatFailed(t *testing.T) {
	_, err := credentials.ProjectSecrets(context.Background(), project(t, ".env\n"), map[string]any{
		"API_KEY": map[string]any{"env": "UNSET"},
	})
	if err == nil || !strings.Contains(err.Error(), "sandbox.secrets.API_KEY") || !strings.Contains(err.Error(), "UNSET") {
		t.Errorf("err = %v, want it to name sandbox.secrets.API_KEY and UNSET", err)
	}
}

func TestHooksGetProjectSecrets(t *testing.T) {
	all := map[string]string{"GH_TOKEN": "g", "DATABASE_URL": "d"}

	if got := credentials.ForHook(all, "sandbox", nil); !maps.Equal(got, all) {
		t.Errorf("a Sandbox Hook gets %v, want them all", got)
	}
	if got := credentials.ForHook(all, "host", []string{"GH_TOKEN"}); !maps.Equal(got, map[string]string{"GH_TOKEN": "g"}) {
		t.Errorf("a host Hook naming GH_TOKEN gets %v, want only it", got)
	}
	if got := credentials.ForHook(all, "host", nil); len(got) != 0 {
		t.Errorf("a host Hook naming none gets %v, want none", got)
	}
}
