// Package sandbox is what the Factory's core knows of a Sandbox: a place
// that runs commands and can be cleared and destroyed. A nil Sandbox means
// no Sandbox (`kind = "none"`): commands run directly on the host.
package sandbox

import (
	"context"
	"maps"
	"os/exec"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/Marcel-Goehn/fliesswerk/internal/shell"
)

// Sandbox is one Sandbox, holding one worktree.
type Sandbox interface {
	// Exec turns a command to run inside the Sandbox, with env in its
	// environment, into the host command that does it. A value of env never
	// goes into hostArgv.
	Exec(argv, env []string) (hostArgv, hostEnv []string)
	// KillAll kills every process left in the Sandbox: ending the host
	// command doesn't end what it started inside.
	KillAll(ctx context.Context) error
	// Services are the Services the Sandbox can reach, none if it has none.
	Services() []Service
	// MemoryUse is how many bytes the Sandbox and its own Services hold now.
	// Shared Services aren't part of it: they exist once, however many
	// Sandboxes there are.
	MemoryUse(ctx context.Context) (int64, error)
	// Destroy removes the Sandbox and everything created for it.
	Destroy(ctx context.Context) error
}

// Service is a Service a Sandbox can reach.
type Service struct {
	Name string
	// Addresses are where to reach it from inside the Sandbox.
	Addresses []string
}

// PublishedService is a running Service with ports on the host's loopback,
// which is how a native Sandbox reaches it.
type PublishedService struct {
	Name string
	// Aliases are the names it answers to, Name first.
	Aliases []string
	Ports   []PublishedPort
}

// PublishedPort is a Service's port in its container, and on the host.
type PublishedPort struct {
	Container, Host int
}

// proxyVariables point a program at a proxy.
var proxyVariables = []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY"}

var nonEnvChars = regexp.MustCompile(`[^A-Z0-9]`)

// ServiceEnv are the variables a Sandbox has for a Service at host and port,
// 0 for none: FLIESSWERK_SERVICE_<NAME>_HOST and _PORT, and the Service's
// `env` templates filled in. Its entries are NAME=value. A template can't set
// a proxy variable: those always name the Sandbox's way out.
func ServiceEnv(name, host string, port int, templates map[string]string) []string {
	prefix := "FLIESSWERK_SERVICE_" + nonEnvChars.ReplaceAllString(strings.ToUpper(name), "_")
	env := []string{prefix + "_HOST=" + host}
	fill := []string{"{host}", host}
	if port != 0 {
		env = append(env, prefix+"_PORT="+strconv.Itoa(port))
		fill = append(fill, "{port}", strconv.Itoa(port))
	}
	filler := strings.NewReplacer(fill...)
	for _, variable := range slices.Sorted(maps.Keys(templates)) {
		if slices.Contains(proxyVariables, strings.ToUpper(variable)) {
			continue
		}
		env = append(env, variable+"="+filler.Replace(templates[variable]))
	}
	return env
}

// Request says what a new Sandbox is for.
type Request struct {
	// Name tells it from the Run's other Sandboxes.
	Name string
	// Worktree is the git worktree it holds, and Rel the project root
	// relative to the top of it.
	Worktree, Rel string
	// Protected are the project's Protected paths, as patterns relative to
	// the project root.
	Protected []string
	// NoServices leaves the Sandbox without its own Services, for one that
	// only exists to ask something.
	NoServices bool
}

// Shell is the host command that runs line with the Sandbox's shell, or
// with the host's in dir when s is nil. It is stopped when ctx ends, though
// what it started inside a Sandbox takes a KillAll.
func Shell(ctx context.Context, s Sandbox, dir, line string, env []string) *exec.Cmd {
	if s == nil {
		cmd := shell.Command(ctx, line)
		cmd.Dir, cmd.Env = dir, env
		return cmd
	}
	argv, hostEnv := s.Exec([]string{"/bin/sh", "-c", line}, env)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // the project's own command, for the Sandbox
	shell.Isolate(cmd)
	cmd.Env = hostEnv
	return cmd
}

// CredentialFile is the name of the credential file the Factory keeps for a
// Harness, in a directory a Sandbox can only read: a directory, because a file
// the Factory replaces would stay the old one in a mount of just the file.
const CredentialFile = "credentials.json"

// CredentialFileEnv names the variable that says where a command finds the
// credential file.
const CredentialFileEnv = "FLIESSWERK_CREDENTIAL_FILE" //nolint:gosec // the name of a variable that holds a path

// CredentialEnv is what points a Sandbox's commands at the credential file
// in dir, a path inside the Sandbox.
func CredentialEnv(dir string) []string {
	return []string{CredentialFileEnv + "=" + dir + "/" + CredentialFile}
}

// ReadCredentialCommand is the shell command that prints the credential file:
// with sh in a Sandbox, or with the host's own shell when there is none.
func ReadCredentialCommand(onHost bool) string {
	if onHost && runtime.GOOS == "windows" {
		return `type "%` + CredentialFileEnv + `%"`
	}
	return `cat "$` + CredentialFileEnv + `"`
}
