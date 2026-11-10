// Package credentials finds the secrets the Factory needs. The config only
// ever says where a secret comes from (a Source), never its value: an
// environment variable, a command's stdout, the user-level credentials
// file `fliesswerk auth set` writes, or .fliesswerk/.env. Secrets are
// resolved on the host and handed to each Step's env when it starts.
package credentials

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/Marcel-Goehn/fliesswerk/internal/git"
)

// The kinds of Source, as written in the config.
const (
	Env     = "env"
	Command = "command"
	Stored  = "stored"
	Dotenv  = "dotenv"
)

var kinds = []string{Env, Command, Stored, Dotenv}

// Source is where one secret comes from, e.g. `{ env = "ANTHROPIC_API_KEY" }`.
type Source struct {
	Kind string
	// Ref is the variable name, command or key, by Kind.
	Ref string
}

// ParseSource reads a Source from its decoded TOML: a table that sets
// exactly one known source to a string.
func ParseSource(v any) (Source, bool) {
	t, ok := v.(map[string]any)
	if !ok || len(t) != 1 {
		return Source{}, false
	}
	for _, k := range kinds {
		if ref, ok := t[k].(string); ok {
			return Source{Kind: k, Ref: ref}, true
		}
	}
	return Source{}, false
}

func (s Source) String() string {
	return fmt.Sprintf("{ %s = %q }", s.Kind, s.Ref)
}

// fix says how a person makes s yield the secret.
func (s Source) fix() string {
	switch s.Kind {
	case Env:
		return "set the environment variable " + s.Ref
	case Command:
		return fmt.Sprintf("make `%s` print it", s.Ref)
	case Stored:
		return "run `fliesswerk auth set " + s.Ref + "`"
	default:
		return "add `" + s.Ref + "=…` to " + DotenvFile
	}
}

// DotenvFile is the project's secrets file, relative to the project root.
// It must be git-ignored.
const DotenvFile = ".fliesswerk/.env"

// commandTimeout bounds a `command` source.
const commandTimeout = 30 * time.Second

// Resolver resolves Sources on the host.
type Resolver struct {
	// Root is the project root: `command` runs there, and .env is read
	// from it.
	Root string
	// Store is the user-level credentials file (see StorePath).
	Store string
	// Getenv reads the Factory's own environment.
	Getenv func(string) string
}

// Resolve returns the secret s points to. The error says, in plain words,
// what is wrong and how to fix it; it never holds the secret.
func (r Resolver) Resolve(ctx context.Context, s Source) (string, error) {
	var v string
	var err error
	switch s.Kind {
	case Env:
		v = r.Getenv(s.Ref)
		if v == "" {
			err = fmt.Errorf("the environment variable %s is not set", s.Ref)
		}
	case Command:
		v, err = runCommand(ctx, r.Root, s.Ref)
	case Stored:
		v, err = r.stored(s.Ref)
	case Dotenv:
		v, err = r.dotenv(ctx, s.Ref)
	default:
		err = fmt.Errorf("unknown source %q", s.Kind)
	}
	if err != nil {
		return "", fmt.Errorf("%s: %w; %s", s, err, s.fix())
	}
	return v, nil
}

// runCommand runs command through the shell in dir and returns its
// stdout, trimmed.
func runCommand(ctx context.Context, dir, command string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd.exe", "/d", "/s", "/c", command) //nolint:gosec // the user's own configured command
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", command) //nolint:gosec // the user's own configured command
	}
	cmd.Dir = dir
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	// Its stderr is left out of the error on purpose: a helper may print
	// the secret there, and the redactor doesn't know it yet.
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("the command failed (%v); run it yourself to see why", err)
	}
	v := strings.TrimSpace(stdout.String())
	if v == "" {
		return "", errors.New("the command printed nothing")
	}
	return v, nil
}

func (r Resolver) stored(key string) (string, error) {
	values, err := readStore(r.Store)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("there is no credentials file at %s yet", r.Store)
	}
	if err != nil {
		return "", err
	}
	v, ok := values[key]
	if !ok || v == "" {
		return "", fmt.Errorf("%s has no %q", r.Store, key)
	}
	return v, nil
}

func (r Resolver) dotenv(ctx context.Context, key string) (string, error) {
	if err := CheckDotenvIgnored(ctx, r.Root); err != nil {
		return "", err
	}
	data, err := os.ReadFile(DotenvPath(r.Root)) //nolint:gosec // the project's own secrets file
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("there is no %s", DotenvFile)
	}
	if err != nil {
		return "", err
	}
	v, ok := parseDotenv(string(data))[key]
	if !ok || v == "" {
		return "", fmt.Errorf("%s has no %s", DotenvFile, key)
	}
	return v, nil
}

// CheckDotenvIgnored refuses a project whose .fliesswerk/.env git doesn't
// ignore, whether the file exists yet or not: a secret put there must
// never be committed.
func CheckDotenvIgnored(ctx context.Context, root string) error {
	ignored, err := git.Repo{Dir: root}.IsIgnored(ctx, DotenvFile)
	if err != nil {
		return fmt.Errorf("can't tell whether %s is git-ignored (is %s a git checkout?): %w", DotenvFile, root, err)
	}
	if !ignored {
		return fmt.Errorf("%s is not git-ignored, so a secret put there could be committed; add `.env` to .fliesswerk/.gitignore", DotenvFile)
	}
	return nil
}

// parseDotenv reads KEY=VALUE lines. Blank lines and lines starting with
// # are skipped, and a leading `export` is allowed. A value may be in
// double quotes (with \n, \" and \\ escapes) or single quotes (taken
// literally); an unquoted one ends at " #".
func parseDotenv(s string) map[string]string {
	out := map[string]string{}
	for line := range strings.Lines(s) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch {
		case len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"':
			v = strings.NewReplacer(`\n`, "\n", `\"`, `"`, `\\`, `\`).Replace(v[1 : len(v)-1])
		case len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'':
			v = v[1 : len(v)-1]
		default:
			if i := strings.Index(v, " #"); i >= 0 {
				v = strings.TrimSpace(v[:i])
			}
		}
		out[k] = v
	}
	return out
}
