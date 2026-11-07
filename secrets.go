package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/credentials"
	"github.com/Marcel-Goehn/kraftwerk/internal/git"
	"github.com/Marcel-Goehn/kraftwerk/internal/harness"
	"github.com/Marcel-Goehn/kraftwerk/internal/harness/claudecode"
	"github.com/Marcel-Goehn/kraftwerk/internal/redact"
	"github.com/Marcel-Goehn/kraftwerk/internal/tracker/github"
)

// harnesses are the Harnesses compiled in, by config name.
var harnesses = map[string]harness.Harness{"claude_code": claudecode.ClaudeCode{}}

// defaultHarness is the Harness of a Step that names none, at its level or
// the top level.
const defaultHarness = "claude_code"

// theHarness is the Harness whose credentials a Run resolves: "claude_code"
// is the only one, so every Step uses it.
var theHarness = harnesses[defaultHarness]

// testCallTimeout bounds a credential's free test call.
const testCallTimeout = 30 * time.Second

// secrets are everything secret a Run (or `check`) uses, resolved on the
// host, and the redactor that knows them all.
type secrets struct {
	// auth is the Harness's credential, nil when the Harness signs in with
	// its own login.
	auth *credentials.Auth
	// project are the `[sandbox.secrets]`, by env name.
	project map[string]string
	// trackerToken is empty when the Tracker uses its own login.
	trackerToken string
	redactor     *redact.Redactor
	// hostOnly are variables of the Factory's own env a Step never
	// inherits: gh's token variables, the project secrets' names and every
	// variable an `env` source reads. A Step gets a secret only as stepEnv
	// hands it over.
	hostOnly []string
	// harnessEnv are the Harness's credential variables, left out of a
	// Step's inherited env when a credential is configured.
	harnessEnv []string
}

// resolveSecrets checks that .kraftwerk/.env is git-ignored and that the
// Harness's auth config fits the methods it declares, then resolves every
// secret the config references. An error is for a person to fix before
// anything can start; it names the fix and never holds a secret.
func resolveSecrets(ctx context.Context, values map[string]any, root string) (*secrets, error) {
	if err := credentials.CheckDotenvIgnored(ctx, root); err != nil {
		return nil, err
	}
	desc := theHarness.Describe()
	table, _ := valueAt(values, "harnesses", desc.Name).(map[string]any)
	if problems := credentials.CheckAuth(desc, table); len(problems) > 0 {
		return nil, errors.New(strings.Join(problems, "\n"))
	}
	store, err := credentials.StorePath()
	if err != nil {
		return nil, err
	}
	r := credentials.Resolver{Root: root, Store: store, Getenv: os.Getenv}

	s := &secrets{redactor: redact.New(), harnessEnv: desc.RedactEnv}
	projectTable, _ := valueAt(values, "sandbox", "secrets").(map[string]any)
	s.hostOnly = append(s.hostOnly, github.TokenVars...)
	for name, src := range projectTable {
		s.hostOnly = append(s.hostOnly, name)
		s.hostOnly = appendEnvSource(s.hostOnly, src)
	}
	for _, src := range table {
		s.hostOnly = appendEnvSource(s.hostOnly, src)
	}
	s.hostOnly = appendEnvSource(s.hostOnly, valueAt(values, "tracker", "token"))
	// Credentials the Factory's own env holds are known secrets too, used
	// or not: an agent could still print them.
	for _, name := range append(desc.RedactEnv, github.TokenVars...) {
		s.redactor.Add(os.Getenv(name))
	}
	if s.auth, err = credentials.ResolveAuth(ctx, r, desc, table); err != nil {
		return nil, err
	}
	if s.auth != nil {
		for _, v := range s.auth.Secrets {
			s.redactor.Add(v)
		}
	}
	if s.project, err = credentials.ProjectSecrets(ctx, r, projectTable); err != nil {
		return nil, err
	}
	for _, v := range s.project {
		s.redactor.Add(v)
	}
	if s.trackerToken, err = trackerToken(ctx, r, values); err != nil {
		return nil, err
	}
	s.redactor.Add(s.trackerToken)
	return s, nil
}

// appendEnvSource appends the variable v reads from, if v is an `env`
// Source.
func appendEnvSource(names []string, v any) []string {
	if src, ok := credentials.ParseSource(v); ok && src.Kind == credentials.Env {
		return append(names, src.Ref)
	}
	return names
}

// trackerToken resolves `tracker.token`, empty when it isn't set.
func trackerToken(ctx context.Context, r credentials.Resolver, values map[string]any) (string, error) {
	v := valueAt(values, "tracker", "token")
	if v == nil {
		return "", nil
	}
	src, ok := credentials.ParseSource(v)
	if !ok {
		return "", errors.New("tracker.token: must say where the token comes from, e.g. `{ stored = \"github\" }`")
	}
	token, err := r.Resolve(ctx, src)
	if err != nil {
		return "", fmt.Errorf("tracker.token: %w", err)
	}
	return token, nil
}

// testHarnessCredential makes the Harness credential's free test call.
// With no credential configured there is nothing to test.
func (s *secrets) testHarnessCredential(ctx context.Context) error {
	if s.auth == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, testCallTimeout)
	defer cancel()
	return s.auth.Test(ctx, http.DefaultClient)
}

// stepEnv is what each Step's env gets: the Harness's credential, and the
// project secrets only when scrubbing is off, the user having chosen to
// let the agent's own commands see credentials. unset are the variables
// of the Factory's own env a Step must not inherit: every host-only one,
// and the Harness's credential variables when a configured credential
// must win over the user's own.
func (s *secrets) stepEnv(scrub bool) (env map[string]string, unset []string) {
	env = map[string]string{}
	unset = append(unset, s.hostOnly...)
	if !scrub {
		for k, v := range s.project {
			env[k] = v
		}
	}
	if s.auth != nil {
		for k, v := range s.auth.Env {
			env[k] = v
		}
		unset = append(unset, s.harnessEnv...)
	}
	return env, unset
}

// commandEnv is the env of the Feedback commands the Factory runs itself.
// They run the agent's code, so they get no more than the agent's own
// commands: the Factory's env without host-only variables, the project
// secrets only when scrubbing is off, and never a Harness credential,
// not even one from the Factory's own env while scrubbing is on.
func (s *secrets) commandEnv(scrub bool) []string {
	unset := slices.Clone(s.hostOnly)
	if scrub {
		unset = append(unset, s.harnessEnv...)
	}
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return slices.ContainsFunc(unset, func(u string) bool { return strings.EqualFold(u, name) })
	})
	env = append(env, git.NonInteractiveEnv...)
	if !scrub {
		for k, v := range s.project {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// redacted are stdout and stderr behind the redactor.
func (s *secrets) redacted(stdout, stderr io.Writer) (io.Writer, io.Writer) {
	return s.redactor.Writer(stdout), s.redactor.Writer(stderr)
}
