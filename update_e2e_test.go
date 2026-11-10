package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// thisArchive is the release archive for the OS the test runs on.
func thisArchive() string {
	name := "kraftwerk_" + runtime.GOOS + "_" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		return name + ".zip"
	}
	return name + ".tar.gz"
}

type fakeTag struct {
	tag               string
	draft, prerelease bool
}

// publish makes the fake release server list these releases, and gives this
// OS's archive the bytes of a new binary.
func publish(t *testing.T, r *fakeRelease, binary []byte, tags ...fakeTag) {
	t.Helper()
	var list []map[string]any
	for _, tag := range tags {
		list = append(list, map[string]any{"tag_name": tag.tag, "draft": tag.draft, "prerelease": tag.prerelease})
	}
	data, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	r.files["releases"] = data
	r.replace(t, thisArchive(), binary)
}

func (r *fakeRelease) requested() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.requests)
}

// updateEnv is a machine that reaches the fake release server instead of
// GitHub, with a home of its own and no sign of CI.
func updateEnv(t *testing.T, r *fakeRelease, home string, extra ...string) []string {
	t.Helper()
	var env []string
	for _, kv := range ghEnv(t, "ok") {
		name, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(name) {
		case "CI", "JENKINS_URL", "TF_BUILD", "TEAMCITY_VERSION":
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"KRAFTWERK_RELEASES_API="+r.url+"/releases", "KRAFTWERK_DOWNLOAD_URL="+r.url,
		"HOME="+home, "USERPROFILE="+home, "XDG_CACHE_HOME="+filepath.Join(home, ".cache"), "LocalAppData="+filepath.Join(home, "local"))
	return append(env, extra...)
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func entriesOf(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func newBinary() []byte { return []byte("the new kraftwerk\n") }

func TestUpdateInstallsTheNewestReleaseOfTheCurrentMajor(t *testing.T) {
	bin := buildKraftwerk(t, "-X main.version=1.2.0")
	r := newFakeRelease(t)
	publish(t, r, newBinary(),
		fakeTag{tag: "v1.3.0"}, fakeTag{tag: "v2.0.0"}, fakeTag{tag: "v1.4.0"}, fakeTag{tag: "v1.1.0"},
		fakeTag{tag: "v1.5.0-rc.1", prerelease: true}, fakeTag{tag: "v1.9.0", draft: true}, fakeTag{tag: "nightly"})

	stdout, stderr, code := runEnv(t, bin, t.TempDir(), updateEnv(t, r, t.TempDir()), "update")

	if code != exitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if got := mustRead(t, bin); !bytes.Equal(got, newBinary()) {
		t.Fatalf("the binary is %d bytes that aren't the release's", len(got))
	}
	mustContain(t, "stdout", stdout, "1.4.0", "`kraftwerk update --major`", "2.0.0")
	mustContain(t, "requests", strings.Join(r.requested(), "\n"), "/download/v1.4.0/"+thisArchive(), "/download/v1.4.0/checksums.txt")

	want := []string{filepath.Base(bin)}
	if runtime.GOOS == "windows" {
		want = append(want, filepath.Base(bin)+".old") // the running one, renamed
	} else if info, err := os.Stat(bin); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Errorf("the new binary isn't executable: %v, %v", info, err)
	}
	if got := entriesOf(t, filepath.Dir(bin)); !slices.Equal(got, want) {
		t.Errorf("the directory holds %v, want %v", got, want)
	}
}

func TestUpdateOnWindowsKeepsTheRunningExecutableUnderAnotherName(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("only Windows refuses to replace a running executable")
	}
	bin := buildKraftwerk(t, "-X main.version=1.2.0")
	running := mustRead(t, bin)
	if err := os.WriteFile(bin+".old", []byte("an earlier update's leftover"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := newFakeRelease(t)
	publish(t, r, newBinary(), fakeTag{tag: "v1.3.0"})

	stdout, stderr, code := runEnv(t, bin, t.TempDir(), updateEnv(t, r, t.TempDir()), "update")

	if code != exitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if got := mustRead(t, bin); !bytes.Equal(got, newBinary()) {
		t.Error("the binary wasn't replaced")
	}
	if got := mustRead(t, bin+".old"); !bytes.Equal(got, running) {
		t.Error("the running executable wasn't renamed to .old")
	}
}

func TestUpdateWhenAlreadyNewestChangesNothing(t *testing.T) {
	bin := buildKraftwerk(t, "-X main.version=1.2.0")
	before := mustRead(t, bin)
	r := newFakeRelease(t)
	publish(t, r, newBinary(), fakeTag{tag: "v1.2.0"}, fakeTag{tag: "v1.1.9"})

	stdout, stderr, code := runEnv(t, bin, t.TempDir(), updateEnv(t, r, t.TempDir()), "update")

	if code != exitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	mustContain(t, "stdout", stdout, "up to date")
	if !bytes.Equal(mustRead(t, bin), before) {
		t.Error("the binary changed")
	}
	if got := r.requested(); len(got) != 1 {
		t.Errorf("requests = %v, want only the release list", got)
	}
}

func TestUpdateFromAPrereleaseMovesToItsRelease(t *testing.T) {
	bin := buildKraftwerk(t, "-X main.version=1.3.0-rc.1")
	r := newFakeRelease(t)
	publish(t, r, newBinary(), fakeTag{tag: "v1.3.0"})

	stdout, stderr, code := runEnv(t, bin, t.TempDir(), updateEnv(t, r, t.TempDir()), "update")

	if code != exitOK || !bytes.Equal(mustRead(t, bin), newBinary()) {
		t.Fatalf("exit code = %d, want the release installed\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
}

func TestUpdateRefusesADownloadThatFailsItsChecksum(t *testing.T) {
	cases := []struct {
		name   string
		damage func(r *fakeRelease)
		want   string
	}{
		{"tampered archive", func(r *fakeRelease) { r.files[thisArchive()] = append(slices.Clone(r.files[thisArchive()]), 0) }, "doesn't match"},
		{"not listed", func(r *fakeRelease) { r.files["checksums.txt"] = []byte("00  other.tar.gz\n") }, "doesn't list"},
		{"no checksums", func(r *fakeRelease) { delete(r.files, "checksums.txt") }, "checksums.txt"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bin := buildKraftwerk(t, "-X main.version=1.2.0")
			before := mustRead(t, bin)
			r := newFakeRelease(t)
			publish(t, r, newBinary(), fakeTag{tag: "v1.3.0"})
			c.damage(r)

			stdout, stderr, code := runEnv(t, bin, t.TempDir(), updateEnv(t, r, t.TempDir()), "update")

			if code != exitFailures {
				t.Fatalf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, exitFailures, stdout, stderr)
			}
			mustContain(t, "stderr", stderr, c.want)
			if !bytes.Equal(mustRead(t, bin), before) {
				t.Error("the binary changed")
			}
			if got := entriesOf(t, filepath.Dir(bin)); len(got) != 1 {
				t.Errorf("the directory holds %v, want only the binary", got)
			}
		})
	}
}

func TestUpdateMajorMovesToTheNewestMajorAndPointsToMigrate(t *testing.T) {
	bin := buildKraftwerk(t, "-X main.version=1.9.0")
	r := newFakeRelease(t)
	publish(t, r, newBinary(), fakeTag{tag: "v1.9.5"}, fakeTag{tag: "v3.1.0"}, fakeTag{tag: "v2.4.0"})

	stdout, stderr, code := runEnv(t, bin, t.TempDir(), updateEnv(t, r, t.TempDir()), "update", "--major")

	if code != exitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !bytes.Equal(mustRead(t, bin), newBinary()) {
		t.Error("the binary wasn't replaced")
	}
	mustContain(t, "stdout", stdout, "3.1.0", "`kraftwerk migrate`")
	mustContain(t, "requests", strings.Join(r.requested(), "\n"), "/download/v3.1.0/")
}

func TestUpdateMajorWithNoNewerMajorChangesNothing(t *testing.T) {
	bin := buildKraftwerk(t, "-X main.version=1.2.0")
	before := mustRead(t, bin)
	r := newFakeRelease(t)
	publish(t, r, newBinary(), fakeTag{tag: "v1.9.0"})

	stdout, stderr, code := runEnv(t, bin, t.TempDir(), updateEnv(t, r, t.TempDir()), "update", "--major")

	if code != exitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	mustContain(t, "stdout", stdout, "no newer major", "`kraftwerk update`")
	if !bytes.Equal(mustRead(t, bin), before) {
		t.Error("the binary changed")
	}
}

func TestUpdateInsideAProjectStaysWithinItsFactoryPin(t *testing.T) {
	cases := []struct {
		name, version, pin string
		args               []string
		want               string
	}{
		{"within the major", "1.2.0", "~1.3", []string{"update"}, "1.3.5"},
		{"to a new major", "1.9.5", "2", []string{"update", "--major"}, "2.4.0"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bin := buildKraftwerk(t, "-X main.version="+c.version)
			root := projectDir(t, map[string]string{"config.toml": "version = 1\nfactory = \"" + c.pin + "\"\n"})
			r := newFakeRelease(t)
			publish(t, r, newBinary(), fakeTag{tag: "v1.3.5"}, fakeTag{tag: "v1.9.9"}, fakeTag{tag: "v2.4.0"}, fakeTag{tag: "v3.1.0"})

			stdout, stderr, code := runEnv(t, bin, root, updateEnv(t, r, t.TempDir()), c.args...)

			if code != exitOK {
				t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
			}
			mustContain(t, "stdout", stdout, c.want, "factory = \""+c.pin+"\"")
			mustContain(t, "requests", strings.Join(r.requested(), "\n"), "/download/v"+c.want+"/")
		})
	}
}

func TestUpdateRenewsAKwkThatIsAHardLinkOrACopy(t *testing.T) {
	for _, kind := range []string{"hard link", "copy"} {
		t.Run(kind, func(t *testing.T) {
			bin := buildKraftwerk(t, "-X main.version=1.2.0")
			kwk := filepath.Join(filepath.Dir(bin), "kwk"+filepath.Ext(bin))
			if kind == "hard link" {
				if err := os.Link(bin, kwk); err != nil {
					t.Skipf("no hard links here: %v", err)
				}
			} else if err := os.WriteFile(kwk, mustRead(t, bin), 0o700); err != nil { //nolint:gosec // an executable the test runs
				t.Fatal(err)
			}
			r := newFakeRelease(t)
			publish(t, r, newBinary(), fakeTag{tag: "v1.3.0"})

			stdout, stderr, code := runEnv(t, kwk, t.TempDir(), updateEnv(t, r, t.TempDir()), "update")

			if code != exitOK {
				t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
			}
			for _, p := range []string{bin, kwk} {
				if !bytes.Equal(mustRead(t, p), newBinary()) {
					t.Errorf("%s wasn't replaced", p)
				}
			}
		})
	}
}

func TestUpdateThroughASymlinkReplacesTheTargetAndLeavesTheLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows install makes no symlink")
	}
	bin := buildKraftwerk(t, "-X main.version=1.2.0")
	kwk := filepath.Join(filepath.Dir(bin), "kwk")
	if err := os.Symlink("kraftwerk", kwk); err != nil {
		t.Fatal(err)
	}
	r := newFakeRelease(t)
	publish(t, r, newBinary(), fakeTag{tag: "v1.3.0"})

	stdout, stderr, code := runEnv(t, kwk, t.TempDir(), updateEnv(t, r, t.TempDir()), "update")

	if code != exitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !bytes.Equal(mustRead(t, bin), newBinary()) {
		t.Error("the target wasn't replaced")
	}
	if target, err := os.Readlink(kwk); err != nil || target != "kraftwerk" {
		t.Errorf("kwk is no longer the symlink: %q, %v", target, err)
	}
}

func TestUpdateFailures(t *testing.T) {
	t.Run("a development build has no release to update from", func(t *testing.T) {
		bin := buildKraftwerk(t, "")
		r := newFakeRelease(t)
		_, stderr, code := runEnv(t, bin, t.TempDir(), updateEnv(t, r, t.TempDir()), "update")
		if code != exitConfigError {
			t.Fatalf("exit code = %d, want %d; stderr: %s", code, exitConfigError, stderr)
		}
		mustContain(t, "stderr", stderr, "development build")
		if got := r.requested(); len(got) != 0 {
			t.Errorf("requests = %v, want none", got)
		}
	})
	t.Run("the release list can't be had", func(t *testing.T) {
		bin := buildKraftwerk(t, "-X main.version=1.2.0")
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
		t.Cleanup(srv.Close)
		env := append(updateEnv(t, newFakeRelease(t), t.TempDir()), "KRAFTWERK_RELEASES_API="+srv.URL)
		_, stderr, code := runEnv(t, bin, t.TempDir(), env, "update")
		if code != exitFailures {
			t.Fatalf("exit code = %d, want %d; stderr: %s", code, exitFailures, stderr)
		}
		mustContain(t, "stderr", stderr, "403", "KRAFTWERK_GITHUB_TOKEN")
	})
	t.Run("an unexpected argument", func(t *testing.T) {
		bin := buildKraftwerk(t, "-X main.version=1.2.0")
		_, stderr, code := runEnv(t, bin, t.TempDir(), updateEnv(t, newFakeRelease(t), t.TempDir()), "update", "now")
		if code != exitConfigError {
			t.Fatalf("exit code = %d, want %d; stderr: %s", code, exitConfigError, stderr)
		}
		mustContain(t, "stderr", stderr, `"now"`)
	})
}

// ageNoticeStamp makes the last check look a day and a bit old.
func ageNoticeStamp(t *testing.T, home string) {
	t.Helper()
	found := false
	err := filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Name() == "update-check" {
			found = true
			old := time.Now().Add(-25 * time.Hour)
			return os.Chtimes(p, old, old) //nolint:gosec // the test's own temp dir
		}
		return err
	})
	if err != nil || !found {
		t.Fatalf("no update-check stamp under %s: %v", home, err)
	}
}

const noticeSays = "is available"

func TestNoticeAppearsAtMostOnceADay(t *testing.T) {
	bin := buildKraftwerk(t, "-X main.version=1.2.0")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})
	r := newFakeRelease(t)
	publish(t, r, newBinary(), fakeTag{tag: "v1.4.0"})
	home := t.TempDir()
	env := updateEnv(t, r, home)

	stdout, stderr, code := runEnv(t, bin, root, env, checkNoSandbox...)
	if code != exitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	mustContain(t, "stderr", stderr, "1.4.0 "+noticeSays, "`kraftwerk update`", "update_notice = false")
	if strings.Contains(stdout, noticeSays) {
		t.Errorf("the notice is on stdout, which belongs to the command: %s", stdout)
	}

	before := len(r.requested())
	_, stderr, _ = runEnv(t, bin, root, env, checkNoSandbox...)
	if strings.Contains(stderr, noticeSays) || len(r.requested()) != before {
		t.Errorf("a second notice the same day: requests %v, stderr: %s", r.requested(), stderr)
	}

	ageNoticeStamp(t, home)
	_, stderr, _ = runEnv(t, bin, root, env, checkNoSandbox...)
	mustContain(t, "stderr", stderr, "1.4.0 "+noticeSays)
}

func TestNoticePointsToUpdateMajorWhenOnlyANewMajorIsOut(t *testing.T) {
	bin := buildKraftwerk(t, "-X main.version=1.2.0")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})
	r := newFakeRelease(t)
	publish(t, r, newBinary(), fakeTag{tag: "v2.0.0"}, fakeTag{tag: "v1.2.0"})

	_, stderr, _ := runEnv(t, bin, root, updateEnv(t, r, t.TempDir()), checkNoSandbox...)

	mustContain(t, "stderr", stderr, "2.0.0 "+noticeSays, "`kraftwerk update --major`")
}

func TestNoNotice(t *testing.T) {
	cases := []struct {
		name, ldflags, config string
		env                   []string
		releases              []fakeTag
		wantRequests          bool
	}{
		{name: "turned off by its key", ldflags: "-X main.version=1.2.0", config: "version = 1\nupdate_notice = false\n", releases: []fakeTag{{tag: "v1.4.0"}}},
		{name: "in CI", ldflags: "-X main.version=1.2.0", config: "version = 1\n", env: []string{"CI=true"}, releases: []fakeTag{{tag: "v1.4.0"}}},
		{name: "in Jenkins, which doesn't set CI", ldflags: "-X main.version=1.2.0", config: "version = 1\n", env: []string{"JENKINS_URL=http://ci.example"}, releases: []fakeTag{{tag: "v1.4.0"}}},
		{name: "nothing newer within the pin", ldflags: "-X main.version=1.2.0", config: "version = 1\nfactory = \"~1.2\"\n", releases: []fakeTag{{tag: "v1.9.0"}}, wantRequests: true},
		{name: "a development build", ldflags: "", config: "version = 1\n", releases: []fakeTag{{tag: "v1.4.0"}}},
		{name: "already the newest", ldflags: "-X main.version=1.4.0", config: "version = 1\n", releases: []fakeTag{{tag: "v1.4.0"}}, wantRequests: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bin := buildKraftwerk(t, c.ldflags)
			root := projectDir(t, map[string]string{"config.toml": c.config})
			r := newFakeRelease(t)
			publish(t, r, newBinary(), c.releases...)

			stdout, stderr, code := runEnv(t, bin, root, updateEnv(t, r, t.TempDir(), c.env...), checkNoSandbox...)

			if code != exitOK {
				t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
			}
			if strings.Contains(stderr, noticeSays) {
				t.Errorf("a notice: %s", stderr)
			}
			if got := r.requested(); (len(got) > 0) != c.wantRequests {
				t.Errorf("requests = %v, want some: %v", got, c.wantRequests)
			}
		})
	}
}

func TestNoticeThatCantReachGitHubStaysQuiet(t *testing.T) {
	bin := buildKraftwerk(t, "-X main.version=1.2.0")
	root := projectDir(t, map[string]string{"config.toml": "version = 1\n"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) }))
	t.Cleanup(srv.Close)
	env := append(updateEnv(t, newFakeRelease(t), t.TempDir()), "KRAFTWERK_RELEASES_API="+srv.URL)

	stdout, stderr, code := runEnv(t, bin, root, env, checkNoSandbox...)

	if code != exitOK {
		t.Fatalf("exit code = %d\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if strings.Contains(stderr, "502") || strings.Contains(stderr, noticeSays) {
		t.Errorf("stderr mentions the check: %s", stderr)
	}
}
