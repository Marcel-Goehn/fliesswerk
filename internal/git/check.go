package git

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"runtime"

	"github.com/Marcel-Goehn/kraftwerk/internal/version"
)

// MinVersion is the oldest git the Factory works with: the first whose
// `merge-tree` has --write-tree.
const MinVersion = "2.38.0"

var versionLine = regexp.MustCompile(`git version (\d+\.\d+\.\d+)`)

// ReadVersion is the version in what `git --version` printed.
func ReadVersion(output string) (string, error) {
	m := versionLine.FindStringSubmatch(output)
	if m == nil {
		return "", fmt.Errorf("can't read git's version from %q", output)
	}
	return m[1], nil
}

// CheckVersion verifies git is installed at MinVersion or newer.
func CheckVersion(ctx context.Context) error {
	out, err := exec.CommandContext(ctx, "git", "--version").Output()
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("git is not installed; the Factory needs it. Install it: %s", installHint())
	}
	if err != nil {
		return fmt.Errorf("`git --version` failed: %w", err)
	}
	have, err := ReadVersion(string(out))
	if err != nil {
		return err
	}
	if !version.AtLeast(have, MinVersion) {
		return fmt.Errorf("git %s is older than %s, the first whose `merge-tree` the Factory can use. Upgrade it: %s", have, MinVersion, installHint())
	}
	return nil
}

// installHint names the command that installs or upgrades git on this OS.
func installHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "`brew install git`"
	case "windows":
		return "`winget install --id Git.Git`"
	default:
		return "your distribution's package (`sudo apt install git`, `sudo dnf install git`), or https://git-scm.com/downloads/linux if it is too old"
	}
}

// DryRunPush asks remote whether this machine's git credentials may create
// the branch dst from the local ref src, without creating it.
func (r Repo) DryRunPush(ctx context.Context, remote, src, dst string) error {
	_, err := r.output(ctx, "push", "--dry-run", "--quiet", "--porcelain", remote, src+":refs/heads/"+dst)
	return err
}
