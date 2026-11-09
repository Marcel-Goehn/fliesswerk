package harness

import (
	"fmt"

	"github.com/Marcel-Goehn/kraftwerk/internal/version"
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

	// AuthMethods are the auth methods the Harness accepts, in the order
	// `init` offers them. `check` rejects any other.
	AuthMethods []AuthMethod

	// FixedEnv are env flags Command always sets, by name. Values may
	// depend on the Step (EnvScrub is left out when scrubbing is off).
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

	Capabilities Capabilities
}

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
	// Fields are the config keys the method reads, besides `method`.
	Fields []AuthField
	// Env maps each env name the Step gets to the Field its value comes
	// from, or to a fixed value prefixed with "=".
	Env map[string]string
	// Endpoints are the hosts the egress allowlist gains for this method,
	// given the method's resolved field values.
	Endpoints func(fields map[string]string) []string
	// TestCall is the free request that proves the credential works,
	// given the resolved field values.
	TestCall func(fields map[string]string) Request
}

// AuthField is one config key of an AuthMethod.
type AuthField struct {
	Key string
	// Secret marks a credential: it takes a secret source (env, command,
	// stored, dotenv), never a plain value.
	Secret bool
	// Flag marks a true or false setting, "true" among the resolved fields
	// when set.
	Flag     bool
	Optional bool
}

// Request is an HTTP request described as data.
type Request struct {
	Method string
	URL    string
	Header map[string]string
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
