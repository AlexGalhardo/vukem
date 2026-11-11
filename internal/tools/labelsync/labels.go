package main

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"
)

type label struct{ Name, Color, Description string }

var colorRe = regexp.MustCompile(`^[0-9a-fA-F]{6}$`)

// parse reads labels in the form "name|color|description", one per line.
// Blank lines and lines starting with "#" are ignored.
func parse(r io.Reader) ([]label, error) {
	var out []label

	seen := map[string]bool{}
	sc := bufio.NewScanner(r)

	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		f := strings.Split(line, "|")
		if len(f) != 3 {
			return nil, fmt.Errorf("line %d: want 3 fields (name|color|description), got %d", n, len(f))
		}

		l := label{Name: strings.TrimSpace(f[0]), Color: strings.TrimSpace(f[1]), Description: strings.TrimSpace(f[2])}

		switch {
		case l.Name == "":
			return nil, fmt.Errorf("line %d: empty name", n)
		case !colorRe.MatchString(l.Color):
			return nil, fmt.Errorf("line %d: invalid color %q", n, l.Color)
		case seen[l.Name]:
			return nil, fmt.Errorf("line %d: duplicate label %q", n, l.Name)
		}

		seen[l.Name] = true
		out = append(out, l)
	}

	return out, sc.Err()
}

// args returns the gh arguments that create or update l.
func args(l label) []string {
	return []string{"label", "create", l.Name, "--color", l.Color, "--description", l.Description, "--force"}
}
