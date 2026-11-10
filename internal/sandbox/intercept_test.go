package sandbox_test

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/kraftwerk/internal/sandbox"
)

// exitingSandbox runs every command as a shell that exits with code.
type exitingSandbox struct {
	sandbox.Sandbox
	code int
}

func (s exitingSandbox) Exec(_, _ []string) (hostArgv, hostEnv []string) {
	return []string{"/bin/sh", "-c", "echo said-by-curl >&2; exit " + strconv.Itoa(s.code)}, os.Environ()
}

func parse(t *testing.T, certPEM string) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode([]byte(certPEM))
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func needShell(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake Sandbox is a shell")
	}
}

func hostVerifies(chain ...*x509.Certificate) sandbox.HostChain {
	return func(context.Context, string) ([]*x509.Certificate, error) { return chain, nil }
}

func TestAnUnknownCertificateTheHostVerifiesNamesTheRootAndTheFix(t *testing.T) {
	needShell(t)
	root := parse(t, selfSigned(t, "Zscaler Root CA"))

	warning, err := sandbox.CheckInterception(context.Background(), exitingSandbox{code: 60}, "api.example.com", hostVerifies(parse(t, selfSigned(t, "leaf")), root))

	if err == nil {
		t.Fatalf("no error (warning %q)", warning)
	}
	for _, want := range []string{"api.example.com", "Zscaler Root CA", "ca_certs", "[network]"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error doesn't say %q: %v", want, err)
		}
	}
}

func TestAChainNotEndingInARootFallsBackToTheExportedPEM(t *testing.T) {
	needShell(t)
	// Issued by someone else, so it can't be the anchor.
	issued := parse(t, selfSigned(t, "Intermediate"))
	issued.RawIssuer = []byte("someone else")

	_, err := sandbox.CheckInterception(context.Background(), exitingSandbox{code: 60}, "api.example.com", hostVerifies(issued))

	if err == nil || !strings.Contains(err.Error(), "ca_certs") || !strings.Contains(err.Error(), "Export") {
		t.Fatalf("err = %v, want the ca_certs fix telling to export the root", err)
	}
	if strings.Contains(err.Error(), "Intermediate") {
		t.Errorf("an intermediate is named as the root: %v", err)
	}
}

func TestAnUnknownCertificateTheHostRefusesTooIsOnlyAWarning(t *testing.T) {
	needShell(t)
	refuses := func(context.Context, string) ([]*x509.Certificate, error) {
		return nil, errors.New("x509: unknown authority")
	}

	warning, err := sandbox.CheckInterception(context.Background(), exitingSandbox{code: 60}, "api.example.com", refuses)

	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(warning, "api.example.com") {
		t.Errorf("warning = %q", warning)
	}
}

func TestProbeOutcomesThatAreNotInterception(t *testing.T) {
	needShell(t)
	tests := []struct {
		name string
		code int
		want string // in the warning, "" for none
	}{
		{"the Sandbox trusts it", 0, ""},
		{"no curl in the Sandbox", 127, "curl"},
		{"the host is out of reach", 7, "said-by-curl"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			host := func(context.Context, string) ([]*x509.Certificate, error) {
				t.Error("the host was asked")
				return nil, nil
			}
			warning, err := sandbox.CheckInterception(context.Background(), exitingSandbox{code: tt.code}, "api.example.com", host)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if tt.want == "" && warning != "" || !strings.Contains(warning, tt.want) {
				t.Errorf("warning = %q, want it to hold %q", warning, tt.want)
			}
		})
	}
}

func TestVerifiedChainIsWhatTheHostVerifiedAgainst(t *testing.T) {
	srv := httptest.NewTLSServer(http.NotFoundHandler()) // any answer will do
	t.Cleanup(srv.Close)
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())

	chain, err := sandbox.VerifiedChain(nil, roots)(context.Background(), strings.TrimPrefix(srv.URL, "https://"))
	if err != nil {
		t.Fatal(err)
	}
	if last := chain[len(chain)-1]; !last.Equal(srv.Certificate()) {
		t.Errorf("the chain ends in %s", last.Subject)
	}

	if _, err := sandbox.VerifiedChain(nil, x509.NewCertPool())(context.Background(), strings.TrimPrefix(srv.URL, "https://")); err == nil {
		t.Error("a host the roots don't cover was verified")
	}
}

// TestThePlatformVerifierReturnsTheAnchor settles, on the machines CI has,
// what the Go source can't: that macOS and Windows put the root in the chain.
func TestThePlatformVerifierReturnsTheAnchor(t *testing.T) {
	if os.Getenv("KRAFTWERK_TEST_ANCHOR") == "" {
		t.Skip("needs the network; CI sets KRAFTWERK_TEST_ANCHOR")
	}
	chain, err := sandbox.VerifiedChain(nil, nil)(context.Background(), "github.com")
	if err != nil {
		t.Fatal(err)
	}
	if !sandbox.IsRoot(chain[len(chain)-1]) {
		t.Errorf("the chain ends in %s, issued by %s: not a root", chain[len(chain)-1].Subject, chain[len(chain)-1].Issuer)
	}
}
