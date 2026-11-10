//go:build linux

package main

import (
	"bytes"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/fliesswerk/internal/sandbox/catest"
)

const interceptedGateway = "https://intercepted.example"

// interceptedEnv is initEnv on a machine that trusts ca, behind a proxy that
// inspects TLS with it. Go reads SSL_CERT_FILE on Linux only, hence the build
// tag.
func (p *runProject) interceptedEnv(t *testing.T, ca *catest.CA) []string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() { _ = ca.Intercept(l) }()
	caFile := filepath.Join(t.TempDir(), "root.pem")
	writeFile(t, caFile, string(ca.CertPEM))
	return append(p.initEnv(t), "SSL_CERT_FILE="+caFile, "HTTPS_PROXY=http://"+l.Addr().String(), "NO_PROXY=", "no_proxy=")
}

func testCA(t *testing.T, name string) *catest.CA {
	t.Helper()
	ca, err := catest.NewCA(name)
	if err != nil {
		t.Fatal(err)
	}
	return ca
}

func TestInitOffersToExportAnInterceptingRoot(t *testing.T) {
	p := teamProject(t, map[string]string{"config.toml": teamConfig})
	ca := testCA(t, "Fliesswerk Test Root")

	p.init(t, p.interceptedEnv(t, ca), "gateway", interceptedGateway, "", gatewaySecret, "", gitHubSecret, "")

	mustContain(t, "stdout", p.stdout, "intercepted.example", "Fliesswerk Test Root")
	certs := stringsOf(valueAt(p.localConfig(t), "network", "ca_certs"))
	if len(certs) != 1 || !strings.HasPrefix(certs[0], filepath.Join(p.home, ".config", "fliesswerk")+string(filepath.Separator)) {
		t.Fatalf("ca_certs = %v", certs)
	}
	data, err := os.ReadFile(certs[0])
	if err != nil {
		t.Fatal(err)
	}
	got, _ := pem.Decode(data)
	want, _ := pem.Decode(ca.CertPEM)
	if got == nil || !bytes.Equal(got.Bytes, want.Bytes) {
		t.Errorf("exported %q", data)
	}
	if got := p.fliesswerkFile(t, "config.toml"); got != teamConfig {
		t.Errorf("config.toml changed: %q", got)
	}
}

func TestInitDecliningTheRootExportsNothing(t *testing.T) {
	p := teamProject(t, map[string]string{"config.toml": teamConfig})

	p.init(t, p.interceptedEnv(t, testCA(t, "Fliesswerk Test Root")), "gateway", interceptedGateway, "", gatewaySecret, "", gitHubSecret, "n")

	if got := valueAt(p.localConfig(t), "network", "ca_certs"); got != nil {
		t.Errorf("ca_certs = %v", got)
	}
	matches, _ := filepath.Glob(filepath.Join(p.home, ".config", "fliesswerk", "*.pem"))
	if len(matches) != 0 {
		t.Errorf("exported %v", matches)
	}
}

// A list in config.local.toml replaces the team's, so the team's must stay on it.
func TestInitExportedRootJoinsTheTeamsCACerts(t *testing.T) {
	other := testCA(t, "Team Root")
	p := teamProject(t, map[string]string{
		"config.toml": teamConfig + "\n[network]\nca_certs = [\"team.pem\"]\n",
	})
	writeFile(t, filepath.Join(p.work, "team.pem"), string(other.CertPEM))

	p.init(t, p.interceptedEnv(t, testCA(t, "Fliesswerk Test Root")), "gateway", interceptedGateway, "", gatewaySecret, "", gitHubSecret, "")

	certs := stringsOf(valueAt(p.localConfig(t), "network", "ca_certs"))
	if len(certs) != 2 || !slices.Contains(certs, "team.pem") {
		t.Errorf("ca_certs = %v", certs)
	}
}

func TestInitOffersNothingWhenTheTeamAlreadyTrustsTheRoot(t *testing.T) {
	ca := testCA(t, "Fliesswerk Test Root")
	p := teamProject(t, map[string]string{
		"config.toml": teamConfig + "\n[network]\nca_certs = [\"team.pem\"]\n",
	})
	writeFile(t, filepath.Join(p.work, "team.pem"), string(ca.CertPEM))

	p.init(t, p.interceptedEnv(t, ca), "gateway", interceptedGateway, "", gatewaySecret, "", gitHubSecret)

	if strings.Contains(p.stdout, "Fliesswerk Test Root") {
		t.Errorf("offered a root the team trusts:\n%s", p.stdout)
	}
	if got := valueAt(p.localConfig(t), "network", "ca_certs"); got != nil {
		t.Errorf("ca_certs = %v", got)
	}
}
