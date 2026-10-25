package cli

import "fmt"

// suggest returns the vukem command closest to name by edit distance
// (adjacent transpositions count as one edit), or "" when none is close:
// at most 2 edits, and fewer than half of name's length.
func suggest(name string) string {
	best, bestD := "", 3

	candidates := []string{"list", "help"}
	for _, c := range commands() {
		candidates = append(candidates, c.name)
	}

	for _, c := range candidates {
		if d := editDistance(name, c); d < bestD && 2*d < len(name) {
			best, bestD = c, d
		}
	}

	return best
}

// didYouMean is the " (did you mean ...?)" suffix of an unknown command
// error, or "".
func didYouMean(name string) string {
	if s := suggest(name); s != "" {
		return fmt.Sprintf(" (did you mean %q?)", s)
	}

	return ""
}

// editDistance is the optimal string alignment distance between a and b,
// byte-wise (command names are ASCII).
func editDistance(a, b string) int {
	prev2 := make([]int, len(b)+1)
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)

	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		cur[0] = i

		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}

			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
		}

		prev2, prev, cur = prev, cur, prev2
	}

	return prev[len(b)]
}
