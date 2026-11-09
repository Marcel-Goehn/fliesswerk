package sandbox

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// The files a Sandbox with `ca_certs` gets: the system list plus `ca_certs`,
// and `ca_certs` alone.
const (
	caBundleFile = "ca-bundle.pem"
	caCertsFile  = "ca-certs.pem"
)

// SystemCAFiles are where the distributions keep their list of trusted roots,
// the likeliest first.
var SystemCAFiles = []string{
	"/etc/ssl/certs/ca-certificates.crt", // Debian, Ubuntu, Alpine, Arch
	"/etc/pki/tls/certs/ca-bundle.crt",   // Fedora, RHEL
	"/etc/ssl/ca-bundle.pem",             // openSUSE
	"/var/lib/ca-certificates/ca-bundle.pem",
	"/etc/ssl/cert.pem", // macOS, and Alpine without ca-certificates
}

// caBundleVars are what most programs read a replacement CA list from, and
// which therefore must hold the system's roots as well. Node's own variable
// is added to its roots instead, so it gets `ca_certs` alone.
var caBundleVars = []string{"SSL_CERT_FILE", "REQUESTS_CA_BUNDLE", "AWS_CA_BUNDLE", "GIT_SSL_CAINFO", "CURL_CA_BUNDLE"}

const nodeCAVar = "NODE_EXTRA_CA_CERTS"

// CAEnv points a Sandbox's programs at the files Install wrote, which the
// Sandbox sees in dir.
func CAEnv(dir string) []string {
	env := make([]string, 0, len(caBundleVars)+1)
	for _, name := range caBundleVars {
		env = append(env, name+"="+dir+"/"+caBundleFile)
	}
	return append(env, nodeCAVar+"="+dir+"/"+caCertsFile)
}

// CACerts is what `[network] ca_certs` holds.
type CACerts struct {
	// Paths are the configured files, as absolute paths.
	Paths []string
	// PEM is every certificate in them. Nothing else in those files, a
	// private key least of all, gets into a Sandbox this way.
	PEM []byte
}

// Configured reports whether there are any.
func (c CACerts) Configured() bool { return len(c.Paths) > 0 }

// LoadCACerts reads the configured files. A path may be absolute, start with
// ~/ or be relative to the project root.
func LoadCACerts(root string, configured []string) (CACerts, error) {
	var ca CACerts
	for _, p := range configured {
		file, err := caPath(root, p)
		if err != nil {
			return CACerts{}, fmt.Errorf("[network] ca_certs: %q: %w", p, err)
		}
		certs, err := readCertificates(file)
		if err != nil {
			return CACerts{}, fmt.Errorf("[network] ca_certs: %q: %w", p, err)
		}
		ca.Paths, ca.PEM = append(ca.Paths, file), append(ca.PEM, certs...)
	}
	return ca, nil
}

// caPath is a configured path made absolute.
func caPath(root, p string) (string, error) {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, rest), nil
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	return filepath.Join(root, p), nil
}

// readCertificates is the certificates of a PEM file, re-encoded: what an
// export on Windows does to line endings stays out of the bundle.
func readCertificates(file string) ([]byte, error) {
	data, err := os.ReadFile(file) //nolint:gosec // a path the project's config names, for this
	if err != nil {
		return nil, errors.New("can't be read: " + underlying(err))
	}
	var out bytes.Buffer
	for {
		var block *pem.Block
		if block, data = pem.Decode(data); block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		if _, err := x509.ParseCertificate(block.Bytes); err != nil {
			return nil, fmt.Errorf("holds a certificate that can't be parsed: %w", err)
		}
		if err := pem.Encode(&out, block); err != nil {
			return nil, err
		}
	}
	if out.Len() == 0 {
		return nil, errors.New("holds no PEM certificate")
	}
	return out.Bytes(), nil
}

// underlying is what went wrong with a file, without its path: the caller
// names the file as the config does.
func underlying(err error) string {
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err.Error()
	}
	return err.Error()
}

// Install writes a Sandbox's two CA files into dir, readable by whichever
// user the Sandbox runs as.
func (c CACerts) Install(dir string, system []byte) error {
	bundle := append(bytes.TrimRight(bytes.Clone(system), "\n"), '\n')
	if err := os.WriteFile(filepath.Join(dir, caBundleFile), append(bundle, c.PEM...), 0o644); err != nil { //nolint:gosec // public certificates, for the Sandbox's user
		return err
	}
	return os.WriteFile(filepath.Join(dir, caCertsFile), c.PEM, 0o644) //nolint:gosec // public certificates, for the Sandbox's user
}
