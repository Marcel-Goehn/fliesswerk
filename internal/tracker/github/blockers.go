package github

import (
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Marcel-Goehn/fliesswerk/internal/tracker"
)

var (
	heading   = regexp.MustCompile(`^ {0,3}#{1,6}\s+(.*?)[\s#:]*$`)
	blockedBy = regexp.MustCompile(`(?i)^\s*(?:[-+]\s+)?blocked by\s*:`)
	issueURL  = regexp.MustCompile(`https?://[\w.-]+/([\w.-]+/[\w.-]+)/issues/(\d+)`)
	shortRef  = regexp.MustCompile(`(?:^|[^\w&/])((?:[\w.-]+/[\w.-]+)?)#(\d+)\b`)
)

// textBlockers returns the issues a body names as Blockers, in a
// `## Blocked by` section (up to the next heading) or on a `Blocked by:`
// line. Code blocks are ignored. Text never makes a Blocker: the Snapshot
// only uses this to flag names that lack a native link.
func textBlockers(home, body string) []tracker.ID {
	var out []tracker.ID
	seen := map[string]bool{}
	add := func(text string) {
		for _, id := range refs(home, text) {
			if !seen[id.Key] {
				seen[id.Key] = true
				out = append(out, id)
			}
		}
	}

	inSection, inCode := false, false
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inCode = !inCode
			continue
		}
		if inCode {
			continue
		}
		if m := heading.FindStringSubmatch(line); m != nil {
			inSection = strings.EqualFold(strings.Trim(m[1], "*_ "), "blocked by")
			continue
		}
		plain := strings.NewReplacer("*", "", "_", "").Replace(line)
		switch {
		case inSection:
			add(line)
		case blockedBy.MatchString(plain):
			_, rest, _ := strings.Cut(line, ":")
			add(rest)
		}
	}
	return out
}

// refs finds issue references in text: URLs, owner/name#N and #N.
func refs(home, text string) []tracker.ID {
	type match struct {
		at   int
		repo string
		n    string
	}
	var ms []match
	for _, m := range issueURL.FindAllStringSubmatchIndex(text, -1) {
		ms = append(ms, match{m[0], text[m[2]:m[3]], text[m[4]:m[5]]})
	}
	// Blank the URLs out so their parts aren't found again.
	blanked := issueURL.ReplaceAllStringFunc(text, func(s string) string { return strings.Repeat(" ", len(s)) })
	for _, m := range shortRef.FindAllStringSubmatchIndex(blanked, -1) {
		ms = append(ms, match{m[0], blanked[m[2]:m[3]], blanked[m[4]:m[5]]})
	}
	// Keep the order they appear in.
	slices.SortFunc(ms, func(a, b match) int { return a.at - b.at })

	out := make([]tracker.ID, 0, len(ms))
	for _, m := range ms {
		n, err := strconv.Atoi(m.n)
		if err != nil {
			continue
		}
		repo := m.repo
		if repo == "" {
			repo = home
		}
		out = append(out, idFor(home, "https://github.com/"+repo+"/issues/"+m.n, n))
	}
	return out
}
