package config_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
)

// The config reference, which documents every key of the published schema.
var reference = filepath.Join("..", "..", "docs", "config.md")

// keyHeading is how the reference names a key: a level-3 heading holding
// the key in backticks, or a level-2 one for a table that opens a section.
var keyHeading = regexp.MustCompile("(?m)^#{2,3} `([^`]+)`")

// The keys every Step and every Hook point share are documented once.
var (
	perStep  = regexp.MustCompile(`^steps\.[a-z_]+\.`)
	perPoint = regexp.MustCompile(`^hooks\.[a-z_]+\[\]\.`)
)

func TestConfigReferenceDocumentsEveryKeyOfTheSchema(t *testing.T) {
	data, err := os.ReadFile(publishedSchema) //nolint:gosec // a fixed path in this repo
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	schemaKeys(root, "", want)

	doc, err := os.ReadFile(reference) //nolint:gosec // a fixed path in this repo
	if err != nil {
		t.Fatal(err)
	}
	doc = bytes.ReplaceAll(doc, []byte("\r\n"), []byte("\n"))
	got := map[string]bool{}
	for _, m := range keyHeading.FindAllSubmatch(doc, -1) {
		key := string(m[1])
		if got[key] {
			t.Errorf("docs/config.md documents `%s` twice", key)
		}
		got[key] = true
	}

	for _, k := range sortedSet(want) {
		if !got[k] {
			t.Errorf("docs/config.md has no heading for `%s`, but the schema has that key", k)
		}
	}
	for _, k := range sortedSet(got) {
		if !want[k] {
			t.Errorf("docs/config.md documents `%s`, which the schema doesn't have", k)
		}
	}
}

// schemaKeys adds the key path of every property under s to keys. A list's
// entries are `key[]`, a free-form table's keys `<name>`, and a secret
// reference is one key, its sources documented once.
func schemaKeys(s map[string]any, prefix string, keys map[string]bool) {
	props, _ := s["properties"].(map[string]any)
	for name, p := range props {
		addKey(p.(map[string]any), prefix+name, keys)
	}
	if extra, ok := s["additionalProperties"].(map[string]any); ok {
		addKey(extra, prefix+"<name>", keys)
	}
}

// addKey adds path, the key s describes, and the keys under it.
func addKey(s map[string]any, path string, keys map[string]bool) {
	path = perStep.ReplaceAllString(path, "steps.<step>.")
	path = perPoint.ReplaceAllString(path, "hooks.<point>[].")
	keys[path] = true
	if isSecretRef(s) {
		return
	}
	if items, ok := s["items"].(map[string]any); ok {
		schemaKeys(items, path+"[].", keys)
	}
	schemaKeys(s, path+".", keys)
}

// isSecretRef reports whether s says where a secret comes from.
func isSecretRef(s map[string]any) bool {
	props, _ := s["properties"].(map[string]any)
	_, stored := props["stored"]
	return stored && s["maxProperties"] != nil
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
