package native

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Host is what the Sandbox's paths depend on.
type Host struct {
	// Home is the user's real home, and RuntimeDir their XDG runtime dir.
	Home, RuntimeDir string
	// Path are the directories on the user's PATH.
	Path []string
	// WSL reports whether this is Linux under WSL2.
	WSL bool
}

// CurrentHost is the machine this runs on.
func CurrentHost() Host {
	home, _ := os.UserHomeDir()
	runtimeDir := os.Getenv("XDG_RUNTIME_DIR")
	if runtimeDir == "" {
		runtimeDir = "/run/user/" + strconv.Itoa(os.Getuid())
	}
	version, _ := os.ReadFile("/proc/version")
	return Host{
		Home: home, RuntimeDir: runtimeDir,
		Path: filepath.SplitList(os.Getenv("PATH")),
		WSL:  strings.Contains(strings.ToLower(string(version)), "microsoft"),
	}
}

// tooBroad can't be added whole: what must stay hidden lies under them, in
// places too many and too changeable to mask one by one.
var tooBroad = []string{"/", "/run", "/var", "/var/run", "/run/user"}

// neverExposed are the container engines' sockets, the session and system
// D-Bus, and the keychains: each hands the agent something outside the
// Sandbox. Under WSL2 the Windows side counts too.
func (h Host) neverExposed() []string {
	never := []string{
		"/run/docker.sock", "/var/run/docker.sock", "/run/podman", "/var/run/podman",
		"/run/dbus", "/var/run/dbus",
	}
	if h.RuntimeDir != "" {
		never = append(never, h.RuntimeDir) // the bus, the keyring and the rootless engines' sockets
	}
	if h.Home != "" {
		for _, rel := range []string{
			".docker/run", ".docker/desktop", ".colima", ".config/colima",
			".local/share/containers/podman/machine",
			".local/share/keyrings", ".local/share/kwalletd", ".gnupg",
		} {
			never = append(never, filepath.Join(h.Home, rel))
		}
	}
	if h.WSL {
		never = append(never, "/mnt", "/run/WSL")
	}
	// Whichever way a path reaches them: /home is a symlink on some systems.
	for _, p := range slices.Clone(never) {
		never = append(never, realPath(p))
	}
	slices.Sort(never)
	return slices.Compact(never)
}

// hidden reports whether p must stay out of every Sandbox.
func (h Host) hidden(p string) bool {
	return slices.Contains(tooBroad, p) || slices.ContainsFunc(h.neverExposed(), func(n string) bool { return within(p, n) })
}

// exposes reports whether binding p whole would show what must stay hidden.
func (h Host) exposes(p string) bool {
	return h.hidden(p) || slices.ContainsFunc(h.neverExposed(), func(n string) bool { return within(n, p) })
}

// within reports whether p is dir or inside it.
func within(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, strings.TrimSuffix(dir, "/")+"/")
}

// expand makes a configured path absolute. Only absolute paths and paths in
// the home can be meant the same on every teammate's machine.
func (h Host) expand(p string) (string, bool) {
	switch {
	case p == "~":
		return h.Home, h.Home != ""
	case strings.HasPrefix(p, "~/"):
		return filepath.Join(h.Home, p[2:]), h.Home != ""
	case filepath.IsAbs(p):
		return filepath.Clean(p), true
	}
	return "", false
}

// realPath is p with its symlinks followed, or p itself while it doesn't
// exist.
func realPath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

// shellFiles run outside the Sandbox whenever the user opens a shell.
var shellFiles = []string{
	".bashrc", ".bash_aliases", ".bash_profile", ".bash_login", ".bash_logout", ".profile",
	".zshrc", ".zshenv", ".zprofile", ".zlogin", ".zlogout", ".config/fish",
}

// CheckPaths checks `sandbox.read_only_paths` and `sandbox.writable_paths`.
// refused are the paths no Sandbox may have, and warnings the writable ones
// that let the agent reach past the Sandbox, which stay the user's call.
func CheckPaths(readOnly, writable []string, h Host) (refused, warnings []string) {
	lists := []struct {
		key      string
		paths    []string
		writable bool
	}{{"sandbox.read_only_paths", readOnly, false}, {"sandbox.writable_paths", writable, true}}
	for _, list := range lists {
		for _, p := range list.paths {
			full, ok := h.expand(p)
			if !ok {
				refused = append(refused, fmt.Sprintf("%s: %q is refused: a path must be absolute or start with ~/", list.key, p))
				continue
			}
			if h.hidden(full) || h.hidden(realPath(full)) {
				refused = append(refused, fmt.Sprintf("%s: %q is refused: it would expose a container engine's socket, D-Bus, a keychain or (under WSL2) the Windows side", list.key, p))
				continue
			}
			if list.writable {
				if w := h.footgun(realPath(full)); w != "" {
					warnings = append(warnings, fmt.Sprintf("%s: %q %s", list.key, p, w))
				}
			}
		}
	}
	return refused, warnings
}

// footgun says how making full writable reaches past the Sandbox, "" if it
// doesn't.
func (h Host) footgun(full string) string {
	if h.Home != "" && within(realPath(h.Home), full) {
		return "makes your home root writable: the agent can change what runs outside the Sandbox"
	}
	for _, rel := range shellFiles {
		shellFile := realPath(filepath.Join(h.Home, rel))
		if within(shellFile, full) || within(full, shellFile) {
			return "makes a shell rc file writable: it runs outside the Sandbox whenever you open a shell"
		}
	}
	for _, dir := range h.Path {
		if dir = realPath(dir); filepath.IsAbs(dir) && (within(full, dir) || within(dir, full)) {
			return "makes a directory on your PATH writable: the agent can replace programs you run outside the Sandbox"
		}
	}
	return ""
}
