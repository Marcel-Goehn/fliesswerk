package container

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Marcel-Goehn/fliesswerk/internal/proxyimage"
)

// Engine is one connection to a container engine, resolved once per Run.
// Every call goes through it, so that all of a Run's containers and
// networks are on the same engine.
type Engine struct {
	// Binary is docker or podman.
	Binary string
	// Connection is the Docker context or Podman connection, "" for
	// Podman's local engine.
	Connection string
	// Rootless reports that the engine runs as the user, which decides how
	// a Sandbox's user maps to the owner of the worktree.
	Rootless bool
}

// String names the engine and its connection.
func (e Engine) String() string {
	if e.Connection == "" {
		return e.Binary
	}
	return e.Binary + " (" + e.Connection + ")"
}

// argv is the command line of an engine call.
func (e Engine) argv(args ...string) []string {
	argv := []string{e.Binary}
	if e.Connection != "" {
		flag := "--connection"
		if e.Binary == docker {
			flag = "--context"
		}
		argv = append(argv, flag, e.Connection)
	}
	return append(argv, args...)
}

// run makes an engine call and returns its stdout. An error holds what the
// engine said.
func (e Engine) run(ctx context.Context, args ...string) (string, error) {
	return runCLI(ctx, e.argv(args...)...)
}

// removeRetryDelay is the wait before a second try at removing a container.
const removeRetryDelay = 500 * time.Millisecond

// remove force-removes a container, at once: its first process ignores
// SIGTERM, and Podman would wait out its stop timeout. A second try covers
// rootless Podman failing to stop its network helper (an AppArmor denial
// on Ubuntu), which reports an error for a container that is going away.
func (e Engine) remove(ctx context.Context, name string) error {
	args := []string{"rm", "-f", name}
	if e.Binary == podman {
		args = []string{"rm", "-f", "--time", "0", name}
	}
	_, err := e.run(ctx, args...)
	if err == nil {
		return nil
	}
	select {
	case <-time.After(removeRetryDelay):
	case <-ctx.Done():
		return err
	}
	_, err = e.run(ctx, args...)
	return err
}

const (
	docker = "docker"
	podman = "podman"
)

// cli makes a command-line call and returns its stdout.
type cli func(ctx context.Context, argv ...string) (string, error)

func runCLI(ctx context.Context, argv ...string) (string, error) {
	return runCLIWith(ctx, nil, argv...)
}

// runCLIWith is runCLI with env added to the host's.
func runCLIWith(ctx context.Context, env []string, argv ...string) (string, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // the Factory's own engine call
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), fmt.Errorf("%s: %s", strings.Join(argv[:min(len(argv), 4)], " "), msg)
	}
	return stdout.String(), nil
}

// proxyArch is the architecture of the sidecar's image: the engine's, which
// in a VM needn't be the host's. An engine that can't run Linux containers
// is an error that says what to do.
func proxyArch(ctx context.Context, e Engine) (string, error) {
	format := "{{.OSType}} {{.Architecture}}"
	if e.Binary == podman {
		format = "{{.Host.OS}} {{.Host.Arch}}"
	}
	out, err := e.run(ctx, "info", "--format", format)
	if err != nil {
		return "", err
	}
	osType, arch, _ := strings.Cut(strings.TrimSpace(out), " ")
	if osType == "windows" {
		return "", errors.New("the engine is Docker Desktop in Windows-containers mode, which can't run the container Sandbox's proxy sidecar, a Linux container; choose \"Switch to Linux containers\" in Docker Desktop's menu, or pick another Sandbox kind with `sandbox.kind`")
	}
	switch arch {
	case "amd64", "x86_64":
		return "amd64", nil
	case "arm64", "aarch64":
		return "arm64", nil
	}
	return "", fmt.Errorf("the container engine runs on %q, and the proxy sidecar exists only for amd64 and arm64; pick another Sandbox kind with `sandbox.kind`", arch)
}

// CheckEngine resolves the configured engine and reports one that can't run
// the proxy sidecar, and a Factory that has no sidecar to give it.
func CheckEngine(ctx context.Context, binary string) (Engine, error) {
	e, err := Resolve(ctx, binary)
	if err != nil {
		return Engine{}, err
	}
	arch, err := proxyArch(ctx, e)
	if err != nil {
		return Engine{}, err
	}
	if _, err := proxyimage.Rootfs(arch); err != nil {
		return Engine{}, err
	}
	return e, nil
}

// Resolve connects to the engine `sandbox.engine` names, docker or podman.
// There is no fallback to the other one.
func Resolve(ctx context.Context, binary string) (Engine, error) {
	return resolve(ctx, binary, os.Getenv, runCLI, exec.LookPath)
}

func resolve(ctx context.Context, binary string, getenv func(string) string, run cli, lookPath func(string) (string, error)) (Engine, error) {
	var e Engine
	var err error
	switch binary {
	case docker, podman:
	case "":
		return Engine{}, errors.New("sandbox.engine isn't set, and the container Sandbox and the Services need a container engine; set `engine = \"docker\"` or `engine = \"podman\"` under [sandbox] in .fliesswerk/config.local.toml")
	default:
		return Engine{}, fmt.Errorf("sandbox.engine is %q; it must be %s or %s", binary, docker, podman)
	}
	if _, err := lookPath(binary); err != nil {
		return Engine{}, fmt.Errorf("sandbox.engine is %s, and `%s` isn't on PATH; install it (%s), or set the other engine in `sandbox.engine`", binary, binary, installCommands[binary])
	}
	if binary == docker {
		e, err = resolveDocker(ctx, run)
	} else {
		e, err = resolvePodman(ctx, getenv, run)
	}
	if err != nil {
		return Engine{}, fmt.Errorf("sandbox.engine is %s, and it didn't respond; start it, or set the other engine in `sandbox.engine`\n  %w", binary, err)
	}
	// The Containerfile's `RUN --mount` needs BuildKit, which Docker only
	// has with its buildx plugin. Distribution packages often leave it out.
	if binary == docker {
		if _, err := run(ctx, docker, "buildx", "version"); err != nil {
			return Engine{}, fmt.Errorf("sandbox.engine is docker, and it has no buildx plugin, which builds the Sandbox image; install it (Debian/Ubuntu: `sudo apt install docker-buildx`; see https://docs.docker.com/go/buildx/), or set podman in `sandbox.engine`\n  %w", err)
		}
	}
	return e, nil
}

// installCommands say how to install each engine.
var installCommands = map[string]string{
	docker: "Debian/Ubuntu: `sudo apt install docker.io docker-buildx`, Fedora: `sudo dnf install moby-engine docker-buildx`, Arch: `sudo pacman -S docker docker-buildx`, macOS: `brew install --cask docker`; see https://docs.docker.com/engine/install/",
	podman: "Debian/Ubuntu: `sudo apt install podman`, Fedora: `sudo dnf install podman`, Arch: `sudo pacman -S podman`, macOS: `brew install podman`; see https://podman.io/docs/installation",
}

// resolveDocker pins Docker's current context, which also honours
// DOCKER_CONTEXT and DOCKER_HOST.
func resolveDocker(ctx context.Context, run cli) (Engine, error) {
	out, err := run(ctx, docker, "context", "show")
	if err != nil {
		return Engine{}, err
	}
	e := Engine{Binary: docker, Connection: strings.TrimSpace(out)}
	security, err := run(ctx, e.argv("info", "--format", "{{json .SecurityOptions}}")...)
	if err != nil {
		return Engine{}, err
	}
	e.Rootless = strings.Contains(security, "rootless")
	return e, nil
}

// resolvePodman pins Podman's connection: CONTAINER_CONNECTION, else the
// default one. CONTAINER_HOST and a local engine need no flag.
func resolvePodman(ctx context.Context, getenv func(string) string, run cli) (Engine, error) {
	e := Engine{Binary: podman, Connection: getenv("CONTAINER_CONNECTION")}
	if e.Connection == "" && getenv("CONTAINER_HOST") == "" {
		out, err := run(ctx, podman, "system", "connection", "list", "--format", "json")
		if err != nil {
			return Engine{}, err
		}
		var connections []struct {
			Name    string
			Default bool
		}
		if err := json.Unmarshal([]byte(out), &connections); err != nil {
			return Engine{}, fmt.Errorf("reading Podman's connections: %w", err)
		}
		for _, c := range connections {
			if c.Default {
				e.Connection = c.Name
			}
		}
	}
	rootless, err := run(ctx, e.argv("info", "--format", "{{.Host.Security.Rootless}}")...)
	if err != nil {
		return Engine{}, err
	}
	e.Rootless = strings.TrimSpace(rootless) == "true"
	return e, nil
}
