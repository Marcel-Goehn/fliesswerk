package native

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// probeTimeout bounds the live test of what builds a Sandbox.
const probeTimeout = 30 * time.Second

// BlockedError says the machine refuses bwrap the namespaces it needs.
type BlockedError struct{ Fix string }

func (e *BlockedError) Error() string { return e.Fix }

// prerequisites is what Check looks at.
type prerequisites struct {
	lookPath func(string) (string, error)
	// probe runs bwrap with the namespaces a Sandbox uses and returns what
	// it printed.
	probe func(ctx context.Context, bwrap string) (string, error)
	// sysctl reads a value under /proc/sys, "" when there is none.
	sysctl func(name string) string
}

// Check proves that this machine can build a Sandbox, and returns the path of
// what builds it: bwrap, or sandbox-exec on macOS. Whatever is missing stops
// the Run with the exact fix: there is never a fallback to less isolation.
func Check(ctx context.Context) (string, error) {
	if runtime.GOOS == "darwin" {
		return seatbeltPrerequisites{version: macOSVersion, probe: probeSeatbelt}.check(ctx)
	}
	if runtime.GOOS != "linux" {
		return "", fmt.Errorf("the native Sandbox isn't available on %s yet; use `kind = \"container\"`, or run with `--sandbox none --i-accept-unsandboxed-risk` (or set them in .kraftwerk/config.local.toml)", runtime.GOOS)
	}
	return prerequisites{lookPath: exec.LookPath, probe: probe, sysctl: readSysctl}.check(ctx)
}

func probe(ctx context.Context, bwrap string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, bwrap, "--unshare-all", "--ro-bind", "/", "/", "--proc", "/proc", "--dev", "/dev", "--", "/bin/true").CombinedOutput() //nolint:gosec // bwrap, found on PATH
	return string(out), err
}

func readSysctl(name string) string {
	data, err := os.ReadFile(filepath.Join("/proc/sys", name)) //nolint:gosec // a fixed kernel setting
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (m prerequisites) check(ctx context.Context) (string, error) {
	bwrap, err := m.lookPath("bwrap")
	if err != nil {
		return "", errors.New("the native Sandbox needs bubblewrap, and `bwrap` isn't on PATH; install it (Debian/Ubuntu: `sudo apt install bubblewrap`, Fedora: `sudo dnf install bubblewrap`, Arch: `sudo pacman -S bubblewrap`), or pick another Sandbox kind with `sandbox.kind`")
	}
	out, err := m.probe(ctx, bwrap)
	if err == nil {
		return bwrap, nil
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return "", m.diagnose(bwrap, strings.TrimSpace(out), err)
}

// diagnose names the fix for a bwrap that can't start. The probe has
// already failed, so a setting that restricts user namespaces is the cause.
func (m prerequisites) diagnose(bwrap, out string, err error) error {
	const refused = "the native Sandbox can't start: bwrap failed (%s), because %s.\nFix it with:\n\n%s"
	switch {
	case m.sysctl("kernel/apparmor_restrict_unprivileged_userns") == "1":
		// Not indented: a shell only ends a heredoc at a line that is exactly EOF.
		fix := "sudo tee /etc/apparmor.d/bwrap >/dev/null <<'EOF'\nabi <abi/4.0>,\ninclude <tunables/global>\nprofile bwrap " + bwrap + " flags=(unconfined) {\n  userns,\n}\nEOF\nsudo apparmor_parser -r /etc/apparmor.d/bwrap"
		return &BlockedError{Fix: fmt.Sprintf(refused, out, "AppArmor blocks unprivileged user namespaces on this machine (the default on Ubuntu 24.04 and later)", fix)}
	case m.sysctl("kernel/unprivileged_userns_clone") == "0":
		return &BlockedError{Fix: fmt.Sprintf(refused, out, "this kernel has unprivileged user namespaces switched off", "  sudo sysctl -w kernel.unprivileged_userns_clone=1")}
	case m.sysctl("user/max_user_namespaces") == "0":
		return &BlockedError{Fix: fmt.Sprintf(refused, out, "this kernel allows no user namespaces", "  sudo sysctl -w user.max_user_namespaces=15000")}
	}
	return fmt.Errorf("the native Sandbox can't start: bwrap failed: %w\n%s", err, out)
}

// sandboxExec is macOS's own, by full path: PATH is the user's to change.
const sandboxExec = "/usr/bin/sandbox-exec"

// minMacOS is the oldest macOS the native Sandbox supports.
const minMacOS = 14

// seatbeltPrerequisites is what Check looks at on macOS.
type seatbeltPrerequisites struct {
	version func() (string, error)
	// probe runs sandbox-exec and returns what it printed.
	probe func(ctx context.Context, launcher string) (string, error)
}

func macOSVersion() (string, error) {
	out, err := exec.Command("/usr/bin/sw_vers", "-productVersion").Output()
	return strings.TrimSpace(string(out)), err
}

func probeSeatbelt(ctx context.Context, launcher string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, launcher, "-p", "(version 1)(allow default)", "/usr/bin/true").CombinedOutput() //nolint:gosec // macOS's own, by full path
	return string(out), err
}

func (m seatbeltPrerequisites) check(ctx context.Context) (string, error) {
	version, err := m.version()
	if err != nil {
		return "", fmt.Errorf("the native Sandbox can't tell which macOS this is: %w; check that `/usr/bin/sw_vers -productVersion` works, or pick another Sandbox kind with `sandbox.kind`", err)
	}
	majorText, _, _ := strings.Cut(version, ".")
	major, err := strconv.Atoi(majorText)
	if err != nil {
		return "", fmt.Errorf("the native Sandbox can't tell which macOS this is: %q isn't a version; check that `/usr/bin/sw_vers -productVersion` works, or pick another Sandbox kind with `sandbox.kind`", version)
	}
	if major < minMacOS {
		return "", fmt.Errorf("the native Sandbox needs macOS %d or later, and this is macOS %s; update it in System Settings > General > Software Update, or pick another Sandbox kind with `sandbox.kind`", minMacOS, version)
	}
	out, err := m.probe(ctx, sandboxExec)
	if err == nil {
		return sandboxExec, nil
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return "", fmt.Errorf("the native Sandbox can't start: sandbox-exec failed: %w\n%s\nmacOS ships %s: check that it runs, or pick another Sandbox kind with `sandbox.kind`", err, strings.TrimSpace(out), sandboxExec)
}
