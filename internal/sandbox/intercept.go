package sandbox

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"
)

// What curl exits with.
const (
	curlUnknownCertificate = 60
	shellNoSuchCommand     = 127
)

// probeTimeout bounds asking the host, as curl's --max-time bounds the
// request from inside the Sandbox.
const probeTimeout = 30 * time.Second

// HostChain is the certificate chain the host verifies host's certificate
// against, the leaf first.
type HostChain func(ctx context.Context, host string) ([]*x509.Certificate, error)

// VerifiedChain asks the host's own verifier, through route (nil for a direct
// connection), and against roots (nil for the system's). Go hands the
// verifying to the OS on macOS and Windows, whose answer holds the root too.
func VerifiedChain(route func(*url.URL) (*url.URL, error), roots *x509.CertPool) HostChain {
	return func(ctx context.Context, host string) ([]*x509.Certificate, error) {
		ctx, cancel := context.WithTimeout(ctx, probeTimeout)
		defer cancel()
		transport := &http.Transport{DisableKeepAlives: true, TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
		if route != nil {
			transport.Proxy = func(r *http.Request) (*url.URL, error) { return route(r.URL) }
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodHead, "https://"+host+"/", http.NoBody)
		if err != nil {
			return nil, err
		}
		resp, err := transport.RoundTrip(req)
		if err != nil {
			return nil, err
		}
		_ = resp.Body.Close()
		if resp.TLS == nil || len(resp.TLS.VerifiedChains) == 0 || len(resp.TLS.VerifiedChains[0]) == 0 {
			return nil, errors.New("the connection has no verified chain")
		}
		return resp.TLS.VerifiedChains[0], nil
	}
}

// IsRoot reports whether cert is its own issuer.
func IsRoot(cert *x509.Certificate) bool { return bytes.Equal(cert.RawIssuer, cert.RawSubject) }

// CheckInterception makes one request from inside s, through its proxy, to
// host. If that fails on an unknown certificate while the host succeeds,
// something on the way inspects TLS, and err names the root and the fix.
// Anything else that goes wrong says nothing about interception and is left to
// a warning, "" for none: the request is only a question about trust.
func CheckInterception(ctx context.Context, s Sandbox, host string, chain HostChain) (warning string, err error) {
	line := fmt.Sprintf("curl --silent --show-error --output /dev/null --max-time %d %s",
		int(probeTimeout.Seconds()), shellQuote("https://"+host+"/"))
	out, runErr := Shell(ctx, s, "", line, nil).CombinedOutput()
	if runErr == nil {
		return "", nil
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	var exit *exec.ExitError
	code := -1
	if errors.As(runErr, &exit) {
		code = exit.ExitCode()
	}
	said := strings.TrimSpace(string(out))
	switch code {
	case curlUnknownCertificate:
	case shellNoSuchCommand:
		return "the Sandbox has no curl, so whether it trusts " + host + " wasn't checked", nil
	default:
		return fmt.Sprintf("the Sandbox couldn't reach %s to check that it trusts it: %s", host, said), nil
	}
	verified, err := chain(ctx, host)
	if err != nil {
		return fmt.Sprintf("the Sandbox doesn't trust the certificate of %s, and this machine couldn't verify it either: %v", host, err), nil
	}
	return "", interceptionError(host, verified)
}

// interceptionError names the root that verified's chain ends in and the fix,
// or says to export the root if the chain stops short of it.
func interceptionError(host string, verified []*x509.Certificate) error {
	const fix = "  [network]\n  ca_certs = [\"/path/to/corporate-root.pem\"]"
	lead := fmt.Sprintf("the Sandbox doesn't trust the certificate of %s, but this machine does: something on the way, such as a corporate proxy, is inspecting TLS.\n", host)
	root := verified[len(verified)-1]
	if !IsRoot(root) {
		return fmt.Errorf("%sThis machine didn't say which root it trusts. Export your organisation's root certificate from its trust store as a PEM file and add it to .fliesswerk/config.toml:\n\n%s", lead, fix)
	}
	sum := sha256.Sum256(root.Raw)
	return fmt.Errorf("%sThis machine verified it against the root %q (SHA-256 %x). Export that root as a PEM file and add it to .fliesswerk/config.toml:\n\n%s", lead, root.Subject.String(), sum, fix)
}
