package container

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/proxyimage"
)

// sidecarPort is where the proxy listens in the sidecar.
const sidecarPort = "3128"

// closeTimeout bounds removing the sidecar at the end of a Run.
const closeTimeout = time.Minute

// proxyVars are the variables that point a program at a proxy.
var proxyVars = []string{"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy"}

// proxyEnv points a Sandbox's programs at the sidecar, by address: Podman's
// internal networks have no DNS. A Sandbox's own localhost stays direct.
func proxyEnv(ip string) []string {
	proxy := "http://" + net.JoinHostPort(ip, sidecarPort)
	direct := "localhost,127.0.0.1,::1"
	return []string{
		"HTTP_PROXY=" + proxy, "HTTPS_PROXY=" + proxy, "NO_PROXY=" + direct,
		"http_proxy=" + proxy, "https_proxy=" + proxy, "no_proxy=" + direct,
	}
}

// noHostProxy stops Podman copying the host's proxy variables, and any
// login in them, into a container it creates.
func noHostProxy(e Engine) []string {
	if e.Binary == podman {
		return []string{"--http-proxy=false"}
	}
	return nil
}

// proxyTag is the sidecar image's tag: the Factory's version. A dev build
// has no version that tells one proxy from another, so it uses the hash.
func proxyTag(version string, rootfs []byte) string {
	if version == "" || strings.HasPrefix(version, "dev") {
		sum := sha256.Sum256(rootfs)
		return "dev-" + hex.EncodeToString(sum[:])[:12]
	}
	return unsafeName.ReplaceAllString(version, "-")
}

// ensureProxyImage imports the sidecar's image unless this version has it
// already. It comes from the binary itself: there is no registry to pull
// from, and so no version that could drift from the Factory's.
func (p *Provider) ensureProxyImage(ctx context.Context) error {
	arch, err := proxyArch(ctx, p.engine)
	if err != nil {
		return err
	}
	rootfs, err := proxyimage.Rootfs(arch)
	if err != nil {
		return err
	}
	p.proxyImage = "kraftwerk-proxy:" + proxyTag(p.opts.Version, rootfs)
	if _, err := p.engine.run(ctx, "image", "inspect", p.proxyImage); err == nil {
		return nil
	}
	file, err := os.CreateTemp("", "kraftwerk-proxy-*.tar.gz")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(file.Name()) }()
	_, err = file.Write(rootfs)
	if cerr := file.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	p.log("Importing the proxy sidecar's image")
	if _, err := p.engine.run(ctx, "import", file.Name(), p.proxyImage); err != nil {
		return fmt.Errorf("importing the proxy sidecar's image: %w", err)
	}
	return nil
}

// startSidecar starts the Run's one proxy sidecar on a normal network of
// its own, the only way from the Run's Sandboxes to the outside.
func (p *Provider) startSidecar(ctx context.Context) error {
	if err := p.opts.Egress.Validate(); err != nil {
		return err
	}
	if err := p.ensureProxyImage(ctx); err != nil {
		return err
	}
	name := unsafeName.ReplaceAllString("kraftwerk-"+p.opts.RunID+"-egress", "-")
	p.sidecar = name
	if _, err := p.engine.run(ctx, append(append([]string{"network", "create"}, p.labels()...), name)...); err != nil {
		return fmt.Errorf("creating the proxy sidecar's network: %w", err)
	}
	if _, err := p.engine.run(ctx, p.sidecarArgs(name)...); err != nil {
		return fmt.Errorf("creating the proxy sidecar: %w", err)
	}
	if _, err := p.engine.run(ctx, "start", name); err != nil {
		return fmt.Errorf("starting the proxy sidecar: %w", err)
	}
	return nil
}

// sidecarArgs is the `create` call of the sidecar. The proxy needs nothing
// but its sockets. IP forwarding is off because the sidecar is on all of a
// Run's networks: a Sandbox whose user has CAP_NET_RAW could otherwise send
// it frames to be routed to the internet or to another Sandbox.
func (p *Provider) sidecarArgs(name string) []string {
	args := []string{
		"create", "--name", name, "--network", name, "--entrypoint", proxyimage.Entrypoint,
		"--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--user", "65534:65534",
		"--sysctl", "net.ipv4.ip_forward=0",
	}
	args = append(args, p.labels()...)
	args = append(args, noHostProxy(p.engine)...)
	// An upstream_proxy replaces these, and Egress.Validate then hasn't
	// looked at them. Otherwise it has made sure none holds a login.
	if p.opts.Egress.UpstreamProxy == "" {
		for _, variable := range proxyVars {
			if value, ok := os.LookupEnv(variable); ok {
				args = append(args, "--env", variable+"="+value)
			}
		}
	}
	args = append(args, p.proxyImage, "-listen", ":"+sidecarPort)
	for _, host := range slices.Concat(p.opts.Egress.Allow, p.opts.Egress.Endpoints) {
		args = append(args, "-allow", host)
	}
	if upstream := p.opts.Egress.UpstreamProxy; upstream != "" {
		args = append(args, "-upstream", upstream)
	}
	return args
}

// attachSidecar connects the sidecar to a Sandbox's network and returns its
// address there.
func (p *Provider) attachSidecar(ctx context.Context, network string) (string, error) {
	if _, err := p.engine.run(ctx, "network", "connect", network, p.sidecar); err != nil {
		return "", fmt.Errorf("connecting the proxy sidecar: %w", err)
	}
	format := `{{(index .NetworkSettings.Networks "` + network + `").IPAddress}}`
	out, err := p.engine.run(ctx, "container", "inspect", "--format", format, p.sidecar)
	if err != nil {
		return "", fmt.Errorf("finding the proxy sidecar: %w", err)
	}
	ip := strings.TrimSpace(out)
	if ip == "" {
		return "", fmt.Errorf("the proxy sidecar has no address on %s; is it still running? (`%s logs %s`)", network, p.engine.Binary, p.sidecar)
	}
	return ip, nil
}

// Close removes the Run's sidecar and its network, after passing on which
// hosts it denied. It can be called when no sidecar was started.
func (p *Provider) Close() {
	if p.sidecar == "" {
		return
	}
	name := p.sidecar
	p.sidecar = ""
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	warn := func(err error) { p.log("warning: removing the proxy sidecar: %v", err) }
	if out, err := p.engine.run(ctx, "logs", name); err == nil {
		lines := strings.Split(strings.TrimSpace(out), "\n")
		slices.Sort(lines)
		for _, line := range slices.Compact(lines) {
			if line != "" {
				p.log("%s", line)
			}
		}
	}
	if err := p.engine.remove(ctx, name); err != nil {
		warn(err)
	}
	if _, err := p.engine.run(ctx, "network", "rm", name); err != nil {
		warn(err)
	}
}
