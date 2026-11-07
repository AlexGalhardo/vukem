// Command release-notes writes the CHANGELOG.md section for one version tag
// to release-notes.md.
//
// An empty or missing section exits 1. There is no placeholder body: a
// release published with an empty body looks the same as a successful one.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: release-notes <vX.Y.Z>")
		os.Exit(2)
	}

	raw, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		fmt.Fprintf(os.Stderr, "release-notes: %v\n", err)
		os.Exit(1)
	}

	body, err := changelogSection(string(raw), os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "release-notes: %v\n", err)
		os.Exit(1)
	}

	if err := os.WriteFile("release-notes.md", []byte(body), 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "release-notes: %v\n", err)
		os.Exit(1)
	}
}

// changelogSection returns the body under "## [<version>]" up to the next
// release heading. The tag may be "v1.2.3" or "1.2.3". The heading line
// itself is not part of the body.
func changelogSection(text, tag string) (string, error) {
	version := strings.TrimPrefix(tag, "v")
	if version == "" {
		return "", errors.New("missing version")
	}

	heading := "## [" + version + "]"
	var b strings.Builder
	found := false

	for line := range strings.SplitSeq(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if found && strings.HasPrefix(line, "## [") {
			break
		}

		if strings.HasPrefix(line, heading) {
			found = true

			continue
		}

		if found {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}

	if !found || strings.TrimSpace(b.String()) == "" {
		return "", fmt.Errorf("CHANGELOG.md has no section for v%s", version)
	}

	return b.String(), nil
}
