package config

import (
	"fmt"
	"strings"
)

// didYouMean returns `; did you mean "x"?` for the candidate closest to
// word, or "" when none is close enough to be a likely typo.
func didYouMean(word string, candidates []string) string {
	best, bestDist := "", -1
	for _, c := range candidates {
		d := distance(strings.ToLower(word), strings.ToLower(c))
		if bestDist == -1 || d < bestDist {
			best, bestDist = c, d
		}
	}
	// Allow about one edit per three characters, and always one.
	limit := max(1, len([]rune(word))/3)
	if bestDist == -1 || bestDist > limit {
		return ""
	}
	return fmt.Sprintf("; did you mean %q?", best)
}

// distance is the optimal string alignment distance: insertions, deletions,
// substitutions and transpositions of neighbours each cost one.
func distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	d := make([][]int, len(ra)+1)
	for i := range d {
		d[i] = make([]int, len(rb)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(ra); i++ {
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(ra)][len(rb)]
}
