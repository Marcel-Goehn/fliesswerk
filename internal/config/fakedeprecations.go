//go:build fwkfake

package config

// No key has been renamed or removed yet, so a test build brings its own.
func init() {
	deprecations = []deprecation{
		{old: "sandbox.type", new: "sandbox.kind"},
		{old: "implement_model", new: "steps.implement.model"},
		{old: "auth", new: "harnesses.claude_code"},
		{old: "legacy_flag", note: "it has done nothing since 1.1"},
	}
}
