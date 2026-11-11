package cli

import (
	"fmt"

	"github.com/AlexGalhardo/vukem/internal/suggest"
)

// suggestCommand returns the vukem command closest to name, or "" when none
// is close (see suggest.Closest).
func suggestCommand(name string) string {
	candidates := []string{"list", "help"}
	for _, c := range commands() {
		candidates = append(candidates, c.name)
	}

	return suggest.Closest(name, candidates)
}

// didYouMean is the " (did you mean ...?)" suffix of an unknown command
// error, or "".
func didYouMean(name string) string {
	if s := suggestCommand(name); s != "" {
		return fmt.Sprintf(" (did you mean %q?)", s)
	}

	return ""
}
