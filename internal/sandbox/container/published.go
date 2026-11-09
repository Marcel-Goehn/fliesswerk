package container

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Marcel-Goehn/kraftwerk/internal/sandbox"
)

// PublishedOptions are what every Sandbox of a Run shares.
type PublishedOptions struct {
	// Root is the project root, and StateDir the runtime state in it.
	Root, StateDir string
	// ComposeFile is the compose file relative to the project root, and
	// Services the Services the Sandboxes reach.
	ComposeFile string
	Services    []string
}

// Published runs a project's Services for Sandboxes outside any engine, which
// reach them on ports published on the host's loopback: each Sandbox gets its
// own Compose project, as a container Sandbox does.
type Published struct {
	opts PublishedOptions
	tool composeTool
}

// NewPublished is a Published that still has to be connected.
func NewPublished(opts PublishedOptions) *Published { return &Published{opts: opts} }

// resolvePublishedTool finds the engine and compose tool the Services need.
func resolvePublishedTool(ctx context.Context) (composeTool, error) {
	engine, err := Resolve(ctx)
	if err != nil {
		return composeTool{}, fmt.Errorf("the project's Services run in a container engine, even with a native Sandbox: %w", err)
	}
	return resolveCompose(ctx, engine, runCLI, exec.LookPath)
}

// Connect resolves the engine and its compose tool.
func (p *Published) Connect(ctx context.Context) error {
	tool, err := resolvePublishedTool(ctx)
	if err != nil {
		return err
	}
	p.tool = tool
	return nil
}

// DownLeft removes the Compose projects a crashed Run's Sandboxes left.
func (p *Published) DownLeft(ctx context.Context) error {
	return downRecorded(ctx, p.tool, p.opts.StateDir)
}

// Validate refuses a compose file these Services can't be run from.
func (p *Published) Validate(ctx context.Context) error {
	_, err := validatePublished(ctx, p.tool, p.opts.Root, p.opts.ComposeFile, p.opts.Services)
	return err
}

// CheckPublishedServices is Validate for `check`, where no engine at all is
// as much of a problem as a bad compose file.
func CheckPublishedServices(ctx context.Context, root, composeFile string, services []string) error {
	tool, err := resolvePublishedTool(ctx)
	if err != nil {
		return err
	}
	_, err = validatePublished(ctx, tool, root, composeFile, services)
	return err
}

// validatePublished is the compose rules, and that each Service has a TCP
// port on the host for a Sandbox to reach.
func validatePublished(ctx context.Context, tool composeTool, root, composeFile string, services []string) (composeProject, error) {
	cp, err := tool.validate(ctx, root, composeFile, services)
	if err != nil {
		return composeProject{}, err
	}
	var problems []string
	for _, name := range services {
		if !cp.Services[name].publishesTCP() {
			problems = append(problems, fmt.Sprintf("compose file: Service %s publishes no port, and a native Sandbox reaches a Service only through one; give it `ports: [\"127.0.0.1:<H>:<C>\"]`", name))
		}
	}
	if len(problems) > 0 {
		return composeProject{}, errors.New(strings.Join(problems, "\n"))
	}
	return cp, nil
}

// publishesTCP reports whether the Service's `ports` hold a TCP port.
func (s composeService) publishesTCP() bool {
	for _, v := range s.Ports {
		if slices.ContainsFunc(parsePorts(v), func(p port) bool { return !p.udp }) {
			return true
		}
	}
	return false
}

// Start brings up a Sandbox's own copy of the Services and returns where they
// are published, for those configured, and what removes them, which is also
// returned with an error once anything may have started. state is the
// Sandbox's directory in the runtime state.
func (p *Published) Start(ctx context.Context, state string, req sandbox.Request) (running []sandbox.PublishedService, stop func(context.Context) error, err error) {
	ctx, cancel := context.WithTimeout(ctx, servicesTimeout)
	defer cancel()
	root := filepath.Join(req.Worktree, req.Rel)
	cp, err := validatePublished(ctx, p.tool, root, p.opts.ComposeFile, p.opts.Services)
	if err != nil {
		return nil, nil, err
	}
	project := composeProjectName(p.opts.Root, projectHash(p.opts.Root), req.Name)
	stack, err := writeStack(cp, p.opts.Services, project, filepath.Join(root, p.opts.ComposeFile), state)
	if err != nil {
		return nil, nil, err
	}
	stop = func(ctx context.Context) error { return p.tool.down(ctx, stack) }
	running, err = p.up(ctx, stack, cp)
	return running, stop, err
}

// up starts the project and reads back the configured Services' ports. What
// a Service `depends_on` runs, but is nothing a Sandbox may reach.
func (p *Published) up(ctx context.Context, stack composeStack, cp composeProject) ([]sandbox.PublishedService, error) {
	if err := p.tool.up(ctx, stack, p.opts.Services); err != nil {
		return nil, err
	}
	containers, err := p.tool.running(ctx, stack)
	if err != nil {
		return nil, err
	}
	services := make([]sandbox.PublishedService, 0, len(p.opts.Services))
	for _, name := range p.opts.Services {
		i := slices.IndexFunc(containers, func(c composeContainer) bool { return c.Service == name })
		if i < 0 {
			return nil, fmt.Errorf("the Service %s isn't running after it was started; a native Sandbox can only reach a Service that keeps running", name)
		}
		ports, err := p.tool.publishedPorts(ctx, containers[i].Name, cp.Services[name])
		if err != nil {
			return nil, fmt.Errorf("finding the ports of the Service %s: %w", name, err)
		}
		if len(ports) == 0 {
			return nil, fmt.Errorf("the Service %s has no port published on the host, as `network_mode` can cause; a native Sandbox reaches a Service only through one", name)
		}
		services = append(services, sandbox.PublishedService{Name: name, Aliases: cp.Services[name].aliases(name), Ports: ports})
	}
	return services, nil
}

// publishedPorts asks the engine rather than the compose tool, whose `port`
// prints differently per provider. A port with no host side is left out.
func (c composeTool) publishedPorts(ctx context.Context, container string, spec composeService) ([]sandbox.PublishedPort, error) {
	out, err := c.run(ctx, c.env, c.engine.argv("inspect", "--format", "{{json .NetworkSettings.Ports}}", container)...)
	if err != nil {
		return nil, err
	}
	var bindings map[string][]struct{ HostPort string }
	if err := json.Unmarshal([]byte(out), &bindings); err != nil {
		return nil, fmt.Errorf("reading the published ports: %w", err)
	}
	var ports []sandbox.PublishedPort
	for _, declared := range spec.declaredPorts() {
		binding := bindings[strconv.Itoa(declared.number)+"/tcp"]
		if declared.udp || len(binding) == 0 {
			continue
		}
		host, err := strconv.Atoi(binding[0].HostPort)
		if err != nil {
			return nil, fmt.Errorf("reading the published port %q: %w", binding[0].HostPort, err)
		}
		ports = append(ports, sandbox.PublishedPort{Container: declared.number, Host: host})
	}
	return ports, nil
}
