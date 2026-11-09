//go:build integration && linux

package native

import (
	"os"
	"os/exec"
	"strings"
	"testing"
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
