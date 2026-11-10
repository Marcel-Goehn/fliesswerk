package credentials

import (
	"context"
	"fmt"
)

// ProjectSecrets resolves `[sandbox.secrets]`: environment variable name
// to Source. They come from the same sources as a Harness's credential.
func ProjectSecrets(ctx context.Context, r Resolver, table map[string]any) (map[string]string, error) {
	out := make(map[string]string, len(table))
	for _, name := range sortedKeys(table) {
		src, ok := ParseSource(table[name])
		if !ok {
			return nil, fmt.Errorf("sandbox.secrets.%s: must say where the secret comes from, e.g. `{ stored = \"name\" }`", name)
		}
		v, err := r.Resolve(ctx, src)
		if err != nil {
			return nil, fmt.Errorf("sandbox.secrets.%s: %w", name, err)
		}
		out[name] = v
	}
	return out, nil
}

// ForHook is the project secrets a Hook gets: all of them for a Hook in
// the Sandbox, only those it names for a Hook on the host.
func ForHook(secrets map[string]string, where string, names []string) map[string]string {
	if where == "sandbox" {
		out := make(map[string]string, len(secrets))
		for k, v := range secrets {
			out[k] = v
		}
		return out
	}
	out := map[string]string{}
	for _, n := range names {
		if v, ok := secrets[n]; ok {
			out[n] = v
		}
	}
	return out
}
