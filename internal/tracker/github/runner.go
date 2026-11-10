package github

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/Marcel-Goehn/fliesswerk/internal/shell"
)

// ExecRunner runs the real gh found on PATH.
type ExecRunner struct {
	// Token, if set, reaches gh as GH_TOKEN in its environment, never in
	// its arguments.
	Token string
}

// Run runs gh.
func (r ExecRunner) Run(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	path, err := exec.LookPath("gh")
	if err != nil {
		return nil, ErrNotInstalled
	}
	cmd := exec.CommandContext(ctx, path, args...) //nolint:gosec // gh is the tool this adapter exists to run
	shell.Isolate(cmd)
	cmd.Env = Env(os.Environ(), r.Token)
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return nil, ErrNotInstalled
		}
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return stdout.Bytes(), fmt.Errorf("gh %s: %s", strings.Join(args[:min(2, len(args))], " "), msg)
		}
		return stdout.Bytes(), fmt.Errorf("gh %s: %w", strings.Join(args[:min(2, len(args))], " "), err)
	}
	return stdout.Bytes(), nil
}

// TokenVars are the environment variables gh reads a token from.
var TokenVars = []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN"}

// Env is the environment gh runs with: base, never prompting, plus token
// as GH_TOKEN when one is configured (which then replaces any token in
// base).
func Env(base []string, token string) []string {
	out := make([]string, 0, len(base)+3)
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		if token != "" && isTokenVar(name) {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1")
	if token != "" {
		out = append(out, "GH_TOKEN="+token)
	}
	return out
}

func isTokenVar(name string) bool {
	for _, v := range TokenVars {
		if strings.EqualFold(name, v) {
			return true
		}
	}
	return false
}
