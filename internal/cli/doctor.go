package cli

import (
	"context"
	"flag"
	"fmt"
	"os/exec"
	"strings"
)

// doctorTool is an external tool the generated project relies on outside
// the Go toolchain.
type doctorTool struct {
	name, versionArg, why, install string
}

func doctorTools() []doctorTool {
	return []doctorTool{
		{
			name:       "betterleaks",
			versionArg: "version",
			why:        "secret scanning in the pre-commit hook and npm run secrets:scan",
			install:    "https://github.com/betterleaks/betterleaks/releases",
		},
	}
}

// doctorCmd reports which optional tools are installed and their versions.
// It is informational: a missing tool is not an error, CI still runs it.
func doctorCmd(*flag.FlagSet) func(ctx context.Context, env *Env, args []string) error {
	return func(ctx context.Context, env *Env, _ []string) error {
		doctor(ctx, env, exec.LookPath, func(ctx context.Context, bin, arg string) (string, error) {
			out, err := exec.CommandContext(ctx, bin, arg).Output()
			return string(out), err
		})

		return nil
	}
}

func doctor(
	ctx context.Context,
	env *Env,
	lookPath func(string) (string, error),
	run func(ctx context.Context, bin, arg string) (string, error),
) {
	for _, t := range doctorTools() {
		bin, err := lookPath(t.name)
		if err != nil {
			fmt.Fprintf(env.Stdout, "%s: not installed (%s); install it from %s\n", t.name, t.why, t.install)
			continue
		}

		out, err := run(ctx, bin, t.versionArg)
		if err != nil {
			fmt.Fprintf(env.Stdout, "%s: installed at %s, version unknown (%v)\n", t.name, bin, err)
			continue
		}

		fmt.Fprintf(env.Stdout, "%s: %s (%s)\n", t.name, strings.TrimSpace(out), bin)
	}
}
