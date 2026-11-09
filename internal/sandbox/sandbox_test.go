package sandbox_test

import (
	"slices"
	"testing"

	"github.com/Marcel-Goehn/kraftwerk/internal/sandbox"
)

func TestServiceEnvNamesTheAddressAndFillsInTheTemplates(t *testing.T) {
	got := sandbox.ServiceEnv("my-db", "127.0.0.1", 40123, map[string]string{
		"DATABASE_URL": "postgres://u@{host}:{port}/app",
		"PLAIN":        "{other}",
	})
	want := []string{
		"KRAFTWERK_SERVICE_MY_DB_HOST=127.0.0.1",
		"KRAFTWERK_SERVICE_MY_DB_PORT=40123",
		"DATABASE_URL=postgres://u@127.0.0.1:40123/app",
		"PLAIN={other}",
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestAServiceTemplateCannotRedirectTheProxy(t *testing.T) {
	got := sandbox.ServiceEnv("db", "db", 5432, map[string]string{"HTTPS_PROXY": "http://{host}:{port}", "no_proxy": "*"})
	want := []string{"KRAFTWERK_SERVICE_DB_HOST=db", "KRAFTWERK_SERVICE_DB_PORT=5432"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestServiceEnvWithoutAPortHasNoPortVariable(t *testing.T) {
	got := sandbox.ServiceEnv("db", "db", 0, nil)
	if want := []string{"KRAFTWERK_SERVICE_DB_HOST=db"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}
