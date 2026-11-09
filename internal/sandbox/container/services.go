package container

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/proxyimage"
	"github.com/Marcel-Goehn/kraftwerk/internal/sandbox"
)

// servicesTimeout bounds starting a Sandbox's Services, image pulls
// included.
const servicesTimeout = 10 * time.Minute

// Files in a Sandbox's directory of the runtime state.
const (
	overrideFile = "compose-override.yaml"
	stackFile    = "compose-stack.json"
)

// runningService is a Service container on a Sandbox's network.
type runningService struct {
	// name is the Service's, aliases what it answers to, the name first.
	name    string
	aliases []string
	ip      string
	ports   []int
	// localhost are the ports it has on the Sandbox's localhost.
	localhost []int
}

// CheckServices enforces the compose tool's version and the rules on the
// compose file, for `check`. Having no engine at all is left to the Run,
// which stops with the fix.
func CheckServices(ctx context.Context, root, composeFile string, own, shared []string) error {
	e, err := Resolve(ctx)
	if err != nil {
		return nil
	}
	tool, err := resolveCompose(ctx, e, runCLI, exec.LookPath)
	if err != nil {
		return err
	}
	_, err = tool.validate(ctx, root, composeFile, own, shared)
	return err
}

// startServices brings up the Sandbox's own copy of the Services and connects
// them and the Run's shared Services to its network. The stack is on record
// before anything starts, so that a failure part way still gets cleaned up.
func (p *Provider) startServices(ctx context.Context, b *box, req sandbox.Request) ([]runningService, error) {
	if len(p.opts.Services) == 0 && p.shared == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, servicesTimeout)
	defer cancel()
	var services []runningService
	if len(p.opts.Services) > 0 {
		root := filepath.Join(req.Worktree, req.Rel)
		cp, err := p.compose.validate(ctx, root, p.opts.ComposeFile, p.opts.Services, p.opts.Shared)
		if err != nil {
			return nil, err
		}
		stack, err := writeStack(cp, p.opts.Services, composeProjectName(p.opts.Root, p.project, req.Name), filepath.Join(root, p.opts.ComposeFile), b.state)
		if err != nil {
			return nil, err
		}
		b.compose, b.stack = p.compose, &stack
		p.log("Starting the Services of %s", req.Name)
		if err := p.compose.up(ctx, stack, p.opts.Services); err != nil {
			return nil, err
		}
		containers, err := p.compose.running(ctx, stack)
		if err != nil {
			return nil, err
		}
		for _, c := range containers {
			s, err := p.connectService(ctx, b.name, c, cp.Services[c.Service])
			if err != nil {
				return nil, err
			}
			b.ownContainers = append(b.ownContainers, c.Name)
			services = append(services, s)
		}
	}
	if p.shared != nil {
		for _, c := range p.shared.containers {
			// A helper of both sides: the Sandbox keeps its own copy.
			if slices.ContainsFunc(services, func(s runningService) bool { return s.name == c.Service }) {
				continue
			}
			b.sharedContainers = append(b.sharedContainers, c.Name)
			s, err := p.connectService(ctx, b.name, c, p.shared.project.Services[c.Service])
			if err != nil {
				return nil, err
			}
			services = append(services, s)
		}
	}
	claimLocalhost(services, p.log)
	return services, nil
}

// StartShared brings up the shared Services from the worktree in req, on
// record like a Sandbox's so that a crashed Run's get cleaned up. Each Sandbox
// connects them to its own network. It returns what removes them, also with
// an error once anything may have started.
func (p *Provider) StartShared(ctx context.Context, req sandbox.Request) (func(context.Context) error, error) {
	ctx, cancel := context.WithTimeout(ctx, servicesTimeout)
	defer cancel()
	root := filepath.Join(req.Worktree, req.Rel)
	cp, err := p.compose.validate(ctx, root, p.opts.ComposeFile, p.opts.Services, p.opts.Shared)
	if err != nil {
		return nil, err
	}
	state := filepath.Join(p.opts.StateDir, "sandboxes", req.Name)
	if err := os.MkdirAll(state, 0o750); err != nil {
		return nil, err
	}
	stack, err := writeStack(cp, p.opts.Shared, composeProjectName(p.opts.Root, p.project, req.Name), filepath.Join(root, p.opts.ComposeFile), state)
	if err != nil {
		return nil, err
	}
	stop := func(ctx context.Context) error {
		if err := p.compose.down(ctx, stack); err != nil {
			return err // the record says what is still to remove
		}
		return os.RemoveAll(state)
	}
	if err := p.compose.up(ctx, stack, p.opts.Shared); err != nil {
		return stop, err
	}
	containers, err := p.compose.running(ctx, stack)
	if err != nil {
		return stop, err
	}
	p.shared = &sharedServices{project: cp, containers: containers}
	return stop, nil
}

// sharedServices are the Run's shared Services, once they run.
type sharedServices struct {
	project    composeProject
	containers []composeContainer
}

// writeStack writes a Sandbox's compose override and puts its Compose project
// on record in the Sandbox's directory of the runtime state.
func writeStack(cp composeProject, services []string, project, file, state string) (composeStack, error) {
	stack := composeStack{Project: project, File: file, Override: filepath.Join(state, overrideFile)}
	if err := os.WriteFile(stack.Override, []byte(cp.override(services, project)), 0o600); err != nil {
		return composeStack{}, err
	}
	record, err := json.Marshal(stack)
	if err != nil {
		return composeStack{}, err
	}
	if err := os.WriteFile(filepath.Join(state, stackFile), record, 0o600); err != nil {
		return composeStack{}, err
	}
	return stack, nil
}

// connectService puts a Service container on the Sandbox's network. Its old
// container_name stays a name the project's code can use.
func (p *Provider) connectService(ctx context.Context, network string, c composeContainer, spec composeService) (runningService, error) {
	s := runningService{name: c.Service, aliases: spec.aliases(c.Service)}
	args := []string{"network", "connect"}
	for _, alias := range s.aliases {
		args = append(args, "--alias", alias)
	}
	if _, err := p.engine.run(ctx, append(args, network, c.Name)...); err != nil {
		return runningService{}, fmt.Errorf("connecting the Service %s: %w", c.Service, err)
	}
	ip, err := p.addressOn(ctx, c.Name, network)
	if err != nil {
		return runningService{}, err
	}
	s.ip = ip
	for _, port := range spec.declaredPorts() {
		if !port.udp {
			s.ports = append(s.ports, port.number)
		}
	}
	if len(s.ports) == 0 {
		// Nothing declared: what the image says it listens on.
		out, err := p.engine.run(ctx, "image", "inspect", "--format", "{{json .Config.ExposedPorts}}", c.Image)
		if err != nil {
			return runningService{}, fmt.Errorf("finding the ports of the Service %s: %w", c.Service, err)
		}
		s.ports = imagePorts(out)
	}
	if len(s.ports) == 0 {
		p.log("warning: the Service %s declares no port; give it `expose:` in the compose file for its address to be listed", c.Service)
	}
	return s, nil
}

// hostArgs make a Service's names resolve in the Sandbox. Podman's internal
// networks have no DNS, so aliases alone wouldn't do.
func hostArgs(services []runningService) []string {
	var args []string
	for _, s := range services {
		for _, alias := range s.aliases {
			args = append(args, "--add-host", alias+":"+s.ip)
		}
	}
	return args
}

// serviceHosts are the names that must not go through the egress proxy.
func serviceHosts(services []runningService) []string {
	var hosts []string
	for _, s := range services {
		hosts = append(hosts, s.aliases...)
	}
	return hosts
}

// serviceEnv are the variables naming each Service's address, and its
// `env` templates filled in.
func serviceEnv(services []runningService, templates map[string]map[string]string) []string {
	var env []string
	for _, s := range services {
		port := 0
		if len(s.ports) > 0 {
			port = s.ports[0]
		}
		env = append(env, sandbox.ServiceEnv(s.name, s.name, port, templates[s.name])...)
	}
	return env
}

// claimLocalhost sorts the Services by name and decides which has each
// port on localhost: when two listen on the same port, the first keeps it.
func claimLocalhost(services []runningService, warn func(format string, args ...any)) {
	slices.SortFunc(services, func(a, b runningService) int { return strings.Compare(a.name, b.name) })
	taken := map[int]string{}
	for i, s := range services {
		for _, port := range s.ports {
			if other, ok := taken[port]; ok {
				warn("warning: localhost:%d stays with the Service %s; reach %s as %s:%d", port, other, s.name, s.name, port)
				continue
			}
			taken[port] = s.name
			services[i].localhost = append(services[i].localhost, port)
		}
	}
}

// forwardRules are what makes `localhost:<port>` reach each Service.
func forwardRules(services []runningService) []string {
	var rules []string
	for _, s := range services {
		for _, port := range s.localhost {
			rules = append(rules, fmt.Sprintf("127.0.0.1:%d=%s:%d", port, s.ip, port))
		}
	}
	return rules
}

// startForwarder runs the localhost forwarder in a container of its own that
// shares the Sandbox's network namespace: it needs no tool in the project's
// image, and a KillAll, which signals every process the Sandbox can see,
// doesn't reach it.
func (p *Provider) startForwarder(ctx context.Context, b *box, labels []string, services []runningService) error {
	rules := forwardRules(services)
	if len(rules) == 0 {
		return nil
	}
	name := b.name + "-forward"
	args := []string{
		"run", "-d", "--name", name, "--network", "container:" + b.name, "--entrypoint", proxyimage.Entrypoint,
		"--read-only", "--cap-drop", "ALL", "--cap-add", "NET_BIND_SERVICE", "--security-opt", "no-new-privileges", "--user", "0:0",
	}
	args = slices.Concat(args, labels, noHostProxy(p.engine), []string{p.proxyImage})
	for _, rule := range rules {
		args = append(args, "-forward", rule)
	}
	b.forwarder = name
	if _, err := p.engine.run(ctx, args...); err != nil {
		return fmt.Errorf("starting the Service forwarder: %w", err)
	}
	return nil
}

// addresses are what the Step is told it can reach.
func addresses(services []runningService) []sandbox.Service {
	out := make([]sandbox.Service, 0, len(services))
	for _, s := range services {
		listed := sandbox.Service{Name: s.name}
		for _, port := range s.ports {
			for _, alias := range s.aliases {
				listed.Addresses = append(listed.Addresses, fmt.Sprintf("%s:%d", alias, port))
			}
			if slices.Contains(s.localhost, port) {
				listed.Addresses = append(listed.Addresses, fmt.Sprintf("localhost:%d", port))
			}
		}
		if len(s.ports) == 0 {
			listed.Addresses = slices.Clone(s.aliases)
		}
		out = append(out, listed)
	}
	return out
}

// downRecorded removes every Compose project on record in the runtime state.
func downRecorded(ctx context.Context, tool composeTool, stateDir string) error {
	records, err := filepath.Glob(filepath.Join(stateDir, "sandboxes", "*", stackFile))
	if err != nil {
		return err
	}
	var errs []error
	for _, record := range records {
		data, err := os.ReadFile(record) //nolint:gosec // the Factory's own runtime state
		if err != nil {
			errs = append(errs, err)
			continue
		}
		var stack composeStack
		if err := json.Unmarshal(data, &stack); err != nil {
			errs = append(errs, err)
			continue
		}
		errs = append(errs, tool.down(ctx, stack))
	}
	return errors.Join(errs...)
}
