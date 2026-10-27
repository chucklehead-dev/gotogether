// Package nearest finds the name a misspelled one most likely meant, for
// messages' "did you mean": one rule for the binding's lint, the CLI and
// qtest's claims.
package nearest

import "strings"

// Name is the name of names nearest to name by edit distance, ignoring
// case, if it is near enough to be a misspelling of it (at most 2 edits,
// or a third of name's length), else "". Of equally near names the first
// wins.
func Name(name string, names []string) string {
	best, bestD := "", -1
	for _, n := range names {
		if d := Distance(strings.ToLower(name), strings.ToLower(n)); bestD < 0 || d < bestD {
			best, bestD = n, d
		}
	}
	if bestD >= 0 && bestD <= max(2, len(name)/3) {
		return best
	}
	return ""
}

// Distance is the Levenshtein distance between a and b, in bytes.
func Distance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			sub := prev[j-1]
			if a[i-1] != b[j-1] {
				sub++
			}
			cur[j] = min(sub, prev[j]+1, cur[j-1]+1)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
