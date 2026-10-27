// Package nearest finds the name a misspelled one most likely meant, for
// messages' "did you mean": one rule for the binding's lint, the CLI and
// qtest's claims.
package nearest

import "strings"

// Name is the name of names nearest to name by edit distance (Distance),
// ignoring case, if it is near enough to be a misspelling of it, else "":
// one edit for a name shorter than 8 bytes, two for a longer one, and
// fewer edits than either name has bytes. A looser bound once offered
// unrelated short names ("p1" for "q2", "put" for "pop"): a suggestion
// that is wrong costs more than none. Of equally near names the first
// wins.
func Name(name string, names []string) string {
	best, bestD := "", -1
	for _, n := range names {
		if d := Distance(strings.ToLower(name), strings.ToLower(n)); bestD < 0 || d < bestD {
			best, bestD = n, d
		}
	}
	bound := 1
	if len(name) >= 8 {
		bound = 2
	}
	if bestD >= 0 && bestD <= bound && bestD < len(name) && bestD < len(best) {
		return best
	}
	return ""
}

// Distance is the optimal string alignment distance between a and b, in
// bytes: insertions, deletions, substitutions and transpositions of
// adjacent bytes ("depsoit" is one edit from "deposit", the typo a hand
// makes most).
func Distance(a, b string) int {
	d := make([][]int, len(a)+1)
	for i := range d {
		d[i] = make([]int, len(b)+1)
		d[i][0] = i
	}
	for j := range d[0] {
		d[0][j] = j
	}
	for i := 1; i <= len(a); i++ {
		for j := 1; j <= len(b); j++ {
			sub := d[i-1][j-1]
			if a[i-1] != b[j-1] {
				sub++
			}
			d[i][j] = min(sub, d[i-1][j]+1, d[i][j-1]+1)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}
	return d[len(a)][len(b)]
}
