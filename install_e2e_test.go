package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// releaseArchives are the six archives of a release.
var releaseArchives = []string{
	"kraftwerk_linux_amd64.tar.gz", "kraftwerk_linux_arm64.tar.gz",
	"kraftwerk_darwin_amd64.tar.gz", "kraftwerk_darwin_arm64.tar.gz",
	"kraftwerk_windows_amd64.zip", "kraftwerk_windows_arm64.zip",
}

// fakeRelease serves a release the way GitHub does, under both the latest
// and the pinned URL. Each archive's binary says which archive it came from.
type fakeRelease struct {
	url      string
	files    map[string][]byte
	binaries map[string][]byte

	mu       sync.Mutex
	requests []string
}

func newFakeRelease(t *testing.T) *fakeRelease {
	t.Helper()
	r := &fakeRelease{files: map[string][]byte{}, binaries: map[string][]byte{}}
	for _, name := range releaseArchives {
		r.replace(t, name, []byte("#!/bin/sh\necho "+name+"\n"))
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.Lock()
		r.requests = append(r.requests, req.URL.Path)
		r.mu.Unlock()
		data, ok := r.files[filepath.Base(req.URL.Path)]
		if !ok {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	r.url = srv.URL
	return r
}

// replace publishes binary in the named archive, with checksums to match.
func (r *fakeRelease) replace(t *testing.T, name string, binary []byte) {
	t.Helper()
	r.binaries[name] = binary
	if strings.HasSuffix(name, ".zip") {
		r.files[name] = zipWith(t, "kraftwerk.exe", binary)
	} else {
		r.files[name] = tarGzWith(t, "kraftwerk", binary)
	}
	var sums strings.Builder
	for _, archive := range releaseArchives {
		if data, ok := r.files[archive]; ok {
			sum := sha256.Sum256(data)
			fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(sum[:]), archive)
		}
	}
	r.files["checksums.txt"] = []byte(sums.String())
}

func tarGzWith(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func zipWith(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// shInstall is one machine running install.sh: a fresh home, and a PATH with
// only what a stock system has, so a kraftwerk or kwk of the developer's own
// can't be seen.
type shInstall struct {
	home   string
	tools  string
	dir    string
	extra  []string
	stdout string
	stderr string
}

func newShInstall(t *testing.T, kernel, machine string) *shInstall {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("install.sh is for Linux and macOS")
	}
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	s := &shInstall{home: home, tools: t.TempDir()}
	s.dir = filepath.Join(home, ".local", "bin")
	for _, name := range []string{"awk", "basename", "chmod", "cp", "curl", "grep", "gzip", "ln", "mkdir", "mktemp", "mv", "readlink", "rm", "tar"} {
		s.linkTool(t, name)
	}
	if _, err := exec.LookPath("sha256sum"); err == nil {
		s.linkTool(t, "sha256sum")
	} else {
		s.linkTool(t, "shasum")
	}
	script := fmt.Sprintf("#!/bin/sh\ncase \"$1\" in -s) echo %s ;; -m) echo %s ;; esac\n", kernel, machine)
	if err := os.WriteFile(filepath.Join(s.tools, "uname"), []byte(script), 0o700); err != nil { //nolint:gosec // an executable the test runs
		t.Fatal(err)
	}
	return s
}

func (s *shInstall) linkTool(t *testing.T, name string) {
	t.Helper()
	path, err := exec.LookPath(name)
	if err != nil {
		t.Fatalf("this test needs %s: %v", name, err)
	}
	if err := os.Symlink(path, filepath.Join(s.tools, name)); err != nil {
		t.Fatal(err)
	}
}

// run pipes install.sh into sh, as the one-line install does.
func (s *shInstall) run(t *testing.T, release *fakeRelease) int {
	t.Helper()
	script, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(shell, "-s")
	cmd.Dir = s.home
	cmd.Stdin = bytes.NewReader(script)
	cmd.Env = append([]string{"HOME=" + s.home, "PATH=" + s.tools, "SHELL=/bin/sh", "KRAFTWERK_DOWNLOAD_URL=" + release.url}, s.extra...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	s.stdout, s.stderr = stdout.String(), stderr.String()
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatal(err)
	}
	return exit.ExitCode()
}

func (s *shInstall) mustInstall(t *testing.T, release *fakeRelease) {
	t.Helper()
	if code := s.run(t, release); code != 0 {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, s.stdout, s.stderr)
	}
}

func TestInstallShPicksTheArchiveAndInstallsItsBinaryUnchanged(t *testing.T) {
	tests := []struct{ kernel, machine, archive string }{
		{"Linux", "x86_64", "kraftwerk_linux_amd64.tar.gz"},
		{"Linux", "aarch64", "kraftwerk_linux_arm64.tar.gz"},
		{"Darwin", "x86_64", "kraftwerk_darwin_amd64.tar.gz"},
		{"Darwin", "arm64", "kraftwerk_darwin_arm64.tar.gz"},
	}
	for _, tt := range tests {
		t.Run(tt.kernel+"_"+tt.machine, func(t *testing.T) {
			release := newFakeRelease(t)
			s := newShInstall(t, tt.kernel, tt.machine)

			s.mustInstall(t, release)

			bin := filepath.Join(s.dir, "kraftwerk")
			got, err := os.ReadFile(bin)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, release.binaries[tt.archive]) {
				t.Errorf("installed binary = %q, want the one from %s", got, tt.archive)
			}
			info, err := os.Stat(bin)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm()&0o100 == 0 {
				t.Errorf("mode = %v, not executable", info.Mode())
			}
		})
	}
}

func TestInstallShRefusesAnArchiveWhoseChecksumDiffers(t *testing.T) {
	release := newFakeRelease(t)
	release.files["kraftwerk_linux_amd64.tar.gz"] = tarGzWith(t, "kraftwerk", []byte("tampered"))
	s := newShInstall(t, "Linux", "x86_64")

	code := s.run(t, release)

	if code == 0 {
		t.Fatalf("a tampered archive installed\nstdout: %s", s.stdout)
	}
	mustContain(t, "stderr", s.stderr, "checksum")
	if _, err := os.Stat(filepath.Join(s.dir, "kraftwerk")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("kraftwerk was installed anyway: %v", err)
	}
}

func TestInstallShRefusesAnArchiveMissingFromTheChecksums(t *testing.T) {
	release := newFakeRelease(t)
	release.files["checksums.txt"] = []byte("abc  kraftwerk_linux_arm64.tar.gz\n")
	s := newShInstall(t, "Linux", "x86_64")

	if code := s.run(t, release); code == 0 {
		t.Fatalf("an unlisted archive installed\nstdout: %s", s.stdout)
	}
	mustContain(t, "stderr", s.stderr, "checksums.txt")
}

func TestInstallShGetsThePinnedVersionOrElseTheLatest(t *testing.T) {
	tests := []struct {
		name, version, wantPrefix string
	}{
		{"latest", "", "/latest/download/"},
		{"pinned", "v1.2.3", "/download/v1.2.3/"},
		{"pinned without the v", "1.2.3", "/download/v1.2.3/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			release := newFakeRelease(t)
			s := newShInstall(t, "Linux", "x86_64")
			if tt.version != "" {
				s.extra = append(s.extra, "KRAFTWERK_VERSION="+tt.version)
			}

			s.mustInstall(t, release)

			for _, path := range release.requests {
				if !strings.HasPrefix(path, tt.wantPrefix) {
					t.Errorf("asked for %s, want it under %s", path, tt.wantPrefix)
				}
			}
		})
	}
}

func TestInstallShAddsKwkAsALinkToKraftwerk(t *testing.T) {
	release := newFakeRelease(t)
	s := newShInstall(t, "Linux", "x86_64")

	s.mustInstall(t, release)

	got, err := os.Readlink(filepath.Join(s.dir, "kwk"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "kraftwerk" {
		t.Errorf("kwk links to %q, want kraftwerk", got)
	}
}

func TestInstallShLeavesAKwkAlreadyOnThePathAlone(t *testing.T) {
	release := newFakeRelease(t)
	s := newShInstall(t, "Linux", "x86_64")
	if err := os.WriteFile(filepath.Join(s.tools, "kwk"), []byte("#!/bin/sh\necho theirs\n"), 0o700); err != nil { //nolint:gosec // an executable the test runs
		t.Fatal(err)
	}

	s.mustInstall(t, release)

	if _, err := os.Lstat(filepath.Join(s.dir, "kwk")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a second kwk was added: %v", err)
	}
	mustContain(t, "stdout", s.stdout, "kwk", filepath.Join(s.tools, "kwk"))
}

func TestInstallShNeverOverwritesAFileNamedKwk(t *testing.T) {
	release := newFakeRelease(t)
	s := newShInstall(t, "Linux", "x86_64")
	theirs := filepath.Join(s.dir, "kwk")
	writeFile(t, theirs, "theirs, and its directory isn't on the PATH yet\n")

	s.mustInstall(t, release)

	got, err := os.ReadFile(theirs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(got), "theirs") {
		t.Errorf("kwk was overwritten: %q", got)
	}
}

func TestInstallShRunAgainUpgradesAndKeepsItsOwnKwk(t *testing.T) {
	release := newFakeRelease(t)
	s := newShInstall(t, "Linux", "x86_64")
	s.mustInstall(t, release)

	release.replace(t, "kraftwerk_linux_amd64.tar.gz", []byte("#!/bin/sh\necho newer\n"))
	s.mustInstall(t, release)

	got, err := os.ReadFile(filepath.Join(s.dir, "kwk"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "#!/bin/sh\necho newer\n" {
		t.Errorf("kwk reaches %q, want the upgraded binary", got)
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Errorf("the install directory holds %v, want only kraftwerk and kwk", entries)
	}
}

func TestInstallShAddsItsDirectoryToTheShellProfileOnce(t *testing.T) {
	tests := []struct{ shell, kernel, profile, line string }{
		{"/bin/zsh", "Darwin", ".zshrc", `export PATH="$HOME/.local/bin:$PATH"`},
		{"/bin/bash", "Linux", ".bashrc", `export PATH="$HOME/.local/bin:$PATH"`},
		{"/bin/bash", "Darwin", ".bash_profile", `export PATH="$HOME/.local/bin:$PATH"`},
		{"/usr/bin/fish", "Linux", ".config/fish/conf.d/kraftwerk.fish", `fish_add_path "$HOME/.local/bin"`},
		{"/bin/sh", "Linux", ".profile", `export PATH="$HOME/.local/bin:$PATH"`},
	}
	for _, tt := range tests {
		t.Run(tt.shell+"_"+tt.kernel, func(t *testing.T) {
			release := newFakeRelease(t)
			s := newShInstall(t, tt.kernel, "arm64")
			s.extra = append(s.extra, "SHELL="+tt.shell)

			s.mustInstall(t, release)
			s.mustInstall(t, release)

			got, err := os.ReadFile(filepath.Join(s.home, filepath.FromSlash(tt.profile)))
			if err != nil {
				t.Fatal(err)
			}
			if n := strings.Count(string(got), tt.line); n != 1 {
				t.Errorf("%s holds the line %d times, want once:\n%s", tt.profile, n, got)
			}
			mustContain(t, "stdout", s.stdout, "new shell")
		})
	}
}

func TestInstallShKeepsAMacBashUsersProfileInEffect(t *testing.T) {
	release := newFakeRelease(t)
	s := newShInstall(t, "Darwin", "arm64")
	s.extra = append(s.extra, "SHELL=/bin/bash")
	writeFile(t, filepath.Join(s.home, ".profile"), "# theirs\n")

	s.mustInstall(t, release)

	if _, err := os.Stat(filepath.Join(s.home, ".bash_profile")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a new .bash_profile would stop bash reading .profile: %v", err)
	}
	mustContain(t, ".profile", readFile(t, filepath.Join(s.home, ".profile")), "# theirs", ".local/bin")
}

func TestInstallShLeavesTheProfileAloneWhenTheDirectoryIsOnThePath(t *testing.T) {
	release := newFakeRelease(t)
	s := newShInstall(t, "Linux", "x86_64")
	s.tools += string(os.PathListSeparator) + s.dir

	s.mustInstall(t, release)

	if _, err := os.Stat(filepath.Join(s.home, ".profile")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".profile was touched: %v", err)
	}
}

func TestInstallShLeavesTheProfileAloneWhenAskedTo(t *testing.T) {
	release := newFakeRelease(t)
	s := newShInstall(t, "Linux", "x86_64")
	s.extra = append(s.extra, "KRAFTWERK_NO_MODIFY_PATH=1")

	s.mustInstall(t, release)

	if _, err := os.Stat(filepath.Join(s.home, ".profile")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".profile was touched: %v", err)
	}
	mustContain(t, "stdout", s.stdout, s.dir, "PATH")
}

// psInstall is one machine running install.ps1: a fresh %LOCALAPPDATA% and a
// PATH holding nothing of the developer's. KRAFTWERK_NO_MODIFY_PATH keeps it
// from the real user PATH; the published-release smoke test covers that part.
type psInstall struct {
	shell     string
	localData string
	tools     string
	dir       string
	arch      []string
	stubs     string
	stdout    string
	stderr    string
}

func newPsInstall(t *testing.T) *psInstall {
	t.Helper()
	shell, err := exec.LookPath("powershell")
	if err != nil {
		shell, err = exec.LookPath("pwsh")
	}
	if err != nil {
		t.Skip("no PowerShell")
	}
	localData := t.TempDir()
	return &psInstall{
		shell: shell, localData: localData, tools: t.TempDir(),
		dir:  filepath.Join(localData, "Programs", "kraftwerk"),
		arch: []string{"PROCESSOR_ARCHITECTURE=AMD64"},
	}
}

// run feeds install.ps1 to Invoke-Expression, as the one-line install does.
func (p *psInstall) run(t *testing.T, release *fakeRelease, extra ...string) int {
	t.Helper()
	script, err := filepath.Abs("install.ps1")
	if err != nil {
		t.Fatal(err)
	}
	command := p.stubs + "\nGet-Content -Raw -LiteralPath '" + script + "' | Invoke-Expression"
	cmd := exec.Command(p.shell, "-NoProfile", "-NonInteractive", "-Command", command)
	cmd.Dir = p.localData
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		upper := strings.ToUpper(name)
		// PSMODULEPATH goes too: inherited from PowerShell 7, as on CI, it
		// hides Windows PowerShell's own modules, and with them Get-FileHash.
		if upper != "PATH" && upper != "PSMODULEPATH" && !strings.HasPrefix(upper, "PROCESSOR_ARCHITE") && !strings.HasPrefix(upper, "KRAFTWERK_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "PATH="+p.tools, "LOCALAPPDATA="+p.localData, "KRAFTWERK_NO_MODIFY_PATH=1", "KRAFTWERK_DOWNLOAD_URL="+release.url)
	cmd.Env = append(append(cmd.Env, p.arch...), extra...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err = cmd.Run()
	p.stdout, p.stderr = stdout.String(), stderr.String()
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatal(err)
	}
	return exit.ExitCode()
}

func (p *psInstall) mustInstall(t *testing.T, release *fakeRelease, extra ...string) {
	t.Helper()
	if code := p.run(t, release, extra...); code != 0 {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, p.stdout, p.stderr)
	}
}

func (p *psInstall) smartAppControl(state int) {
	p.stubs = fmt.Sprintf("function Get-ItemProperty { [pscustomobject]@{ VerifiedAndReputablePolicyState = %d } }", state)
}

// unwrapped is PowerShell's output without whitespace: Windows PowerShell
// wraps long lines wherever the width runs out, even inside a word.
func unwrapped(s string) string { return strings.Join(strings.Fields(s), "") }

func TestInstallPs1PicksTheArchiveAndInstallsItsBinaryUnchanged(t *testing.T) {
	tests := []struct {
		name    string
		arch    []string
		archive string
	}{
		{"x64", []string{"PROCESSOR_ARCHITECTURE=AMD64"}, "kraftwerk_windows_amd64.zip"},
		{"arm64", []string{"PROCESSOR_ARCHITECTURE=ARM64"}, "kraftwerk_windows_arm64.zip"},
		{"32-bit shell on x64", []string{"PROCESSOR_ARCHITECTURE=x86", "PROCESSOR_ARCHITEW6432=AMD64"}, "kraftwerk_windows_amd64.zip"},
		{"x64 shell on arm64", []string{"PROCESSOR_ARCHITECTURE=AMD64", "PROCESSOR_ARCHITEW6432=ARM64"}, "kraftwerk_windows_arm64.zip"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			release := newFakeRelease(t)
			p := newPsInstall(t)
			p.arch = tt.arch

			p.mustInstall(t, release)

			got := readFile(t, filepath.Join(p.dir, "kraftwerk.exe"))
			if got != string(release.binaries[tt.archive]) {
				t.Errorf("installed binary = %q, want the one from %s", got, tt.archive)
			}
		})
	}
}

func TestInstallPs1RefusesAnArchiveWhoseChecksumDiffers(t *testing.T) {
	release := newFakeRelease(t)
	release.files["kraftwerk_windows_amd64.zip"] = zipWith(t, "kraftwerk.exe", []byte("tampered"))
	p := newPsInstall(t)

	if code := p.run(t, release); code == 0 {
		t.Fatalf("a tampered archive installed\nstdout: %s", p.stdout)
	}
	mustContain(t, "stderr", unwrapped(p.stderr), "checksum")
	if _, err := os.Stat(filepath.Join(p.dir, "kraftwerk.exe")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("kraftwerk was installed anyway: %v", err)
	}
}

func TestInstallPs1GetsThePinnedVersion(t *testing.T) {
	release := newFakeRelease(t)
	p := newPsInstall(t)

	p.mustInstall(t, release, "KRAFTWERK_VERSION=1.2.3")

	for _, path := range release.requests {
		if !strings.HasPrefix(path, "/download/v1.2.3/") {
			t.Errorf("asked for %s, want it under /download/v1.2.3/", path)
		}
	}
}

func TestInstallPs1AddsKwkAndRefreshesItOnUpgrade(t *testing.T) {
	release := newFakeRelease(t)
	p := newPsInstall(t)
	p.mustInstall(t, release)
	if got := readFile(t, filepath.Join(p.dir, "kwk.exe")); got != string(release.binaries["kraftwerk_windows_amd64.zip"]) {
		t.Errorf("kwk.exe = %q, want kraftwerk", got)
	}

	release.replace(t, "kraftwerk_windows_amd64.zip", []byte("newer"))
	p.mustInstall(t, release)

	if got := readFile(t, filepath.Join(p.dir, "kwk.exe")); got != "newer" {
		t.Errorf("kwk.exe = %q after the upgrade, want the new binary", got)
	}
}

func TestInstallPs1LeavesAKwkAlreadyOnThePathAlone(t *testing.T) {
	release := newFakeRelease(t)
	p := newPsInstall(t)
	name := "kwk"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(p.tools, name), []byte("theirs"), 0o700); err != nil { //nolint:gosec // a command PowerShell has to find
		t.Fatal(err)
	}

	p.mustInstall(t, release)

	if _, err := os.Stat(filepath.Join(p.dir, "kwk.exe")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a second kwk was added: %v", err)
	}
	mustContain(t, "stdout", unwrapped(p.stdout), "Notaddingkwk")
}

func TestInstallPs1StopsWhereSmartAppControlWouldBlockTheBinary(t *testing.T) {
	release := newFakeRelease(t)
	p := newPsInstall(t)
	p.smartAppControl(1)

	if code := p.run(t, release); code == 0 {
		t.Fatalf("installed under Smart App Control\nstdout: %s", p.stdout)
	}
	mustContain(t, "stderr", unwrapped(p.stderr), "SmartAppControl", "signed")
	if len(release.requests) != 0 {
		t.Errorf("downloaded %v anyway", release.requests)
	}
}

func TestInstallPs1WarnsWhileSmartAppControlIsEvaluating(t *testing.T) {
	release := newFakeRelease(t)
	p := newPsInstall(t)
	p.smartAppControl(2)

	p.mustInstall(t, release)

	if _, err := os.Stat(filepath.Join(p.dir, "kraftwerk.exe")); err != nil {
		t.Error(err)
	}
	mustContain(t, "output", unwrapped(p.stdout+p.stderr), "SmartAppControl", "signed")
}
