package cli

import (
	"bufio"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
)

// stack is the frontend a new project starts with.
type stack struct {
	kind      string // "htmx", "inertia" or "api"
	framework string // Inertia only: "react", "vue" or "svelte"
}

var inertiaFrameworks = []string{"react", "vue", "svelte"}

// chooseStack resolves the stack from flags, or asks on an interactive
// terminal; without a terminal it uses the HTMX default.
func chooseStack(env *Env, api bool, inertia string) (stack, error) {
	inertia = strings.ToLower(strings.TrimSpace(inertia))

	switch {
	case api && inertia != "":
		return stack{}, usagef("choose --api or --inertia, not both")
	case api:
		return stack{kind: "api"}, nil
	case inertia != "":
		if !slices.Contains(inertiaFrameworks, inertia) {
			return stack{}, usagef("--inertia must be react, vue or svelte, not %q", inertia)
		}

		return stack{kind: "inertia", framework: inertia}, nil
	case !env.Interactive || env.Stdin == nil:
		return stack{kind: "htmx"}, nil
	}

	in := bufio.NewReader(env.Stdin)

	kind := ask(env.Stdout, in, "Which stack?", []option{
		{"htmx", "HTMX + Tailwind (server-rendered views)"},
		{"inertia", "Inertia + Tailwind (React, Vue or Svelte)"},
		{"api", "JSON API (tokens, /api/v1)"},
	})
	if kind != "inertia" {
		return stack{kind: kind}, nil
	}

	fw := ask(env.Stdout, in, "Which Inertia framework?", []option{
		{"react", "React"}, {"vue", "Vue"}, {"svelte", "Svelte"},
	})

	return stack{kind: "inertia", framework: fw}, nil
}

type option struct{ value, label string }

// ask prints a numbered menu and returns the chosen value; an empty answer
// or EOF picks the first option, and unknown answers ask again.
func ask(w io.Writer, in *bufio.Reader, question string, opts []option) string {
	for {
		fmt.Fprintln(w, question)

		for i, o := range opts {
			def := ""
			if i == 0 {
				def = " (default)"
			}

			fmt.Fprintf(w, "  %d) %s%s\n", i+1, o.label, def)
		}

		fmt.Fprint(w, "> ")

		line, err := in.ReadString('\n')
		answer := strings.ToLower(strings.TrimSpace(line))

		if answer == "" {
			return opts[0].value
		}

		for i, o := range opts {
			if answer == o.value || answer == strconv.Itoa(i+1) {
				return o.value
			}
		}

		if err != nil { // EOF after an unknown answer
			return opts[0].value
		}

		fmt.Fprintf(w, "Unknown choice %q.\n", answer)
	}
}
