//go:build linux

package native

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testHost(t *testing.T) Host {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return Host{Home: home, RuntimeDir: "/run/user/1000", Path: []string{home + "/.local/bin", "/usr/bin"}}
}

func oneContaining(t *testing.T, got []string, parts ...string) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("got %d lines, want 1: %q", len(got), got)
	}
	for _, p := range parts {
		if !strings.Contains(got[0], p) {
			t.Errorf("%q doesn't say %q", got[0], p)
		}
	}
}

func TestCheckPathsWarnsAboutFootgunsMadeWritable(t *testing.T) {
	h := testHost(t)
	for name, tt := range map[string]struct{ path, says string }{
		"the home root":      {"~", "home"},
		"above the home":     {filepath.Dir(h.Home), "home"},
		"a shell rc file":    {"~/.bashrc", "shell"},
		"a zsh rc file":      {"~/.zshenv", "shell"},
		"a fish config":      {"~/.config/fish/config.fish", "shell"},
		"a fish snippet":     {"~/.config/fish/conf.d/x.fish", "shell"},
		"bash aliases":       {"~/.bash_aliases", "shell"},
		"a PATH dir":         {"~/.local/bin", "PATH"},
		"inside a PATH dir":  {"~/.local/bin/tool", "PATH"},
		"holding a PATH dir": {"~/.local", "PATH"},
	} {
		t.Run(name, func(t *testing.T) {
			refused, warnings := CheckPaths(nil, []string{tt.path}, h)
			if len(refused) != 0 {
				t.Fatalf("refused: %q", refused)
			}
			oneContaining(t, warnings, "sandbox.writable_paths", tt.path, tt.says)
		})
	}
}

func TestCheckPathsLeavesHarmlessPathsAlone(t *testing.T) {
	h := testHost(t)
	refused, warnings := CheckPaths([]string{"~", "~/.bashrc", "~/.local/bin"}, []string{"~/.cache/pnpm", "/opt/data"}, h)
	if len(refused) != 0 || len(warnings) != 0 {
		t.Fatalf("refused %q, warnings %q", refused, warnings)
	}
}

func TestCheckPathsRefusesWhatMustNeverBeExposed(t *testing.T) {
	h := testHost(t)
	for name, path := range map[string]string{
		"the docker socket":     "/var/run/docker.sock",
		"the podman socket dir": "/run/podman",
		"colima":                "~/.colima/default/docker.sock",
		"the session bus":       "/run/user/1000/bus",
		"the runtime dir":       "/run/user/1000",
		"everything":            "/",
		"all of run":            "/run",
		"a keyring":             "~/.local/share/keyrings",
		"a key in the keyring":  "~/.local/share/keyrings/login.keyring",
		"kwallet":               "~/.local/share/kwalletd",
		"gnupg":                 "~/.gnupg",
	} {
		t.Run(name, func(t *testing.T) {
			for _, lists := range [][2][]string{{{path}, nil}, {nil, {path}}} {
				refused, _ := CheckPaths(lists[0], lists[1], h)
				oneContaining(t, refused, path, "refused")
			}
		})
	}
}

func TestCheckPathsSeesThroughASymlink(t *testing.T) {
	h := testHost(t)
	socket := filepath.Join(h.Home, ".colima", "docker.sock")
	if err := os.MkdirAll(filepath.Dir(socket), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(socket, filepath.Join(h.Home, "innocent")); err != nil {
		t.Fatal(err)
	}
	refused, _ := CheckPaths([]string{"~/innocent"}, nil, h)
	oneContaining(t, refused, "~/innocent", "refused")
}

func TestCheckPathsRefusesARelativePath(t *testing.T) {
	refused, _ := CheckPaths([]string{"some/dir"}, nil, testHost(t))
	oneContaining(t, refused, "some/dir", "absolute")
}

func TestUnderWSLTheWindowsSideIsRefused(t *testing.T) {
	h := testHost(t)
	refused, _ := CheckPaths([]string{"/mnt/c/Users/me"}, nil, h)
	if len(refused) != 0 {
		t.Fatalf("off WSL, /mnt is an ordinary path: %q", refused)
	}
	h.WSL = true
	for _, path := range []string{"/mnt", "/mnt/c/Users/me", "/run/WSL/1_interop"} {
		refused, _ := CheckPaths([]string{path}, nil, h)
		oneContaining(t, refused, path, "refused")
	}
}

func TestAShellFileThatIsASymlinkStillWarns(t *testing.T) {
	h := testHost(t)
	real := filepath.Join(h.Home, "dotfiles", "bashrc")
	if err := os.MkdirAll(filepath.Dir(real), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(h.Home, ".bashrc")); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"~/.bashrc", "~/dotfiles"} {
		_, warnings := CheckPaths(nil, []string{path}, h)
		oneContaining(t, warnings, path, "shell")
	}
}

func TestAKeychainBehindASymlinkedHomeIsRefused(t *testing.T) {
	h := testHost(t)
	link := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(h.Home, link); err != nil {
		t.Fatal(err)
	}
	h.Home = link
	for _, path := range []string{"~/.gnupg", filepath.Join(link, ".gnupg")} {
		refused, _ := CheckPaths([]string{path}, nil, h)
		oneContaining(t, refused, path, "refused")
	}
}

func TestOnMacOSKeychainsAndEngineSocketsAreRefused(t *testing.T) {
	h := testHost(t)
	h.Mac = true
	for _, path := range []string{
		"~/Library/Keychains",
		"~/Library/Keychains/login.keychain-db",
		"/Library/Keychains/System.keychain",
		"/private/var/run/docker.sock",
		"~/.orbstack/run/docker.sock",
		"~/.lima/default/sock",
		"~/.rd/docker.sock",
		"~/Library/Containers/com.docker.docker/Data/docker.raw.sock",
		"/private",
		"/private/var",
		"/private/var/run",
	} {
		t.Run(path, func(t *testing.T) {
			refused, _ := CheckPaths([]string{path}, nil, h)
			oneContaining(t, refused, path, "refused")
		})
	}
}

func TestOffMacOSItsPathsAreOrdinary(t *testing.T) {
	refused, _ := CheckPaths([]string{"~/Library/Keychains", "/private"}, nil, testHost(t))
	if len(refused) != 0 {
		t.Fatalf("refused: %q", refused)
	}
}

func TestACACertsFileInAHiddenPlaceIsRefused(t *testing.T) {
	refused := CheckCACerts([]string{"/run/user/1000/corp.pem", "/etc/corp.pem"}, testHost(t))
	oneContaining(t, refused, "ca_certs", "/run/user/1000/corp.pem")
}
