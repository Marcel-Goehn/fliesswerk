package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Marcel-Goehn/fliesswerk/internal/bedrock"
	"github.com/Marcel-Goehn/fliesswerk/internal/credentials"
	"github.com/Marcel-Goehn/fliesswerk/internal/git"
	"github.com/Marcel-Goehn/fliesswerk/internal/harness"
	"github.com/Marcel-Goehn/fliesswerk/internal/harness/claudecode"
	"github.com/Marcel-Goehn/fliesswerk/internal/hook"
	"github.com/Marcel-Goehn/fliesswerk/internal/redact"
	"github.com/Marcel-Goehn/fliesswerk/internal/sandbox"
	"github.com/Marcel-Goehn/fliesswerk/internal/tracker/github"
)

// harnesses are the Harnesses compiled in, by config name.
var harnesses = map[string]harness.Harness{"claude_code": claudecode.ClaudeCode{}}

// defaultHarness is the Harness of a Step that names none, at its level or
// the top level.
const defaultHarness = "claude_code"

// theHarness is the Harness whose credentials a Run resolves: "claude_code"
// is the only one, so every Step uses it.
var theHarness = harnesses[defaultHarness]

// testCallTimeout bounds the free test call of the AWS credentials the host
// mints the Sandbox's from.
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
	// keeper holds the AWS credentials of a running Run, nil for none.
	keeper *bedrock.Keeper
	// credentialFile is where a Step with no Sandbox finds the credential
	// file; in a Sandbox, the Sandbox says.
	credentialFile string
}

// resolveSecrets checks that .fliesswerk/.env is git-ignored and that the
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

// testHarnessCredential proves the credential the host uses itself: the AWS
// credentials it mints the Sandbox's from. Any other credential only the
// Harness sends, so a bad one shows at the first Step (see
// authRejectedFix).
func (s *secrets) testHarnessCredential(ctx context.Context) error {
	if s.auth == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, testCallTimeout)
	defer cancel()
	return s.auth.Test(ctx)
}

// authRejectedFix is what a person does when the Harness rejects its
// credential at a Step.
func (s *secrets) authRejectedFix() string {
	if s.auth == nil {
		return theHarness.Describe().LoginFix
	}
	return s.auth.RejectedFix()
}

// usesBedrock reports whether the Run signs in to AWS on the host, and so
// has a credential directory for its Sandboxes to mount.
func (s *secrets) usesBedrock() bool {
	return s.auth != nil && s.auth.Bedrock != nil
}

// startCredentials mints the Harness's short-lived credentials on the host
// and keeps them fresh until stopCredentials.
func (s *secrets) startCredentials(ctx context.Context, dir string, sandboxed bool, warn func(string)) error {
	if !s.usesBedrock() {
		return nil
	}
	k, err := s.auth.Bedrock.Start(ctx, bedrock.Delivery{Dir: dir, Secret: s.redactor.Add, Warn: warn})
	if err != nil {
		return fmt.Errorf("%s (%s): %w", s.auth.Harness, s.auth.Method, err)
	}
	s.keeper = k
	if !sandboxed && k.InFile() {
		s.credentialFile = k.File()
	}
	return nil
}

// stopCredentials ends the refreshing and removes the credential file.
func (s *secrets) stopCredentials() {
	if s.keeper != nil {
		s.keeper.Stop()
	}
}

// credentialCommand is the command that prints the credential file, "" when
// the credential isn't in one.
func (s *secrets) credentialCommand(sandboxed bool) string {
	if s.keeper == nil || !s.keeper.InFile() {
		return ""
	}
	return sandbox.ReadCredentialCommand(!sandboxed)
}

// authWarnings are what a person should hear about the Harness's credential
// at `check` and at the start of every Run.
func (s *secrets) authWarnings(ctx context.Context) []string {
	if !s.usesBedrock() {
		return nil
	}
	return s.auth.Bedrock.Warnings(ctx, time.Now())
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
	if s.keeper != nil {
		maps.Copy(env, s.keeper.Env())
	}
	if s.credentialFile != "" {
		env[sandbox.CredentialFileEnv] = s.credentialFile
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

// hookEnv is the env of a Hook: a Hook gets no credential of the Factory's
// own and no project secret beyond those ForHook gives it, all of them in
// the Sandbox and only the ones it names on the host.
func (s *secrets) hookEnv(where string, names []string) []string {
	env := s.commandEnv(true)
	for k, v := range credentials.ForHook(s.project, where, names) {
		env = append(env, k+"="+v)
	}
	return env
}

// sandboxCommandEnv is commandEnv for a command in a Sandbox, where nothing of the host's env goes in.
func (s *secrets) sandboxCommandEnv(scrub bool) []string {
	env := slices.Clone(git.NonInteractiveEnv)
	if !scrub {
		for k, v := range s.project {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// sandboxHookEnv is the env of a Hook in a Sandbox: every
// project secret, as ForHook gives any Hook in a Sandbox.
func (s *secrets) sandboxHookEnv() []string {
	env := slices.Clone(git.NonInteractiveEnv)
	for k, v := range credentials.ForHook(s.project, hook.Sandbox, nil) {
		env = append(env, k+"="+v)
	}
	return env
}

// credentialEnv are the variables that hold credentials in a Step or Hook.
func (s *secrets) credentialEnv() []string {
	names := slices.Concat(s.harnessEnv, s.hostOnly)
	if s.auth != nil {
		for k := range s.auth.Env {
			names = append(names, k)
		}
	}
	return names
}

// redacted are stdout and stderr behind the redactor.
func (s *secrets) redacted(stdout, stderr io.Writer) (io.Writer, io.Writer) {
	return s.redactor.Writer(stdout), s.redactor.Writer(stderr)
}
