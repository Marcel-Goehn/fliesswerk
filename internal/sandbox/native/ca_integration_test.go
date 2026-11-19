//go:build integration && (linux || darwin)

package native

import (
	"context"
	"crypto/x509"
	"net"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/fliesswerk/internal/egress"
	"github.com/Marcel-Goehn/fliesswerk/internal/sandbox"
	"github.com/Marcel-Goehn/fliesswerk/internal/sandbox/catest"
)

func newCA(t *testing.T) (*catest.CA, string) {
	t.Helper()
	ca, err := catest.NewCA("Fliesswerk Test Root")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "corp.pem")
	writeFile(t, file, string(ca.CertPEM))
	return ca, file
}

func TestCACertsReachTheSandboxsProgramsOnTopOfTheSystemList(t *testing.T) {
	ca, file := newCA(t)
	pr := newProject(t, nil, func(o *Options) { o.CACerts = []string{file} })
	sb := pr.open(t)

	bundle := mustSh(t, sb, `cat "$SSL_CERT_FILE"`)
	if !strings.Contains(bundle, strings.TrimSpace(string(ca.CertPEM))) {
		t.Error("the combined file doesn't hold ca_certs")
	}
	if strings.Count(bundle, "BEGIN CERTIFICATE") < 2 {
		t.Error("the combined file doesn't hold the system list")
	}
	for _, name := range []string{"REQUESTS_CA_BUNDLE", "AWS_CA_BUNDLE", "GIT_SSL_CAINFO", "CURL_CA_BUNDLE"} {
		if out := mustSh(t, sb, `cmp "$`+name+`" "$SSL_CERT_FILE" && echo same`); out != "same" {
			t.Errorf("%s isn't the combined file: %s", name, out)
		}
	}
	if got := mustSh(t, sb, `cat "$NODE_EXTRA_CA_CERTS"`); got != strings.TrimSpace(string(ca.CertPEM)) {
		t.Errorf("NODE_EXTRA_CA_CERTS holds more than ca_certs: %s", got)
	}
	if got := mustSh(t, sb, "cat "+file); got != strings.TrimSpace(string(ca.CertPEM)) {
		t.Errorf("the ca_certs file reads %q", got)
	}
	for _, target := range []string{`"$SSL_CERT_FILE"`, file} {
		if out, ok := sh(sb, "echo evil >> "+target); ok {
			t.Errorf("%s can be changed: %s", target, out)
		}
	}
}

func TestWithoutCACertsNoCAVariableIsSet(t *testing.T) {
	sb := newProject(t, nil, nil).open(t)
	if out := mustSh(t, sb, `echo "[$SSL_CERT_FILE$REQUESTS_CA_BUNDLE$AWS_CA_BUNDLE$GIT_SSL_CAINFO$CURL_CA_BUNDLE$NODE_EXTRA_CA_CERTS]"`); out != "[]" {
		t.Errorf("set: %s", out)
	}
}

const interceptedHost = "intercepted.example"

// behindInterceptor is a Sandbox whose way out leads through an intercepting
// proxy on the host.
func behindInterceptor(t *testing.T, ca *catest.CA, caCerts ...string) sandbox.Sandbox {
	t.Helper()
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("no curl here")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() { _ = ca.Intercept(l) }()
	pr := newProject(t, nil, func(o *Options) {
		o.CACerts = caCerts
		o.Egress = egress.Options{Allow: []string{interceptedHost}, UpstreamProxy: "http://" + l.Addr().String()}
	})
	return pr.open(t)
}

func hostTrusts(ca *catest.CA) sandbox.HostChain {
	return func(context.Context, string) ([]*x509.Certificate, error) {
		return []*x509.Certificate{ca.Cert}, nil
	}
}

func TestInterceptionIsDetectedAndTheRootAndFixNamed(t *testing.T) {
	ca, _ := newCA(t)
	sb := behindInterceptor(t, ca)

	warning, err := sandbox.CheckInterception(context.Background(), sb, interceptedHost, hostTrusts(ca))

	if err == nil {
		t.Fatalf("not detected (warning %q)", warning)
	}
	for _, want := range []string{"Fliesswerk Test Root", "ca_certs"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error doesn't say %q: %v", want, err)
		}
	}
}

func TestWithCACertsTheSandboxTrustsTheInterceptingProxy(t *testing.T) {
	ca, file := newCA(t)
	sb := behindInterceptor(t, ca, file)

	if out := mustSh(t, sb, "curl -sS --max-time 10 https://"+interceptedHost+"/"); out != catest.Answer {
		t.Errorf("curl = %q", out)
	}
	if warning, err := sandbox.CheckInterception(context.Background(), sb, interceptedHost, hostTrusts(ca)); err != nil || warning != "" {
		t.Errorf("warning %q, err %v", warning, err)
	}
}
