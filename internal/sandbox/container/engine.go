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
)

// EngineEnv names the engine to use, docker or podman, on a machine that
// has both. Without it Docker is used if it responds, else Podman.
const EngineEnv = "KRAFTWERK_ENGINE"

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
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // the Factory's own engine call
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

// Resolve finds the engine a Run uses. There is no fallback from an engine
// the user chose.
func Resolve(ctx context.Context) (Engine, error) {
	return resolve(ctx, os.Getenv, runCLI)
}

func resolve(ctx context.Context, getenv func(string) string, run cli) (Engine, error) {
	switch chosen := getenv(EngineEnv); chosen {
	case docker:
		return resolveDocker(ctx, run)
	case podman:
		return resolvePodman(ctx, getenv, run)
	case "":
	default:
		return Engine{}, fmt.Errorf("%s is %q; it must be %s or %s", EngineEnv, chosen, docker, podman)
	}
	e, dockerErr := resolveDocker(ctx, run)
	if dockerErr == nil {
		return e, nil
	}
	e, podmanErr := resolvePodman(ctx, getenv, run)
	if podmanErr == nil {
		return e, nil
	}
	return Engine{}, errors.New("the container Sandbox needs a running Docker or Podman, and neither responded; start one, or pick another Sandbox kind with `sandbox.kind`\n  " +
		dockerErr.Error() + "\n  " + podmanErr.Error())
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
