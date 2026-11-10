package sandbox_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Marcel-Goehn/fliesswerk/internal/sandbox"
)

// selfSigned is a root certificate called name, in PEM.
func selfSigned(t *testing.T, name string) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func writeCA(t *testing.T, dir, name, content string) string {
	t.Helper()
	file := filepath.Join(dir, name)
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestNoCACertsMeansNothingIsSet(t *testing.T) {
	ca, err := sandbox.LoadCACerts(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if ca.Configured() {
		t.Errorf("an empty ca_certs counts as set: %+v", ca)
	}
}

func TestCACertsAreFoundFromAbsoluteHomeAndProjectPaths(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	abs := writeCA(t, t.TempDir(), "abs.pem", selfSigned(t, "Abs"))
	inHome := writeCA(t, home, "home.pem", selfSigned(t, "Home"))
	inRoot := writeCA(t, root, "root.pem", selfSigned(t, "Root"))

	ca, err := sandbox.LoadCACerts(root, []string{abs, "~/home.pem", "root.pem"})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{abs, inHome, inRoot}; !slices.Equal(ca.Paths, want) {
		t.Errorf("Paths = %q, want %q", ca.Paths, want)
	}
	if n := strings.Count(string(ca.PEM), "BEGIN CERTIFICATE"); n != 3 {
		t.Errorf("PEM holds %d certificates, want 3", n)
	}
}

func TestACACertsFileThatCannotBeUsedStopsAndIsNamed(t *testing.T) {
	root := t.TempDir()
	garbled := "-----BEGIN CERTIFICATE-----\nbm90IGEgY2VydGlmaWNhdGU=\n-----END CERTIFICATE-----\n"
	for name, content := range map[string]string{"text.pem": "not a certificate\n", "garbled.pem": garbled} {
		writeCA(t, root, name, content)
	}
	for _, file := range []string{"missing.pem", "text.pem", "garbled.pem"} {
		_, err := sandbox.LoadCACerts(root, []string{file})
		if err == nil || !strings.Contains(err.Error(), "ca_certs") || !strings.Contains(err.Error(), file) {
			t.Errorf("%s: err = %v, want one naming ca_certs and the file", file, err)
		}
	}
}

func TestAPrivateKeyInACACertsFileStaysOutOfTheBundle(t *testing.T) {
	root := t.TempDir()
	key := "-----BEGIN PRIVATE KEY-----\nc2VjcmV0\n-----END PRIVATE KEY-----\n"
	writeCA(t, root, "both.pem", key+selfSigned(t, "Corp"))

	ca, err := sandbox.LoadCACerts(root, []string{"both.pem"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(ca.PEM), "PRIVATE KEY") {
		t.Errorf("the key is in the bundle:\n%s", ca.PEM)
	}
	if !strings.Contains(string(ca.PEM), "BEGIN CERTIFICATE") {
		t.Errorf("the certificate is not in the bundle:\n%s", ca.PEM)
	}
}

func TestInstallWritesTheSystemListPlusCACertsAndCACertsAlone(t *testing.T) {
	root := t.TempDir()
	corp := selfSigned(t, "Corp")
	writeCA(t, root, "corp.pem", corp)
	ca, err := sandbox.LoadCACerts(root, []string{"corp.pem"})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	// A system list need not end in a newline.
	if err := ca.Install(dir, []byte("SYSTEM LIST")); err != nil {
		t.Fatal(err)
	}

	got := map[string]string{}
	for _, kv := range sandbox.CAEnv("/inside") {
		name, value, _ := strings.Cut(kv, "=")
		got[name] = value
	}
	const combined, alone = "/inside/ca-bundle.pem", "/inside/ca-certs.pem"
	want := map[string]string{
		"SSL_CERT_FILE": combined, "REQUESTS_CA_BUNDLE": combined, "AWS_CA_BUNDLE": combined,
		"GIT_SSL_CAINFO": combined, "CURL_CA_BUNDLE": combined, "NODE_EXTRA_CA_CERTS": alone,
	}
	if len(got) != len(want) {
		t.Errorf("CAEnv = %q, want %q", got, want)
	}
	for name, value := range want {
		if got[name] != value {
			t.Errorf("%s = %q, want %q", name, got[name], value)
		}
	}

	bundle, err := os.ReadFile(filepath.Join(dir, "ca-bundle.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "SYSTEM LIST\n" + corp; string(bundle) != want {
		t.Errorf("the combined file = %q, want %q", bundle, want)
	}
	only, err := os.ReadFile(filepath.Join(dir, "ca-certs.pem"))
	if err != nil {
		t.Fatal(err)
	}
	if string(only) != corp {
		t.Errorf("ca-certs.pem = %q, want %q", only, corp)
	}
}
