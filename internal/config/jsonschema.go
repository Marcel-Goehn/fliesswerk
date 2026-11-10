package config

import (
	"encoding/json"
	"fmt"
)

// SchemaID is where the published JSON Schema can be fetched from.
const SchemaID = "https://raw.githubusercontent.com/Marcel-Goehn/kraftwerk/main/schema/config.schema.json"

// JSONSchema renders the config's shape as a JSON Schema (draft-07), for
// editors to autocomplete and validate config.toml and config.local.toml
// with. It is published as schema/config.schema.json.
func JSONSchema() []byte {
	s := jsonSchema(schema)
	s["$schema"] = "http://json-schema.org/draft-07/schema#"
	s["$id"] = SchemaID
	s["title"] = "Kraftwerk config"
	s["required"] = []string{"version"}
	out, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		panic(fmt.Sprintf("config: rendering the JSON Schema: %v", err)) // the schema is static
	}
	return append(out, '\n')
}

func jsonSchema(n *node) map[string]any {
	s := map[string]any{}
	switch n.kind {
	case kString:
		s["type"] = "string"
		if n.enum != nil {
			s["enum"] = n.enum
		}
	case kVersion:
		s["type"] = "integer"
		s["const"] = SupportedVersion
	case kInt:
		s["type"] = "integer"
		if n.positive {
			s["minimum"] = 1
		}
	case kBool:
		s["type"] = "boolean"
	case kDuration:
		s["type"] = "string"
		s["pattern"] = `^([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$`
	case kKeepDays:
		s["oneOf"] = []any{
			map[string]any{"type": "integer", "minimum": 1},
			map[string]any{"const": "forever"},
		}
	case kSecret:
		s = jsonSchema(secretSource)
		s["minProperties"] = 1
		s["maxProperties"] = 1
		delete(s, "oneOf")
	case kList:
		s["type"] = "array"
		s["items"] = jsonSchema(n.elem)
	case kTable:
		s["type"] = "object"
		props := map[string]any{}
		for k, f := range n.fields {
			props[k] = jsonSchema(f)
		}
		if len(props) > 0 {
			s["properties"] = props
		}
		if n.extra != nil {
			s["additionalProperties"] = jsonSchema(n.extra)
		} else {
			s["additionalProperties"] = false
		}
		if n.oneOf != nil {
			var alts []any
			for _, k := range n.oneOf {
				alts = append(alts, map[string]any{"required": []string{k}})
			}
			s["oneOf"] = alts
		}
	}
	doc := n.doc
	if n.localOnly != nil {
		doc += " Setting " + n.why + " is allowed only in config.local.toml or as a CLI flag, never in config.toml."
	}
	if doc != "" {
		s["description"] = doc
	}
	return s
}
