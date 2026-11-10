package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/Marcel-Goehn/fliesswerk/internal/config"
	semver "github.com/Marcel-Goehn/fliesswerk/internal/version"
)

const (
	// One page holds the newest 100 releases, which is far more than a
	// supported major ever falls behind by.
	githubReleasesAPI  = "https://api.github.com/repos/Marcel-Goehn/fliesswerk/releases?per_page=100"
	githubDownloadBase = "https://github.com/Marcel-Goehn/fliesswerk/releases"

	updateTimeout  = 10 * time.Minute
	noticeTimeout  = 2 * time.Second
	noticeInterval = 24 * time.Hour
	// maxDownload bounds what a mirror can make `update` read into memory.
	maxDownload = 1 << 30
)

// runUpdate is `fliesswerk update [--major]`.
func runUpdate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("fliesswerk update", flag.ContinueOnError)
	fs.SetOutput(stderr)
	major := fs.Bool("major", false, "move to the newest major version, whose config may need `fliesswerk migrate`")
	if err := fs.Parse(args); err != nil {
		return exitConfigError
	}
	if fs.NArg() > 0 {
		_, _ = fmt.Fprintf(stderr, "fliesswerk update: unexpected argument %q\n", fs.Arg(0))
		return exitConfigError
	}
	current, prerelease, ok := semver.Parse(version)
	if !ok {
		_, _ = fmt.Fprintln(stderr, "fliesswerk update: this is a development build, which no release replaces; install one with the install script (docs/install.md)")
		return exitConfigError
	}
	fail := func(err error) int {
		_, _ = fmt.Fprintf(stderr, "fliesswerk update: %v\n", err)
		return exitFailures
	}

	ctx, cancel := context.WithTimeout(context.Background(), updateTimeout)
	defer cancel()
	releases, err := fetchReleases(ctx)
	if err != nil {
		return fail(err)
	}
	pin := projectPin()
	if pin != "" {
		_, _ = fmt.Fprintf(stdout, "fliesswerk update: this project sets factory = %q, so only releases it allows count\n", pin)
	}
	u := findUpgrades(allowedByPin(releases, pin), current, prerelease)

	if *major {
		if u.newerMajor == nil {
			_, _ = fmt.Fprintf(stdout, "fliesswerk update: no newer major than %d is out; `fliesswerk update` looks for releases within it\n", current.Major())
			return exitOK
		}
		if err := install(ctx, *u.newerMajor); err != nil {
			return fail(err)
		}
		_, _ = fmt.Fprintf(stdout, "fliesswerk update: updated from %s to %s; run `fliesswerk migrate` in each project to bring its config along\n", current, *u.newerMajor)
		return exitOK
	}

	if u.inMajor != nil {
		if err := install(ctx, *u.inMajor); err != nil {
			return fail(err)
		}
		_, _ = fmt.Fprintf(stdout, "fliesswerk update: updated from %s to %s\n", current, *u.inMajor)
	} else {
		_, _ = fmt.Fprintf(stdout, "fliesswerk update: %s is up to date within major %d\n", current, current.Major())
	}
	if u.newerMajor != nil {
		_, _ = fmt.Fprintf(stdout, "fliesswerk update: %s is out as well; `fliesswerk update --major` moves to it\n", *u.newerMajor)
	}
	return exitOK
}

// projectPin is the `factory` pin of the project the command runs in, if any.
func projectPin() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	pin, _ := config.Load(projectRoot(cwd), nil).Values["factory"].(string)
	return pin
}

// allowedByPin keeps the releases a `factory` pin allows, so that an update
// can't land outside it. A pin that isn't a range is validation's to report.
func allowedByPin(releases []semver.Version, pin string) []semver.Version {
	r, err := semver.ParseRange(pin)
	if err != nil {
		return releases
	}
	return slices.DeleteFunc(slices.Clone(releases), func(v semver.Version) bool { return !r.Contains(v) })
}

// upgrades are the releases a running version could move to.
type upgrades struct {
	inMajor    *semver.Version // the newest newer release of its major
	newerMajor *semver.Version // the newest release of any higher major
}

// findUpgrades picks them from the published releases. A prerelease is older
// than the release of the same number.
func findUpgrades(releases []semver.Version, current semver.Version, prerelease bool) upgrades {
	var u upgrades
	for _, v := range releases {
		switch {
		case v.Major() > current.Major():
			if u.newerMajor == nil || u.newerMajor.Less(v) {
				u.newerMajor = &v
			}
		case v.Major() == current.Major() && (current.Less(v) || v == current && prerelease):
			if u.inMajor == nil || u.inMajor.Less(v) {
				u.inMajor = &v
			}
		}
	}
	return u
}

// fetchReleases lists the published, final releases.
// FLIESSWERK_RELEASES_API replaces GitHub's API, the way
// FLIESSWERK_DOWNLOAD_URL does its downloads. FLIESSWERK_GITHUB_TOKEN raises
// GitHub's rate limit for machines that share an address; it only ever goes
// to GitHub.
func fetchReleases(ctx context.Context) ([]semver.Version, error) {
	url, token := os.Getenv("FLIESSWERK_RELEASES_API"), ""
	if url == "" {
		url, token = githubReleasesAPI, os.Getenv("FLIESSWERK_GITHUB_TOKEN")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil) //nolint:gosec // GitHub, or the mirror its user set
	if err != nil {
		return nil, fmt.Errorf("listing the releases: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // GitHub, or the mirror its user set
	if err != nil {
		return nil, fmt.Errorf("listing the releases: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("listing the releases: the server answered %d, which GitHub does at its rate limit; set FLIESSWERK_GITHUB_TOKEN to a token to raise it", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("listing the releases: the server answered %d", resp.StatusCode)
	}
	var list []struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxDownload)).Decode(&list); err != nil {
		return nil, fmt.Errorf("listing the releases: %w", err)
	}
	var releases []semver.Version
	for _, r := range list {
		v, pre, ok := semver.Parse(r.Tag)
		if ok && !pre && !r.Draft && !r.Prerelease {
			releases = append(releases, v)
		}
	}
	return releases, nil
}

// install downloads the release, verifies it and swaps it in for the
// executable that is running.
func install(ctx context.Context, v semver.Version) error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("can't find this executable: %w", err)
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return fmt.Errorf("can't find this executable: %w", err)
	}

	base := os.Getenv("FLIESSWERK_DOWNLOAD_URL")
	if base == "" {
		base = githubDownloadBase
	}
	url := base + "/download/v" + v.String() + "/"
	archive := "fliesswerk_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
	if runtime.GOOS == "windows" {
		archive = strings.TrimSuffix(archive, ".tar.gz") + ".zip"
	}
	data, err := download(ctx, url+archive)
	if err != nil {
		return err
	}
	checksums, err := download(ctx, url+"checksums.txt")
	if err != nil {
		return err
	}
	if err := verifyChecksum(data, archive, checksums); err != nil {
		return err
	}
	binary, err := extractBinary(data, archive)
	if err != nil {
		return err
	}
	return replaceExecutable(exe, binary)
}

func download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil) //nolint:gosec // GitHub, or the mirror its user set
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", url, err)
	}
	resp, err := http.DefaultClient.Do(req) //nolint:gosec // GitHub, or the mirror its user set
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("downloading %s: the server answered %d; nothing was replaced", url, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDownload))
	if err != nil {
		return nil, fmt.Errorf("downloading %s: %w", url, err)
	}
	return data, nil
}

// verifyChecksum stops unless data's SHA-256 is the one checksums lists for
// name.
func verifyChecksum(data []byte, name string, checksums []byte) error {
	for line := range strings.SplitSeq(string(checksums), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[1] != name {
			continue
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != fields[0] {
			return fmt.Errorf("the checksum of %s doesn't match checksums.txt, so the download is damaged or was tampered with; nothing was replaced", name)
		}
		return nil
	}
	return fmt.Errorf("checksums.txt doesn't list %s, so it can't be verified; nothing was replaced", name)
}

// extractBinary reads the executable out of a release archive, into memory.
func extractBinary(data []byte, archive string) ([]byte, error) {
	if strings.HasSuffix(archive, ".zip") {
		return extractZip(data, archive)
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", archive, err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s holds no fliesswerk executable; nothing was replaced", archive)
		}
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", archive, err)
		}
		if h.Typeflag == tar.TypeReg && filepath.Base(h.Name) == "fliesswerk" {
			return readBinary(tr, archive)
		}
	}
}

func extractZip(data []byte, archive string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", archive, err)
	}
	for _, f := range zr.File {
		if filepath.Base(f.Name) != "fliesswerk.exe" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", archive, err)
		}
		defer func() { _ = rc.Close() }()
		return readBinary(rc, archive)
	}
	return nil, fmt.Errorf("%s holds no fliesswerk executable; nothing was replaced", archive)
}

func readBinary(r io.Reader, archive string) ([]byte, error) {
	binary, err := io.ReadAll(io.LimitReader(r, maxDownload))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", archive, err)
	}
	return binary, nil
}

// replaceExecutable puts binary in exe's place, byte for byte: changing a
// macOS binary after the build breaks its signature. A fwk beside it that is
// a hard link or a copy, as install.ps1 makes it, is renewed too.
func replaceExecutable(exe string, binary []byte) error {
	aliases := aliasesOf(exe)
	tmp, err := os.CreateTemp(filepath.Dir(exe), ".fliesswerk-update-*")
	if err != nil {
		return fmt.Errorf("can't write next to %s: %w", exe, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, err = tmp.Write(binary)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("can't write next to %s: %w", exe, err)
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil { //nolint:gosec // an executable for its user to run
		return fmt.Errorf("can't make the new executable runnable: %w", err)
	}
	if err := swapIn(tmp.Name(), exe); err != nil {
		return fmt.Errorf("can't replace %s: %w", exe, err)
	}
	for _, alias := range aliases {
		if err := renewAlias(exe, alias, binary); err != nil {
			return fmt.Errorf("updated %s, but can't replace %s: %w; run the update again", exe, alias, err)
		}
	}
	return nil
}

// swapIn moves src to dst. Unix renames over a running executable at once.
// Windows won't, but it does let one be renamed, so the old one steps aside
// and is cleaned up by the next update.
func swapIn(src, dst string) error {
	if runtime.GOOS != "windows" {
		return os.Rename(src, dst)
	}
	old := dst + ".old"
	if err := os.Remove(old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(dst, old); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err != nil {
		return errors.Join(err, os.Rename(old, dst))
	}
	return nil
}

// aliasesOf finds the other name of the install beside exe, if it is the same
// program as a regular file rather than a symlink.
func aliasesOf(exe string) []string {
	info, err := os.Lstat(exe)
	if err != nil {
		return nil
	}
	dir, ext := filepath.Dir(exe), filepath.Ext(exe)
	var aliases []string
	for _, name := range []string{"fliesswerk" + ext, "fwk" + ext} {
		path := filepath.Join(dir, name)
		other, err := os.Lstat(path)
		if err != nil || path == exe || !other.Mode().IsRegular() {
			continue
		}
		if os.SameFile(info, other) || sameContent(exe, path, info, other) {
			aliases = append(aliases, path)
		}
	}
	return aliases
}

func sameContent(a, b string, ai, bi os.FileInfo) bool {
	if ai.Size() != bi.Size() {
		return false
	}
	ad, aerr := os.ReadFile(a) //nolint:gosec // this executable
	bd, berr := os.ReadFile(b) //nolint:gosec // the file beside it
	return aerr == nil && berr == nil && bytes.Equal(ad, bd)
}

// renewAlias makes alias the new binary again: a hard link where the file
// system has them, otherwise a copy.
func renewAlias(exe, alias string, binary []byte) error {
	tmp := alias + ".new"
	_ = os.Remove(tmp)
	if err := os.Link(exe, tmp); err != nil {
		if err := os.WriteFile(tmp, binary, 0o755); err != nil { //nolint:gosec // an executable for its user to run
			return err
		}
	}
	defer func() { _ = os.Remove(tmp) }()
	return swapIn(tmp, alias)
}

// noticeNewVersion says once a day, at most, that a newer release is out. It
// never gets in the command's way: any trouble just means no notice.
func noticeNewVersion(values map[string]any, stderr io.Writer) {
	current, prerelease, ok := semver.Parse(version)
	if !ok || values["update_notice"] == false || inCI() || !noticeIsDue() {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), noticeTimeout)
	defer cancel()
	releases, err := fetchReleases(ctx)
	if err != nil {
		return
	}
	pin, _ := values["factory"].(string)
	u := findUpgrades(allowedByPin(releases, pin), current, prerelease)
	switch {
	case u.inMajor != nil:
		printNotice(stderr, *u.inMajor, current, "fliesswerk update")
	case u.newerMajor != nil:
		printNotice(stderr, *u.newerMajor, current, "fliesswerk update --major")
	}
}

func printNotice(stderr io.Writer, newest, current semver.Version, command string) {
	_, _ = fmt.Fprintf(stderr, "notice: fliesswerk %s is available (this is %s); run `%s`. Set `update_notice = false` in the config to turn this off\n", newest, current, command)
}

// inCI reports whether a CI system is running this. CI is the common signal;
// the others are for the systems that don't set it.
func inCI() bool {
	if v := os.Getenv("CI"); v != "" && v != "false" && v != "0" {
		return true
	}
	for _, name := range []string{"JENKINS_URL", "TF_BUILD", "TEAMCITY_VERSION"} {
		if os.Getenv(name) != "" {
			return true
		}
	}
	return false
}

// noticeIsDue reports whether the last check is a day old, and records this
// one. Where no record can be kept the answer is no, since a check on every
// command is what the record is there to prevent.
func noticeIsDue() bool {
	cache, err := os.UserCacheDir()
	if err != nil {
		return false
	}
	stamp := filepath.Join(cache, "fliesswerk", "update-check")
	if info, err := os.Stat(stamp); err == nil && time.Since(info.ModTime()) < noticeInterval {
		return false
	}
	if err := os.MkdirAll(filepath.Dir(stamp), 0o700); err != nil {
		return false
	}
	if err := os.WriteFile(stamp, nil, 0o600); err != nil {
		return false
	}
	// WriteFile on an existing empty file may leave its time alone.
	now := time.Now()
	return os.Chtimes(stamp, now, now) == nil
}
