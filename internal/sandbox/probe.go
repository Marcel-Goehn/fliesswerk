package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
)

// outsideAddress is a public address that is on nobody's allowlist, and an
// address so that a Sandbox with no name resolution can still try it.
const outsideAddress = "1.1.1.1"

// connectSeconds bounds one attempt at a connection from inside a Sandbox.
const connectSeconds = 5

// NetworkReport is what ProbeNetwork found, in plain language.
type NetworkReport struct {
	Passed []string
	// Warnings are what it couldn't find out.
	Warnings []string
	// Problems each stop a Run from being safe or useful, and name the fix.
	Problems []string
}

// errNoCurl says the Sandbox has nothing to ask with.
var errNoCurl = errors.New("no curl")

// ProbeNetwork proves from inside s what its network is meant to be: every
// Service reachable, direct connections blocked and allowed, a host on the
// allowlist, reachable through the egress proxy ("" for none to try). A
// Sandbox that can't be asked is a problem: nothing about it is proven.
func ProbeNetwork(ctx context.Context, s Sandbox, allowed string) (NetworkReport, error) {
	var r NetworkReport
	err := r.probe(ctx, s, allowed)
	if errors.Is(err, errNoCurl) {
		r.Problems = append(r.Problems, "the Sandbox has no curl, so nothing about its network could be proven; install curl in it (for a container Sandbox, in .kraftwerk/Containerfile)")
		return r, nil
	}
	return r, err
}

func (r *NetworkReport) probe(ctx context.Context, s Sandbox, allowed string) error {
	for _, service := range s.Services() {
		if err := r.probeService(ctx, s, service); err != nil {
			return err
		}
	}
	reached, err := connects(ctx, s, "http://"+outsideAddress+"/", "")
	if err != nil {
		return err
	}
	if reached {
		r.Problems = append(r.Problems, fmt.Sprintf("a command in the Sandbox reached %s without going through the egress proxy, so the Sandbox isn't isolated; check `sandbox.run_args` and your engine's network settings, and don't start a Run until this is gone", outsideAddress))
	} else {
		r.Passed = append(r.Passed, "direct connections are blocked")
	}
	if allowed == "" {
		r.Warnings = append(r.Warnings, "no host is on the allowlist, so reaching one through the egress proxy wasn't checked")
		return nil
	}
	return r.probeAllowed(ctx, s, allowed)
}

// probeService adds whether any address of service answers: an image may
// expose ports nothing listens on.
func (r *NetworkReport) probeService(ctx context.Context, s Sandbox, service Service) error {
	var tried []string
	for _, address := range service.Addresses {
		if _, _, err := net.SplitHostPort(address); err != nil {
			continue // a name with no port says nothing to connect to
		}
		reached, err := connects(ctx, s, "http://"+address+"/", "--retry 3 --retry-delay 1 --retry-connrefused")
		if err != nil {
			return err
		}
		if reached {
			r.Passed = append(r.Passed, fmt.Sprintf("the Service %s is reachable at %s", service.Name, address))
			return nil
		}
		tried = append(tried, address)
	}
	if len(tried) == 0 {
		r.Warnings = append(r.Warnings, fmt.Sprintf("the Service %s publishes no port, so whether the Sandbox reaches it wasn't checked", service.Name))
		return nil
	}
	r.Problems = append(r.Problems, fmt.Sprintf("the Sandbox can't reach the Service %s (tried %s); check that it listens on those ports once it is up, and give it a healthcheck if it takes a while to start", service.Name, strings.Join(tried, ", ")))
	return nil
}

// connects reports whether a TCP connection to url was made, whatever spoke
// on it: a database's answer to HTTP is no answer at all to curl.
func connects(ctx context.Context, s Sandbox, url, options string) (bool, error) {
	written, err := curl(ctx, s, fmt.Sprintf("--noproxy '*' --max-time %d %s --write-out '%%{time_connect}' %s", connectSeconds, options, shellQuote(url)))
	if err != nil {
		return false, err
	}
	seconds, err := strconv.ParseFloat(written, 64)
	if err != nil {
		return false, fmt.Errorf("curl in the Sandbox printed %q where it should say how long connecting took", written)
	}
	return seconds > 0, nil
}

// probeAllowed adds whether the egress proxy opens a tunnel to host. A
// certificate the Sandbox then refuses is CheckInterception's to explain.
func (r *NetworkReport) probeAllowed(ctx context.Context, s Sandbox, host string) error {
	code, err := curl(ctx, s, fmt.Sprintf("--max-time %d --write-out '%%{http_connect}' %s", int(probeTimeout.Seconds()), shellQuote("https://"+host+"/")))
	if err != nil {
		return err
	}
	switch code {
	case "200":
		r.Passed = append(r.Passed, host+" is reachable through the egress proxy")
	case "403":
		r.Problems = append(r.Problems, fmt.Sprintf("the egress proxy refused %s, though it should be allowed; check `allow` in [sandbox.network]", host))
	case "000":
		r.Problems = append(r.Problems, fmt.Sprintf("the Sandbox couldn't get to the egress proxy to reach %s; check `sandbox.run_args` and your engine's network settings", host))
	default:
		r.Problems = append(r.Problems, fmt.Sprintf("the egress proxy couldn't reach %s (HTTP %s); check this machine's network, and `upstream_proxy` in [network] if it has a corporate proxy", host, code))
	}
	return nil
}

// curl runs curl in s, discarding the body, and returns what --write-out
// printed. A curl that fails has still written that; one that printed nothing
// never ran, and proves nothing about the network.
func curl(ctx context.Context, s Sandbox, options string) (string, error) {
	out, runErr := Shell(ctx, s, "", "curl --silent --output /dev/null "+options, nil).Output()
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(runErr, &exit) && exit.ExitCode() == shellNoSuchCommand {
		return "", errNoCurl
	}
	written := strings.TrimSpace(string(out))
	if written != "" {
		return written, nil
	}
	if runErr == nil {
		runErr = errors.New("it printed nothing")
	}
	if exit != nil && len(exit.Stderr) > 0 {
		runErr = fmt.Errorf("%w: %s", runErr, strings.TrimSpace(string(exit.Stderr)))
	}
	return "", fmt.Errorf("the Sandbox couldn't run curl, so nothing about its network could be proven: %w", runErr)
}

// shellQuote is s as one word of a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
