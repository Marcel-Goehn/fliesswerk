package prompt

import (
	"fmt"
	"strings"
)

// diffContext is how many unchanged lines a hunk shows around a change.
const diffContext = 3

// edit is one line of a diff: ' ' kept, '-' removed, '+' added.
type edit struct {
	op   byte
	line string
}

// UnifiedDiff is the unified diff from a to b, labelled with their names,
// "" if they are equal. Prompt files are short, so a plain longest common
// subsequence of their lines is enough.
func UnifiedDiff(aName, a, bName, b string) string {
	edits := diffLines(lines(a), lines(b))
	var out strings.Builder
	for i := 0; i < len(edits); {
		if edits[i].op == ' ' {
			i++
			continue
		}
		start := max(0, i-diffContext)
		end := i
		for end < len(edits) {
			if edits[end].op != ' ' {
				end++
				continue
			}
			run := end
			for run < len(edits) && edits[run].op == ' ' {
				run++
			}
			if run == len(edits) || run-end > 2*diffContext {
				end = min(run, end+diffContext)
				break
			}
			end = run
		}
		if out.Len() == 0 {
			fmt.Fprintf(&out, "--- %s\n+++ %s\n", aName, bName)
		}
		hunk(&out, edits, start, end)
		i = end
	}
	return out.String()
}

// hunk writes edits[start:end] with its header.
func hunk(out *strings.Builder, edits []edit, start, end int) {
	aLine, bLine := 1, 1
	for _, e := range edits[:start] {
		if e.op != '+' {
			aLine++
		}
		if e.op != '-' {
			bLine++
		}
	}
	aLen, bLen := 0, 0
	for _, e := range edits[start:end] {
		if e.op != '+' {
			aLen++
		}
		if e.op != '-' {
			bLen++
		}
	}
	fmt.Fprintf(out, "@@ -%s +%s @@\n", hunkRange(aLine, aLen), hunkRange(bLine, bLen))
	for _, e := range edits[start:end] {
		fmt.Fprintf(out, "%c%s\n", e.op, e.line)
	}
}

// hunkRange is a hunk header's start,length; an empty side starts one
// line earlier, as diff -u writes it.
func hunkRange(start, n int) string {
	if n == 0 {
		start--
	}
	return fmt.Sprintf("%d,%d", start, n)
}

// lines splits s into its lines, without their line ends.
func lines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(strings.ReplaceAll(s, "\r\n", "\n"), "\n"), "\n")
}

// diffLines turns a into b with the fewest added and removed lines.
func diffLines(a, b []string) []edit {
	// lcs[i][j] is the length of the longest common subsequence of a[i:]
	// and b[j:].
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var edits []edit
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		switch {
		case i < len(a) && j < len(b) && a[i] == b[j]:
			edits = append(edits, edit{' ', a[i]})
			i++
			j++
		case i < len(a) && (j == len(b) || lcs[i+1][j] >= lcs[i][j+1]):
			edits = append(edits, edit{'-', a[i]})
			i++
		default:
			edits = append(edits, edit{'+', b[j]})
			j++
		}
	}
	return edits
}
