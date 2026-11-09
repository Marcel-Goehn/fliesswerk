package claudecode

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/Marcel-Goehn/kraftwerk/internal/harness"
)

// importAuth finds a Bedrock setup in the user's Claude Code settings, nil if
// there is none. Only where to sign in comes across, never a credential.
func importAuth(home string) *harness.ImportedAuth {
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json")) //nolint:gosec // the user's own settings file
	if err != nil {
		return nil
	}
	var settings struct {
		Env map[string]any `json:"env"`
	}
	if json.Unmarshal(data, &settings) != nil {
		return nil
	}
	setting := func(name string) string { s, _ := settings.Env[name].(string); return s }
	if !slices.Contains([]string{"1", "true", "yes", "on"}, strings.ToLower(setting("CLAUDE_CODE_USE_BEDROCK"))) {
		return nil
	}
	values := map[string]string{}
	var found []string
	for _, f := range []struct{ key, env string }{{"region", "AWS_REGION"}, {"profile", "AWS_PROFILE"}} {
		if v := setting(f.env); v != "" {
			values[f.key] = v
			found = append(found, f.key+" "+v)
		}
	}
	return &harness.ImportedAuth{
		Method:   "bedrock",
		Values:   values,
		Question: fmt.Sprintf("Your Claude Code settings sign in through Amazon Bedrock (%s). Use that here too?", strings.Join(found, ", ")),
	}
}
