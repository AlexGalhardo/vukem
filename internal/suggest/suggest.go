// Package suggest finds the closest name to a mistyped one.
package suggest

// Closest returns the candidate closest to name by edit distance (adjacent
// transpositions count as one edit), or "" when none is close: at most 2
// edits, and fewer than half of name's length.
func Closest(name string, candidates []string) string {
	best, bestD := "", 3

	for _, c := range candidates {
		if d := Distance(name, c); d < bestD && 2*d < len(name) {
			best, bestD = c, d
		}
	}

	return best
}

// Distance is the optimal string alignment distance between a and b,
// byte-wise.
func Distance(a, b string) int {
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
