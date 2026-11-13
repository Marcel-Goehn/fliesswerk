package harness

import (
	"fmt"

	"github.com/Marcel-Goehn/fliesswerk/internal/version"
)

// Description is what a Harness declares about itself. It is static data:
// the core, `init` and `check` act on it.
type Description struct {
	// Name is the value of `harness = "…"` that selects it.
	Name string

	// Binary is the executable's name, looked up on PATH.
	Binary string
	// MinVersion is the oldest version the adapter works with.
	MinVersion string
	// VersionArgs are the arguments that make Binary print its version.
	VersionArgs []string
	// ParseVersion reads the version from that output.
	ParseVersion func(output string) (string, error)

	// ContainerInstall is the Containerfile snippet that installs the
	// Harness, run last as the unprivileged agent user.
	ContainerInstall string
	// HostInstallHint is how to install it on the host for a native
	// Sandbox, by GOOS.
	HostInstallHint map[string]string

	// AuthQuestion is how `init` asks which auth method a person uses.
	AuthQuestion string
	// AuthMethods are the auth methods the Harness accepts, in the order
	// `init` offers them. `check` rejects any other.
	AuthMethods []AuthMethod

	// LoginFix is what to do when the Harness rejects its own login, with
	// no `[harnesses.<name>]` table set.
	LoginFix string

	// ImportAuth finds an auth setup the Harness already has in the user's
	// home, for `init` to offer. Nil, or nil from it, means there is none.
	ImportAuth func(home string) *ImportedAuth

	// FixedEnv are env flags Command always sets, by name.
	FixedEnv []string
	// RedactEnv are the env names whose values are credentials, redacted
	// from every output.
	RedactEnv []string

	// CredentialFile is how the Harness reads a credential the Factory
	// writes to a file, nil if it never does.
	CredentialFile *CredentialFile

	// HomeSeed are files written into the Sandbox's private home before
	// the first Step.
	HomeSeed []HomeFile
	// HomeWritable are paths in the private home, relative to it, that
	// the Harness must be able to write.
	HomeWritable []string

	// StartCheck is how `check` starts the Harness where Steps run, nil if
	// it doesn't.
	StartCheck *StartCheck

	Capabilities Capabilities
}

// StartCheck is how `check` starts the Harness once with a Step's env but
// no credential, to see that it starts where Steps run. It spends no tokens
// and sends no credential anywhere: with none, the Harness stops at saying
// it isn't logged in.
type StartCheck struct {
	// Step is what Command gets. The core never sets a CredentialCommand.
	Step Step
	// Read tells from what the Harness printed whether it started.
	Read func(output string) Start
	// SkipOnHost says, by GOOS, why the Harness isn't started on the host
	// there: where its own login is somewhere a fresh home doesn't hide,
	// it would be logged in and spend tokens.
	SkipOnHost map[string]string
	// LoginEnv are variables that point the Harness at a login of its own
	// or switch on a provider that signs in by itself, left out on the host.
	LoginEnv []string
}

// Start is what a StartCheck read from the Harness.
type Start int

const (
	// StartUnclear is output the Harness doesn't recognise.
	StartUnclear Start = iota
	// Started is the Harness starting, and stopping at the missing login.
	Started
	// NotStarted is the Harness saying it can't start.
	NotStarted
)

// CheckVersion reports what's wrong with the output of the version command.
func (d Description) CheckVersion(output string) error {
	have, err := d.ParseVersion(output)
	if err != nil {
		return fmt.Errorf("can't read %s's version: %w", d.Binary, err)
	}
	if !version.AtLeast(have, d.MinVersion) {
		return fmt.Errorf("%s %s is older than %s, the oldest the Factory works with", d.Binary, have, d.MinVersion)
	}
	return nil
}

// AuthMethod is one way the Harness authenticates, set by `method = "…"`
// in its `[harnesses.<name>]` table.
type AuthMethod struct {
	Name string
	// Label is how `init` offers it.
	Label string
	// Hint, if set, is what `init` says once it has been chosen.
	Hint string
	// Fields are the config keys the method reads, besides `method`.
	Fields []AuthField
	// Env maps each env name the Step gets to the Field its value comes
	// from, or to a fixed value prefixed with "=".
	Env map[string]string
	// Endpoints are the hosts the egress allowlist gains for this method,
	// given the method's resolved field values.
	Endpoints func(fields map[string]string) []string
	// CostBasis is what the cost the Harness reports means with this
	// method; empty is Billed. Read it with Basis.
	CostBasis CostBasis
	// RejectedFix is what to do when the Harness rejects the credential
	// at a Step. The Factory never tests a credential the Harness sends
	// itself (docs/adr/0007-only-the-harness-sends-its-credential.md), so a Step is where a bad one shows.
	RejectedFix string
}

// Basis is what the cost the Harness reports means with the method.
func (m AuthMethod) Basis() CostBasis {
	if m.CostBasis == "" {
		return Billed
	}
	return m.CostBasis
}

// ImportedAuth is an auth setup found on the user's machine.
type ImportedAuth struct {
	// Method is the AuthMethod it is.
	Method string
	// Values are the fields it settles, by key. Never a secret.
	Values map[string]string
	// Question is how `init` offers it.
	Question string
}

// AuthField is one config key of an AuthMethod.
type AuthField struct {
	Key string
	// Prompt is how `init` asks for it: a question, or for a Secret what to
	// call it ("the API key"). A field with none is never asked for.
	Prompt string
	// StoreKey is the name a Secret gets in the user-level credentials file.
	// With NotStored, it is the name `fliesswerk auth set` refuses.
	StoreKey string
	// NotStored, if set, says why the Secret is never kept in the user-level
	// credentials file: `stored` is refused for it, `init` doesn't offer it,
	// and `fliesswerk auth set` won't take StoreKey.
	NotStored string
	// SuggestEnv is the environment variable `init` suggests a Secret comes
	// from, "" for none.
	SuggestEnv string
	// UnlessSet names a field that, once answered, makes this one
	// unnecessary, so `init` doesn't ask for it.
	UnlessSet string
	// Secret marks a credential: it takes a secret source (env, command,
	// stored, dotenv), never a plain value.
	Secret bool
	// Flag marks a true or false setting, "true" among the resolved fields
	// when set.
	Flag     bool
	Optional bool
}

// CredentialFile is how a Harness reads a credential from a file.
type CredentialFile struct {
	// Methods are the auth methods that use it.
	Methods []string
	// Mechanism says in plain words how the Harness reads the file.
	Mechanism string
}

// HomeFile is a file seeded into the private home.
type HomeFile struct {
	// Path is relative to the home.
	Path    string
	Content string
}

// PromptTransport is how a Harness receives the prompt. Never argv.
type PromptTransport string

const (
	Stdin      PromptTransport = "stdin"
	PromptFile PromptTransport = "file"
)

// Capabilities are what a Harness supports.
type Capabilities struct {
	Prompt PromptTransport
	// NativeSchema means the report schema is passed natively, not as a
	// `<report>{…}</report>` footer line.
	NativeSchema bool
	// EffortLevels are the accepted values of `effort`; none means the
	// Harness has no effort setting, and setting one is a `check` error.
	EffortLevels []string
}
