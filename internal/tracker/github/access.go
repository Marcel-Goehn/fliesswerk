package github

import (
	"strings"
	"unicode"
)

// tokenWalkthrough says how to make a token that reaches repo and no more.
func tokenWalkthrough(repo string) string {
	owner, _, _ := strings.Cut(repo, "/")
	return strings.Join([]string{
		"Make a fine-grained token that reaches only " + repo + ":",
		"  1. Open https://github.com/settings/personal-access-tokens/new",
		"  2. Name it, and set Resource owner to " + owner,
		"  3. Under Repository access, choose Only select repositories and pick " + repo,
		"  4. Under Repository permissions, set Issues to Read and write, and Contents and Pull requests to Read-only (Metadata is always Read-only)",
		"  5. Generate the token and copy it",
	}, "\n")
}

// tokenKey names repo's token in the credentials file, in what a key may hold.
func tokenKey(repo string) string {
	return "github." + strings.Map(func(r rune) rune {
		switch {
		case r == '/':
			return '.'
		case r == '.' || r == '_' || r == '-' || unicode.IsLetter(r) && r < unicode.MaxASCII || unicode.IsDigit(r) && r < unicode.MaxASCII:
			return r
		default:
			return '-'
		}
	}, repo)
}
