package native

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/Marcel-Goehn/fliesswerk/internal/forward"
	"github.com/Marcel-Goehn/fliesswerk/internal/sandbox"
)

// insideServices holds a Sandbox's Service sockets, under bwrap.
const insideServices = insideDir + "/services"

// firstUnprivilegedPort is the lowest port a Sandbox's process can listen on.
// Capabilities don't help below it: AppArmor's unpriv_bwrap profile, on stock
// Ubuntu, denies them inside the namespace even when bwrap grants them.
const firstUnprivilegedPort = 1024

// loopback is the address Services are published on, and a Sandbox's own
// localhost. A literal one, since `localhost` may resolve to ::1 first, where
// nothing is published.
const loopback = "127.0.0.1"

// Services run a project's Services on ports published on the host's
// loopback, each Sandbox with its own copy.
type Services interface {
	// Connect finds what runs the Services.
	Connect(ctx context.Context) error
	// DownLeft removes what a crashed Run's Sandboxes left. It reads the
	// records in the Sandboxes' state.
	DownLeft(ctx context.Context) error
	// Validate refuses Services a Sandbox couldn't reach.
	Validate(ctx context.Context) error
	// Start brings up a Sandbox's Services, recording them in its state
	// directory. They are ready when it returns; stop removes them, and is
	// also returned with an error if anything may have started.
	Start(ctx context.Context, state string, req sandbox.Request) (running []sandbox.PublishedService, stop func(context.Context) error, err error)
	// StartShared brings up the Run's shared Services, as Start does.
	StartShared(ctx context.Context, state string, req sandbox.Request) (running []sandbox.PublishedService, stop func(context.Context) error, err error)
	// MemoryUse is what the own Services of the Sandbox with this state
	// directory hold.
	MemoryUse(ctx context.Context, state string) (int64, error)
}

// StartShared brings up the shared Services from the worktree in req, for
// every Sandbox of the Run, on record like a Sandbox's own. It returns what
// removes them, also with an error once anything may have started.
func (p *Provider) StartShared(ctx context.Context, req sandbox.Request) (func(context.Context) error, error) {
	state := filepath.Join(p.opts.StateDir, "sandboxes", req.Name)
	if err := os.MkdirAll(state, 0o700); err != nil {
		return nil, err
	}
	running, stopServices, err := p.opts.Services.StartShared(ctx, state, req)
	if stopServices == nil {
		return nil, err
	}
	p.shared = running
	return func(ctx context.Context) error {
		if err := stopServices(ctx); err != nil {
			return err // the record says what is still to remove
		}
		return forceRemove(state)
	}, err
}

// startServices brings up the Sandbox's own Services and opens the way to them
// and the Run's shared ones, by the means of the host's Sandbox.
func (p *Provider) startServices(ctx context.Context, b *box, s *sandboxPlan, req sandbox.Request) error {
	if p.opts.Services == nil || req.NoServices {
		return nil
	}
	p.log("Starting the Services of %s", req.Name)
	running, stop, err := p.opts.Services.Start(ctx, b.state, req)
	b.stopServices = stop
	if err != nil {
		return err
	}
	running = slices.SortedFunc(slices.Values(slices.Concat(running, p.shared)), func(x, y sandbox.PublishedService) int { return strings.Compare(x.Name, y.Name) })
	if p.host.Mac {
		p.publishToSeatbelt(b, s, running)
		return nil
	}
	return p.forwardToBwrap(ctx, b, s, running)
}

// publishToSeatbelt allows each published port. A Seatbelt Sandbox shares the
// host's loopback, so its programs use that port and no name.
func (p *Provider) publishToSeatbelt(b *box, s *sandboxPlan, running []sandbox.PublishedService) {
	for _, svc := range running {
		listed := sandbox.Service{Name: svc.Name}
		first := 0
		for _, port := range svc.Ports {
			if first == 0 {
				first = port.Host
			}
			s.ServicePorts = append(s.ServicePorts, port.Host)
			listed.Addresses = append(listed.Addresses, net.JoinHostPort(loopback, strconv.Itoa(port.Host)))
		}
		b.services = append(b.services, listed)
		b.setEnv(sandbox.ServiceEnv(svc.Name, loopback, first, p.opts.EnvTemplates[svc.Name]))
	}
}

// setEnv adds NAME=value entries to what every command gets.
func (b *box) setEnv(entries []string) {
	for _, kv := range entries {
		name, value, _ := strings.Cut(kv, "=")
		b.env[name] = value
	}
}

// forwardToBwrap gives each Service an address on the Sandbox's own loopback
// and a name for it in a generated hosts file, so that two can share a port.
// The first by name with a port also has it on localhost. A port a Sandbox
// can't listen on is at its published port instead, the same on every machine.
// Every port leads over a mounted socket to a forwarder here, which is the
// only thing that can reach the published port from the Sandbox's network
// namespace.
func (p *Provider) forwardToBwrap(ctx context.Context, b *box, s *sandboxPlan, running []sandbox.PublishedService) error {
	dir, err := os.MkdirTemp(p.socketDir, "s")
	if err != nil {
		return err
	}
	var rules []forward.Rule
	hosts := "127.0.0.1 localhost\n::1 localhost\n"
	noProxy := []string{b.env["NO_PROXY"]}
	localhost := map[int]string{}
	if port, err := strconv.Atoi(proxyPort); err == nil {
		localhost[port] = "the egress proxy"
	}
	for i, svc := range running {
		ip := fmt.Sprintf("127.0.1.%d", i+1)
		hosts += ip + " " + strings.Join(svc.Aliases, " ") + "\n"
		noProxy = append(noProxy, svc.Aliases...)
		listed := sandbox.Service{Name: svc.Name}
		first := 0
		for _, port := range svc.Ports {
			inside := port.Container
			if inside < firstUnprivilegedPort {
				inside = port.Host
				p.log("warning: a bwrap Sandbox can't listen on port %d; reach the Service %s at %s:%d", port.Container, svc.Name, svc.Name, inside)
			}
			if first == 0 {
				first = inside
			}
			number := strconv.Itoa(inside)
			for _, alias := range svc.Aliases {
				listed.Addresses = append(listed.Addresses, net.JoinHostPort(alias, number))
			}
			name := fmt.Sprintf("%d-%s.sock", i, number)
			forwarded := serviceForward{Socket: filepath.Join(dir, name), Inside: insideServices + "/" + name, Listen: []string{net.JoinHostPort(ip, number)}}
			if other, taken := localhost[inside]; taken {
				p.log("warning: localhost:%d stays with %s; reach %s as %s:%d", inside, other, svc.Name, svc.Name, inside)
			} else {
				localhost[inside] = "the Service " + svc.Name
				forwarded.Listen = append(forwarded.Listen, net.JoinHostPort(loopback, number))
				listed.Addresses = append(listed.Addresses, net.JoinHostPort("localhost", number))
			}
			s.Forwards = append(s.Forwards, forwarded)
			rules = append(rules, forward.Rule{Listen: forward.Unix(forwarded.Socket), Target: net.JoinHostPort(loopback, strconv.Itoa(port.Host))})
		}
		b.services = append(b.services, listed)
		b.setEnv(sandbox.ServiceEnv(svc.Name, svc.Name, first, p.opts.EnvTemplates[svc.Name]))
	}
	b.setEnv([]string{"NO_PROXY=" + strings.Join(noProxy, ","), "no_proxy=" + strings.Join(noProxy, ",")})
	s.Hosts = filepath.Join(b.state, "hosts")
	if err := os.WriteFile(s.Hosts, []byte(hosts), 0o600); err != nil {
		return err
	}
	// The forwarders last as long as the Sandbox, not the call that created it.
	forwarding, cancel := context.WithCancel(context.WithoutCancel(ctx))
	server, err := forward.Start(forwarding, rules)
	if err != nil {
		cancel()
		return fmt.Errorf("forwarding the Services: %w", err)
	}
	stopCompose := b.stopServices
	b.stopServices = func(ctx context.Context) error {
		cancel()
		server.Wait()
		return errors.Join(stopCompose(ctx), os.RemoveAll(dir))
	}
	return nil
}
