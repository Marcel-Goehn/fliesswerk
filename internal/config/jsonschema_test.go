package config_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/Marcel-Goehn/kraftwerk/internal/config"
)

var update = flag.Bool("update", false, "rewrite the published JSON Schema")

// The JSON Schema published with the sources, for editors to autocomplete
// and validate the config with.
var publishedSchema = filepath.Join("..", "..", "schema", "config.schema.json")

func TestPublishedJSONSchemaIsUpToDate(t *testing.T) {
	got := config.JSONSchema()
	if *update {
		if err := os.MkdirAll(filepath.Dir(publishedSchema), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(publishedSchema, got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(publishedSchema) //nolint:gosec // a fixed path in this repo
	if err != nil {
		t.Fatalf("%v; run `go test ./internal/config -run JSONSchema -update`", err)
	}
	// A Windows checkout may have turned the file's line endings into CRLF.
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	if !bytes.Equal(got, want) {
		t.Fatal("schema/config.schema.json is out of date; run `go test ./internal/config -run JSONSchema -update`")
	}
}

// schemaAt walks the JSON Schema's properties along keys.
func schemaAt(t *testing.T, keys ...string) map[string]any {
	t.Helper()
	var s map[string]any
	if err := json.Unmarshal(config.JSONSchema(), &s); err != nil {
		t.Fatalf("the JSON Schema is not valid JSON: %v", err)
	}
	for _, k := range keys {
		props, _ := s["properties"].(map[string]any)
		next, ok := props[k].(map[string]any)
		if !ok {
			t.Fatalf("no property %q in the JSON Schema", k)
		}
		s = next
	}
	return s
}

func TestJSONSchemaDescribesTheConfig(t *testing.T) {
	root := schemaAt(t)
	if root["additionalProperties"] != false {
		t.Error("unknown top-level keys must be rejected")
	}
	if req, _ := root["required"].([]any); len(req) != 1 || req[0] != "version" {
		t.Errorf("required = %v, want [version]", root["required"])
	}
	if v := schemaAt(t, "version"); v["const"] != float64(1) {
		t.Errorf("version = %v, want const 1", v)
	}
	if k := schemaAt(t, "sandbox", "kind"); len(k["enum"].([]any)) != 3 {
		t.Errorf("sandbox.kind = %v, want an enum of three kinds", k)
	}
	if m := schemaAt(t, "max_parallel"); m["type"] != "integer" || m["minimum"] != float64(1) {
		t.Errorf("max_parallel = %v, want a positive integer", m)
	}
	if kd := schemaAt(t, "logs", "keep_days"); len(kd["oneOf"].([]any)) != 2 {
		t.Errorf("keep_days = %v, want a positive integer or \"forever\"", kd)
	}
	if c := schemaAt(t, "commands"); c["additionalProperties"].(map[string]any)["type"] != "string" {
		t.Errorf("commands = %v, want a free-form table of strings", c)
	}
	if h := schemaAt(t, "hooks", "sandbox_created"); h["type"] != "array" {
		t.Errorf("hooks.sandbox_created = %v, want an array of tables", h)
	}
}

// The published schema must agree with Load: what one accepts, the other
// does. (Rules that span layers, like local-only keys, are Load's alone.)
func TestJSONSchemaAgreesWithLoad(t *testing.T) {
	var doc any
	if err := json.Unmarshal(config.JSONSchema(), &doc); err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(config.SchemaID, doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile(config.SchemaID)
	if err != nil {
		t.Fatalf("the JSON Schema doesn't compile: %v", err)
	}

	cases := map[string]struct {
		toml  string
		valid bool
	}{
		"minimal":            {"version = 1", true},
		"full":               {fullConfig, true},
		"no version":         {`model = "m"`, false},
		"unknown key":        {"version = 1\nmax_paralel = 2", false},
		"wrong type":         {"version = 1\nmax_parallel = \"2\"", false},
		"zero":               {"version = 1\nmax_parallel = 0", false},
		"bad enum":           {"version = 1\n[sandbox]\nkind = \"vm\"", false},
		"keep_days forever":  {"version = 1\n[logs]\nkeep_days = \"forever\"", true},
		"keep_days zero":     {"version = 1\n[logs]\nkeep_days = 0", false},
		"secret value":       {"version = 1\n[sandbox.secrets]\nX = \"hunter2\"", false},
		"two sources":        {"version = 1\n[sandbox.secrets]\nX = { env = \"X\", stored = \"x\" }", false},
		"bad duration":       {"version = 1\n[steps.review]\ntimeout = \"an hour\"", false},
		"hook without run":   {"version = 1\n[[hooks.run_end]]\ntimeout = \"1m\"", false},
		"hook with both":     {"version = 1\n[[hooks.run_end]]\nrun = \"a\"\nparallel = [\"b\"]", false},
		"free-form services": {"version = 1\n[services.db]\nshared = true", true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var values map[string]any
			if err := toml.Unmarshal([]byte(tc.toml), &values); err != nil {
				t.Fatal(err)
			}
			// Round-trip through JSON, as an editor sees the file.
			raw, err := json.Marshal(values)
			if err != nil {
				t.Fatal(err)
			}
			inst, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
			if err != nil {
				t.Fatal(err)
			}
			schemaErr := sch.Validate(inst)
			if (schemaErr == nil) != tc.valid {
				t.Errorf("JSON Schema valid = %v, want %v: %v", schemaErr == nil, tc.valid, schemaErr)
			}
			r := config.Load(project(t, tc.toml, ""), nil)
			if r.OK() != tc.valid {
				t.Errorf("Load OK = %v, want %v: %v", r.OK(), tc.valid, messages(r, config.Error))
			}
		})
	}
}
