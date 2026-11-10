package notices_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/fliesswerk/internal/notices"
)

// The notices every release archive ships next to LICENSE.
var committed = filepath.Join("..", "..", notices.FileName)

func TestCommittedNoticesMatchTheModuleGraph(t *testing.T) {
	want, err := notices.Render()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(committed) //nolint:gosec // a fixed path in this repo
	if err != nil {
		t.Fatal(err)
	}
	got = bytes.ReplaceAll(got, []byte("\r\n"), []byte("\n"))
	if !bytes.Equal(got, want) {
		t.Fatal("THIRD_PARTY_NOTICES is out of date with the modules linked into fliesswerk and the egress proxy; run `go generate ./internal/notices` and commit the result")
	}
}

func TestNoticesCoverTheStandardLibraryButNotTestOnlyModules(t *testing.T) {
	got, err := os.ReadFile(committed) //nolint:gosec // a fixed path in this repo
	if err != nil {
		t.Fatal(err)
	}
	text := string(got)
	for _, linked := range []string{"Go standard library", "golang.org/x/net ", "github.com/aws/smithy-go ", "go.yaml.in/yaml/v3 "} {
		if !strings.Contains(text, linked) {
			t.Errorf("THIRD_PARTY_NOTICES doesn't name %q, which the binary links", linked)
		}
	}
	if strings.Contains(text, "santhosh-tekuri/jsonschema") {
		t.Error("THIRD_PARTY_NOTICES names santhosh-tekuri/jsonschema, which only tests use")
	}
}
