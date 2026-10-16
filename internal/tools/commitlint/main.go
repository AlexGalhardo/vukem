// Command commitlint validates a commit message file against Conventional
// Commits 1.0. It is run by the lefthook commit-msg hook:
//
//	go run ./internal/tools/commitlint .git/COMMIT_EDITMSG
package main

import (
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, out io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(out, "usage: commitlint <commit-message-file>")
		return 2
	}

	// The path comes from git (commit-msg hook), not from untrusted input.
	msg, err := os.ReadFile(args[0]) //nolint:gosec // G703: trusted path from git
	if err != nil {
		fmt.Fprintf(out, "commitlint: %v\n", err)
		return 1
	}

	if err := Validate(string(msg)); err != nil {
		fmt.Fprintf(out, "commitlint: invalid commit message:\n%v\n\nSee docs/tooling.md#conventional-commits\n", err)
		return 1
	}

	return 0
}
