//go:build integration && linux

package main

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Marcel-Goehn/kraftwerk/internal/sandbox/catest"
	"github.com/Marcel-Goehn/kraftwerk/internal/sandbox/native"
)

// behindTheInterceptor is a project with a native Sandbox and a gateway that
// can only be reached through an intercepting proxy, and the env of a machine
// that trusts the proxy's root: only the Sandbox has to be told. Go reads
// SSL_CERT_FILE on Linux only, hence the build tag.
func behindTheInterceptor(t *testing.T, bin string, ca *catest.CA, extraConfig string) (root string, env []string) {
	t.Helper()
	_, err := native.Check(context.Background())
	var blocked *native.BlockedError
	if errors.As(err, &blocked) {
		t.Skipf("bwrap is blocked here: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	go func() { _ = ca.Intercept(l) }()

	caFile := filepath.Join(t.TempDir(), "root.pem")
	if err := os.WriteFile(caFile, ca.CertPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	local := gatewayConfig("https://intercepted.example") + "[sandbox]\nkind = \"native\"\n" + strings.ReplaceAll(extraConfig, "CA_FILE", caFile)
	root = projectDir(t, map[string]string{"config.toml": "version = 1\n", "config.local.toml": local})

	home := configHome(t)
	if _, stderr, code := authSet(t, bin, home, "gw", "good-token"); code != 0 {
		t.Fatalf("auth set: %s", stderr)
	}
	env = append(ghEnv(t, "ok"), home...)
	path := ""
	for _, kv := range env {
		if p, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = p
		}
	}
	for _, tool := range []string{"bwrap", "curl"} {
		found, err := exec.LookPath(tool)
		if err != nil {
			t.Skipf("no %s here", tool)
		}
		path += string(os.PathListSeparator) + filepath.Dir(found)
	}
	return root, append(env, "PATH="+path, "SSL_CERT_FILE="+caFile, "HTTPS_PROXY=http://"+l.Addr().String(), "NO_PROXY=")
}

func TestNativeCheckNamesTheRootBehindAnInterceptingProxy(t *testing.T) {
	ca, err := catest.NewCA("Kraftwerk Test Root")
	if err != nil {
		t.Fatal(err)
	}
	bin := buildKraftwerk(t, "")
	root, env := behindTheInterceptor(t, bin, ca, "")

	_, stderr, code := runEnv(t, bin, root, env, "check")

	if code != exitConfigError {
		t.Fatalf("exit code = %d, want %d; stderr: %s", code, exitConfigError, stderr)
	}
	for _, want := range []string{"intercepted.example", "Kraftwerk Test Root", "ca_certs"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr doesn't say %q: %s", want, stderr)
		}
	}
}

func TestNativeCheckPassesBehindAnInterceptingProxyWithCACerts(t *testing.T) {
	ca, err := catest.NewCA("Kraftwerk Test Root")
	if err != nil {
		t.Fatal(err)
	}
	bin := buildKraftwerk(t, "")
	root, env := behindTheInterceptor(t, bin, ca, "[network]\nca_certs = [\"CA_FILE\"]\n")

	stdout, stderr, code := runEnv(t, bin, root, env, "check")

	if code != exitOK || !strings.Contains(stdout, "a Sandbox trusts intercepted.example") {
		t.Fatalf("exit code = %d, stdout: %s, stderr: %s", code, stdout, stderr)
	}
	if entries, err := os.ReadDir(filepath.Join(root, ".kraftwerk", "state", "probe")); err == nil {
		t.Errorf("the probe's worktree is still there: %v", entries)
	}
}
