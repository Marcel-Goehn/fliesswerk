//go:build integration

package container

import (
	"context"
	"crypto/x509"
	"os"
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
	file := filepath.Join(t.TempDir(), "corp.pem")
	writeFile(t, file, string(ca.CertPEM))
	return ca, file
}

func TestCACertsReachTheSandboxsProgramsOnTopOfTheSystemList(t *testing.T) {
	ca, file := newCA(t)
	pr := newProject(t)
	pr.p.opts.CACerts = []string{file}
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
	if out, ok := sh(sb, `echo evil >> "$SSL_CERT_FILE"`); ok {
		t.Errorf("the agent can change what the Sandbox trusts: %s", out)
	}
}

func TestWithoutCACertsNoCAVariableIsSet(t *testing.T) {
	sb := newProject(t).open(t)
	if out := mustSh(t, sb, `echo "[$SSL_CERT_FILE$REQUESTS_CA_BUNDLE$AWS_CA_BUNDLE$GIT_SSL_CAINFO$CURL_CA_BUNDLE$NODE_EXTRA_CA_CERTS]"`); out != "[]" {
		t.Errorf("set: %s", out)
	}
}

func TestASandboxImageWithoutASystemCAListStopsWithTheFix(t *testing.T) {
	_, file := newCA(t)
	pr := newProject(t)
	writeFile(t, filepath.Join(pr.root, ".fliesswerk", "Containerfile"), baseImage+"RUN rm -f /etc/ssl/certs/ca-certificates.crt /etc/ssl/cert.pem\n")
	pr.p.opts.CACerts = []string{file}
	ctx := context.Background()
	if err := connectAndPrepare(ctx, pr.p); err != nil {
		t.Fatal(err)
	}

	_, err := pr.p.Create(ctx, sandbox.Request{Name: "build-1", Worktree: pr.worktree})

	if err == nil || !strings.Contains(err.Error(), "install `ca-certificates`") {
		t.Fatalf("err = %v, want the fix named", err)
	}
}

// proxyBuild is a Containerfile recording what a build was given. NO_PROXY
// lets the engine itself still pull directly.
const proxyBuild = baseImage + `ARG HTTPS_PROXY
RUN --mount=type=secret,id=fliesswerk-ca cp /run/secrets/fliesswerk-ca /built-with.pem && chmod 644 /built-with.pem && echo "$HTTPS_PROXY" > /built-behind
`

func TestAnImageBuildGetsTheCAAsASecretAndTheHostsProxy(t *testing.T) {
	ca, file := newCA(t)
	pr := newProject(t)
	t.Setenv("HTTPS_PROXY", "http://proxy.invalid:3128")
	t.Setenv("NO_PROXY", "*")
	writeFile(t, filepath.Join(pr.root, ".fliesswerk", "Containerfile"), proxyBuild)
	pr.p.opts.CACerts = []string{file}
	sb := pr.open(t)

	if got := mustSh(t, sb, "cat /built-with.pem"); got != strings.TrimSpace(string(ca.CertPEM)) {
		t.Errorf("the build's secret = %q", got)
	}
	if got := mustSh(t, sb, "cat /built-behind"); got != "http://proxy.invalid:3128" {
		t.Errorf("the build's proxy = %q", got)
	}
	if strings.Contains(pr.engine(t, "image", "inspect", pr.p.image), "BEGIN CERTIFICATE") {
		t.Error("the CA is in the image's metadata")
	}
}

func TestChangingTheCACertsRebuildsTheImage(t *testing.T) {
	_, file := newCA(t)
	pr := newProject(t)
	ctx := context.Background()
	if err := connectAndPrepare(ctx, pr.p); err != nil {
		t.Fatal(err)
	}
	without := pr.p.image

	again := pr.provider(t)
	again.opts.CACerts = []string{file}
	if err := connectAndPrepare(ctx, again); err != nil {
		t.Fatal(err)
	}

	if again.image == without {
		t.Error("an image built without the CA is reused with it")
	}
}

const interceptedHost = "intercepted.example"

// curlImage has curl already: a build's own network isn't something a test
// can count on.
const curlImage = "FROM docker.io/curlimages/curl:latest\n"

// behindInterceptor is a Sandbox whose only way out leads through an
// intercepting proxy. The proxy goes on the sidecar's own network, so its name
// is only known once the sidecar is up, and the sidecar is told the name.
func behindInterceptor(t *testing.T, ca *catest.CA, caCerts ...string) sandbox.Sandbox {
	t.Helper()
	pr := newProject(t)
	writeFile(t, filepath.Join(pr.root, ".fliesswerk", "Containerfile"), curlImage)
	name := "kw-mitm-" + pr.p.opts.RunID
	pr.p.opts.CACerts = caCerts
	pr.p.opts.Egress = egress.Options{Allow: []string{interceptedHost}, UpstreamProxy: "http://" + name + ":3129"}
	pr.prepare(t)

	dir := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(dir, "interceptor"), "github.com/Marcel-Goehn/fliesswerk/internal/sandbox/catest/interceptor")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the interceptor: %v\n%s", err, out)
	}
	writeFile(t, filepath.Join(dir, "ca.crt"), string(ca.CertPEM))
	writeFile(t, filepath.Join(dir, "ca.key"), string(ca.KeyPEM))
	pr.engine(t, "run", "-d", "--name", name, "--network", pr.p.sidecar, "--user", "0:0",
		"--label", labelProject+"="+pr.p.project, "--mount", "type=bind,source="+dir+",target=/mitm,readonly",
		"--entrypoint", "/mitm/interceptor", pr.p.image, "-cert", "/mitm/ca.crt", "-key", "/mitm/ca.key")
	t.Cleanup(func() { _ = pr.p.engine.remove(context.Background(), name) })

	sb := pr.create(t, "build-1")
	if out, ok := eventually(sb, "curl -k -sS --max-time 5 https://"+interceptedHost+"/"); !ok || out != catest.Answer {
		t.Fatalf("the interceptor isn't answering: ok = %v, %q", ok, out)
	}
	return sb
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

func TestASandboxWorksOnAWorktreeWithNothingCheckedOutAndNoServices(t *testing.T) {
	pr := newProject(t)
	dir := filepath.Join(pr.root, ".fliesswerk", "state", "probe")
	gitIn(t, pr.root, "worktree", "add", "-q", "--detach", "--no-checkout", dir, "HEAD")
	ctx := context.Background()
	if err := connectAndPrepare(ctx, pr.p); err != nil {
		t.Fatal(err)
	}

	sb, err := pr.p.Create(ctx, sandbox.Request{Name: "probe", Worktree: dir, NoServices: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sb.Destroy(context.Background()) })

	if out := mustSh(t, sb, "echo ready"); out != "ready" {
		t.Errorf("got %q", out)
	}
}
