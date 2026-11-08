//go:build integration

package container

import (
	"context"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/egress"
	"github.com/Marcel-Goehn/kraftwerk/internal/sandbox"
)

// directWget fetches a URL without the proxy the Sandbox is set up with.
const directWget = "env -u http_proxy -u https_proxy -u HTTP_PROXY -u HTTPS_PROXY wget -q -T 3 -O - "

// serveLoop answers HTTP on port 8080, one connection at a time.
const serveLoop = `while true; do printf 'HTTP/1.0 200 OK\r\nContent-Length: 7\r\n\r\nreached' | nc -l -p 8080; done`

func (pr *project) prepare(t *testing.T) {
	t.Helper()
	if err := connectAndPrepare(context.Background(), pr.p); err != nil {
		t.Fatal(err)
	}
}

func (pr *project) create(t *testing.T, name string) sandbox.Sandbox {
	t.Helper()
	sb, err := pr.p.Create(context.Background(), sandbox.Request{Name: name, Worktree: pr.worktree})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sb.Destroy(context.Background()) })
	return sb
}

// startTarget runs a web server on the sidecar's normal network, where only
// the sidecar can get at it, and returns its address there.
func (pr *project) startTarget(t *testing.T, name string) string {
	t.Helper()
	pr.engine(t, "run", "-d", "--name", name, "--network", pr.p.sidecar,
		"--label", labelProject+"="+pr.p.project, "--entrypoint", "/bin/sh", pr.p.image, "-c", serveLoop)
	t.Cleanup(func() { _ = pr.p.engine.remove(context.Background(), name) })
	return addressOn(t, pr, name, pr.p.sidecar)
}

func addressOn(t *testing.T, pr *project, container, network string) string {
	t.Helper()
	format := `{{(index .NetworkSettings.Networks "` + network + `").IPAddress}}`
	ip := strings.TrimSpace(pr.engine(t, "container", "inspect", "--format", format, container))
	if ip == "" {
		t.Fatalf("%s has no address on %s", container, network)
	}
	return ip
}

// eventually retries line until it passes, for a server that is starting.
func eventually(sb sandbox.Sandbox, line string) (string, bool) {
	var out string
	var ok bool
	for range 20 {
		if out, ok = sh(sb, line); ok {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	return out, ok
}

func TestTheSidecarImageIsImportedOnceForTheEnginesArchitectureAndTheVersion(t *testing.T) {
	pr := newProject(t)
	version := "v0.0.0-test-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	pr.p.opts.Version = version
	image := "kraftwerk-proxy:" + version
	pr.prepare(t)
	t.Cleanup(func() {
		pr.p.Close()
		_, _ = pr.p.engine.run(context.Background(), "image", "rm", "-f", image)
	})

	got := strings.TrimSpace(pr.engine(t, "image", "inspect", "--format", "{{.Architecture}}", image))
	if got != runtime.GOARCH {
		t.Errorf("image architecture = %q, want %q", got, runtime.GOARCH)
	}

	pr.logged = nil
	again := pr.provider(t)
	again.opts.Version = version
	if err := connectAndPrepare(context.Background(), again); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(pr.logged, "\n"), "Importing") {
		t.Errorf("the image was imported again: %q", pr.logged)
	}
}

func TestAnAllowedHostIsReachableThroughTheSidecarAndNothingElseIs(t *testing.T) {
	pr := newProject(t)
	target := "kw-target-" + pr.p.opts.RunID
	pr.p.opts.Egress = egress.Options{Allow: []string{target}}
	pr.prepare(t)
	ip := pr.startTarget(t, target)
	sb := pr.create(t, "build-1")

	if out, ok := eventually(sb, "wget -q -T 5 -O - http://"+target+":8080/"); !ok || out != "reached" {
		t.Fatalf("the allowed host through the sidecar: ok = %v, %q", ok, out)
	}
	want := "http://" + addressOn(t, pr, pr.p.sidecar, sb.(*box).name) + ":3128"
	if got := mustSh(t, sb, `printf %s "$HTTPS_PROXY"`); got != want {
		t.Errorf("HTTPS_PROXY = %q, want %q", got, want)
	}
	if got := mustSh(t, sb, `printf %s "$HTTPS_PROXY"`, "HTTPS_PROXY=http://elsewhere:1"); got != want {
		t.Errorf("a command's own env moved HTTPS_PROXY to %q", got)
	}

	if out, ok := sh(sb, "wget -q -T 5 -O - http://"+ip+":8080/"); ok {
		t.Errorf("a host off the allowlist was reached through the sidecar: %s", out)
	}
	for _, url := range []string{"http://" + ip + ":8080/", "http://" + target + ":8080/", "http://1.1.1.1/"} {
		if out, ok := sh(sb, directWget+url); ok {
			t.Errorf("direct egress to %s worked: %s", url, out)
		}
	}

	// The Run's network can't go while the test's own server is on it.
	if err := pr.p.engine.remove(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	pr.p.Close()
	logged := strings.Join(pr.logged, "\n")
	if !strings.Contains(logged, "denied a connection to "+ip) {
		t.Errorf("the denied host isn't in the Run's log: %q", pr.logged)
	}
	if strings.Contains(logged, "warning: removing the proxy sidecar") {
		t.Errorf("the sidecar didn't go cleanly: %q", pr.logged)
	}
}

func TestTwoSandboxesCannotReachEachOther(t *testing.T) {
	pr := newProject(t)
	pr.prepare(t)
	one, other := pr.create(t, "build-1"), pr.create(t, "build-2")

	mustSh(t, other, "nohup sh -c '"+strings.ReplaceAll(serveLoop, "'", `'\''`)+"' >/dev/null 2>&1 &")
	if out, ok := eventually(other, directWget+"http://127.0.0.1:8080/"); !ok || out != "reached" {
		t.Fatalf("the server doesn't answer in its own Sandbox: ok = %v, %q", ok, out)
	}
	url := "http://" + addressOn(t, pr, other.(*box).name, other.(*box).name) + ":8080/"

	if out, ok := sh(one, directWget+url); ok {
		t.Errorf("one Sandbox reached the other directly: %s", out)
	}
	if out, ok := sh(one, "wget -q -T 5 -O - "+url); ok {
		t.Errorf("one Sandbox reached the other through the sidecar: %s", out)
	}
}

func TestTheSidecarIsOnANormalNetworkAndEveryInternalOneAndGoesWithTheRun(t *testing.T) {
	pr := newProject(t)
	pr.prepare(t)
	sidecar := pr.p.sidecar
	one, other := pr.create(t, "build-1"), pr.create(t, "build-2")

	if got := strings.TrimSpace(pr.engine(t, "network", "inspect", "--format", "{{.Internal}}", sidecar)); got != "false" {
		t.Errorf("the sidecar's own network has Internal = %q, want false", got)
	}
	for _, network := range []string{sidecar, one.(*box).name, other.(*box).name} {
		addressOn(t, pr, sidecar, network)
	}
	label := "label=" + labelRun + "=" + pr.p.opts.RunID
	if got := strings.Fields(pr.engine(t, "ps", "--filter", label, "--filter", "name="+sidecar, "--format", "{{.Names}}")); !slices.Equal(got, []string{sidecar}) {
		t.Errorf("sidecars of the Run = %q, want one", got)
	}

	for _, sb := range []sandbox.Sandbox{one, other} {
		if err := sb.Destroy(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	pr.p.Close()
	for _, list := range [][]string{{"ps", "-a", "--filter", label, "--format", "{{.Names}}"}, {"network", "ls", "--filter", label, "--format", "{{.Name}}"}} {
		if got := strings.TrimSpace(pr.engine(t, list...)); got != "" {
			t.Errorf("%s left: %q", list[0], got)
		}
	}
}

func TestAHostProxyLoginStaysOutOfTheSidecarWhenUpstreamProxyReplacesIt(t *testing.T) {
	pr := newProject(t)
	pr.p.opts.Egress = egress.Options{UpstreamProxy: "http://proxy.corp:8080"}
	if err := pr.p.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HTTPS_PROXY", "http://me:hunter2@proxy.corp:8080")

	if err := pr.p.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}

	sb := pr.create(t, "build-1")
	for _, name := range []string{pr.p.sidecar, sb.(*box).name} {
		if strings.Contains(pr.engine(t, "container", "inspect", name), "hunter2") {
			t.Errorf("inspect of %s shows the password", name)
		}
	}
}

func TestAHostProxyWithALoginStopsTheRunWithoutShowingIt(t *testing.T) {
	pr := newProject(t)
	if err := pr.p.Connect(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HTTPS_PROXY", "http://me:hunter2@proxy.corp:8080")

	err := pr.p.Prepare(context.Background())

	if err == nil || !strings.Contains(err.Error(), "proxy authentication") {
		t.Fatalf("err = %v, want one about proxy authentication", err)
	}
	if strings.Contains(err.Error(), "hunter2") {
		t.Errorf("the error shows the password: %v", err)
	}
}
