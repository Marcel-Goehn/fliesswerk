package github

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
)

var versionLine = regexp.MustCompile(`gh version (\d+)\.(\d+)\.(\d+)`)

// checkVersion verifies gh is installed at MinVersion or newer.
func (t *Tracker) checkVersion(ctx context.Context) error {
	out, err := t.opts.Runner.Run(ctx, nil, "--version")
	if errors.Is(err, ErrNotInstalled) {
		return fmt.Errorf("gh is not installed; the GitHub Tracker needs it. Install it: %s", t.installHint(false))
	}
	if err != nil {
		return fmt.Errorf("`gh --version` failed: %w", err)
	}
	m := versionLine.FindStringSubmatch(string(out))
	if m == nil {
		return fmt.Errorf("can't read gh's version from %q", out)
	}
	var have [3]int
	for i := range have {
		have[i], _ = strconv.Atoi(m[i+1])
	}
	if older(have, MinVersion) {
		return fmt.Errorf("gh %d.%d.%d is older than %d.%d.%d, the first that returns parent, subIssues and blockedBy. Upgrade it: %s",
			have[0], have[1], have[2], MinVersion[0], MinVersion[1], MinVersion[2], t.installHint(true))
	}
	return nil
}

// checkLogin verifies gh can reach GitHub as someone: through GH_TOKEN
// when a token is configured, else through gh's own login.
func (t *Tracker) checkLogin(ctx context.Context) error {
	if _, err := t.opts.Runner.Run(ctx, nil, "auth", "status"); err != nil {
		return fmt.Errorf("gh is not logged in (%v). Run `gh auth login`, or set `tracker.token` in config.local.toml", err)
	}
	return nil
}

// checkRepo is the token's free test call: can it read the repository the
// Tickets are in?
func (t *Tracker) checkRepo(ctx context.Context) error {
	repo := cmp.Or(t.opts.Repo, "{owner}/{repo}")
	if _, err := t.opts.Runner.Run(ctx, nil, "api", "repos/"+repo, "--silent"); err != nil {
		return fmt.Errorf("gh can't read %s (%v). Check `tracker.repo`, and that your login or `tracker.token` has access to it", repo, err)
	}
	return nil
}

func older(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// installHint names the command that installs (or upgrades) gh on the OS.
func (t *Tracker) installHint(upgrade bool) string {
	switch t.opts.GOOS {
	case "darwin":
		if upgrade {
			return "`brew upgrade gh`"
		}
		return "`brew install gh`"
	case "windows":
		if upgrade {
			return "`winget upgrade --id GitHub.cli`"
		}
		return "`winget install --id GitHub.cli`"
	default:
		return "use GitHub's own packages, as https://github.com/cli/cli/blob/trunk/docs/install_linux.md describes (distribution packages are often too old)"
	}
}
