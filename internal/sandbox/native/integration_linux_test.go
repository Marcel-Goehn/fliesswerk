//go:build integration && linux

package native

import (
	"context"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Marcel-Goehn/kraftwerk/internal/sandbox"
	"github.com/Marcel-Goehn/kraftwerk/internal/sandbox/container"
)

func TestTheSecretIsNotInTheFirstProcesssArgv(t *testing.T) {
	sb := newProject(t, nil, nil).open(t)
	if got := mustSh(t, sb, `tr '\0' ' ' < /proc/1/cmdline; ls /proc/self/fd | wc -l`, "API_TOKEN="+secret); strings.Contains(got, secret) {
		t.Errorf("the secret is in the Sandbox's first process's argv: %q", got)
	}
}

func TestNothingOutlivesItsCommand(t *testing.T) {
	sb := newProject(t, nil, nil).open(t)
	const marker = "31415926"
	mustSh(t, sb, "setsid sleep "+marker+" >/dev/null 2>&1 &")
	out, _ := exec.Command("pgrep", "-f", "sleep "+marker).Output()
	if got := strings.TrimSpace(string(out)); got != "" {
		t.Errorf("left running: %s", got)
	}
}

func TestEngineSocketsAndTheBusAreNotThere(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/run/user/1000/bus")
	sb := newProject(t, nil, nil).open(t)

	for _, p := range []string{"/run/docker.sock", "/var/run/docker.sock", "/run/podman", "/run/dbus", "/run/user", "/sys"} {
		mustFail(t, sb, "test -e "+p)
	}
	if got := mustSh(t, sb, `printf %s "${DBUS_SESSION_BUS_ADDRESS:-unset}"`); got != "unset" {
		t.Errorf("DBUS_SESSION_BUS_ADDRESS = %q", got)
	}
}

func TestUnderWSLTheWindowsSideAndInteropAreHidden(t *testing.T) {
	t.Setenv("PATH", "/mnt:"+os.Getenv("PATH"))
	pr := newProject(t, nil, func(o *Options) {
		host := CurrentHost()
		host.WSL = true
		o.Host = &host
	})
	sb := pr.open(t)

	if got := mustSh(t, sb, `printf %s "$PATH"`); strings.Contains(got, "/mnt") {
		t.Errorf("PATH = %q", got)
	}
	for _, p := range []string{"/mnt", "/run/WSL", "/init", "/proc/sys/fs/binfmt_misc/register", "/proc/sys/fs/binfmt_misc/WSLInterop"} {
		mustFail(t, sb, "test -e "+p)
	}
}

// published is one Service with one port, on a listener of its own.
func published(t *testing.T, name string, containerPort int, aliases ...string) sandbox.PublishedService {
	t.Helper()
	return sandbox.PublishedService{Name: name, Aliases: append([]string{name}, aliases...), Ports: []sandbox.PublishedPort{{Container: containerPort, Host: listener(t, name)}}}
}

func TestAServiceIsReachableEveryWayTheSandboxIsToldAndNothingElseIs(t *testing.T) {
	db := published(t, "db", 5432, "shop-db")
	other := unpublishedPort(t)
	pr := newProject(t, nil, func(o *Options) {
		o.Services = &fakeServices{running: []sandbox.PublishedService{db}}
		o.EnvTemplates = map[string]map[string]string{"db": {"DATABASE_URL": "postgres://{host}:{port}/app"}}
	})
	sb := pr.open(t)

	for _, address := range []string{"db:5432", "shop-db:5432", "localhost:5432", "127.0.0.1:5432"} {
		if got := mustSh(t, sb, fetch(address)); got != "db" {
			t.Errorf("through %s: %q", address, got)
		}
	}
	if got := mustSh(t, sb, `printf '%s %s %s' "$KRAFTWERK_SERVICE_DB_HOST" "$KRAFTWERK_SERVICE_DB_PORT" "$DATABASE_URL"`); got != "db 5432 postgres://db:5432/app" {
		t.Errorf("the variables say %q", got)
	}
	listed := strings.Join(sb.Services()[0].Addresses, " ")
	for _, address := range []string{"db:5432", "shop-db:5432", "localhost:5432"} {
		if !strings.Contains(listed, address) {
			t.Errorf("Services() = %q, without %s", listed, address)
		}
	}
	mustFail(t, sb, fetch("127.0.0.1:"+strconv.Itoa(db.Ports[0].Host)))
	mustFail(t, sb, "curl -s --noproxy '*' --max-time 3 http://127.0.0.1:"+strconv.Itoa(other)+"/")
	mustFail(t, sb, "echo 127.0.0.1 evil >> /etc/hosts")
	if err := sb.KillAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := mustSh(t, sb, fetch("db:5432")); got != "db" {
		t.Errorf("after a KillAll: %q", got)
	}
}

func TestTwoServicesOnOnePortAreBothReachableByName(t *testing.T) {
	pr := newProject(t, nil, func(o *Options) {
		o.Services = &fakeServices{running: []sandbox.PublishedService{published(t, "queue", 6379), published(t, "cache", 6379)}}
	})
	sb := pr.open(t)

	for name, address := range map[string]string{"cache": "cache:6379", "queue": "queue:6379"} {
		if got := mustSh(t, sb, fetch(address)); got != name {
			t.Errorf("%s: %q", address, got)
		}
	}
	if got := mustSh(t, sb, fetch("localhost:6379")); got != "cache" {
		t.Errorf("localhost stays with the first by name, and went to %q", got)
	}
}

func TestAServiceOnALowPortIsAtItsPublishedPortAndTheSandboxIsToldSo(t *testing.T) {
	web := published(t, "web", 80)
	pr := newProject(t, nil, func(o *Options) {
		o.Services = &fakeServices{running: []sandbox.PublishedService{web}}
		o.EnvTemplates = map[string]map[string]string{"web": {"WEB_URL": "http://{host}:{port}"}}
	})
	sb := pr.open(t)
	address := "web:" + strconv.Itoa(web.Ports[0].Host)

	if got := mustSh(t, sb, fetch(address)); got != "web" {
		t.Errorf("%s: %q", address, got)
	}
	if got := mustSh(t, sb, `printf %s "$WEB_URL"`); got != "http://"+address {
		t.Errorf("WEB_URL = %q", got)
	}
	if listed := strings.Join(sb.Services()[0].Addresses, " "); !strings.Contains(listed, address) || strings.Contains(listed, "web:80 ") {
		t.Errorf("Services() = %q", listed)
	}
	if got := mustSh(t, sb, `grep -E '^Cap(Eff|Amb)' /proc/self/status | awk '{print $2}' | sort -u`); got != "0000000000000000" {
		t.Errorf("the command's capabilities: %q", got)
	}
}

func TestAServiceOnTheProxysPortLeavesTheProxyAlone(t *testing.T) {
	pr := newProject(t, nil, func(o *Options) {
		o.Services = &fakeServices{running: []sandbox.PublishedService{published(t, "squid", 3128)}}
	})
	sb := pr.open(t)

	if got := mustSh(t, sb, fetch("squid:3128")); got != "squid" {
		t.Errorf("by name: %q", got)
	}
	if got := status(t, sb, "http://denied.example/"); got != "403" {
		t.Errorf("the egress proxy answered %s", got)
	}
}

func TestARealComposeServiceIsReachableInsideTheSandbox(t *testing.T) {
	if _, err := container.Resolve(context.Background()); err != nil {
		t.Skipf("no container engine: %v", err)
	}
	const compose = "services:\n  web:\n    image: docker.io/library/python:3-alpine\n    container_name: shop-web\n    command: python -m http.server 8000\n    ports:\n      - \"58000:8000\"\n"
	var services *container.Published
	pr := newProject(t, map[string]string{"compose.yaml": compose}, func(o *Options) {
		services = container.NewPublished(container.PublishedOptions{Root: o.Root, StateDir: o.StateDir, ComposeFile: "compose.yaml", Services: []string{"web"}})
		o.Services = services
	})
	sb := pr.open(t)

	// The port is published before the server in the container listens.
	if out, ok := sh(sb, `curl -s -o /dev/null -w '%{http_code}' --max-time 5 --retry 30 --retry-all-errors --retry-delay 1 http://web:8000/`); !ok || out != "200" {
		t.Errorf("web:8000: %q", out)
	}
	if got := mustSh(t, sb, `curl -s -o /dev/null -w '%{http_code}' --max-time 5 http://shop-web:8000/`); got != "200" {
		t.Errorf("shop-web:8000: %q", got)
	}
	if err := sb.Destroy(context.Background()); err != nil {
		t.Fatal(err)
	}
	if conn, err := net.DialTimeout("tcp", "127.0.0.1:58000", time.Second); err == nil {
		_ = conn.Close()
		t.Error("the user's own port was published")
	}
}
