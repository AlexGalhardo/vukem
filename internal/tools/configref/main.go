// Command configref generates the environment variable table of
// docs/configuration.md from the config structs of the root package, and
// checks that the committed table is in sync:
//
//	go run ./internal/tools/configref [-check] [-file docs/configuration.md] [dir]
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("configref", flag.ContinueOnError)
	fs.SetOutput(stderr)

	checkOnly := fs.Bool("check", false, "fail when the table in -file is out of date instead of rewriting it")
	file := fs.String("file", "docs/configuration.md", "Markdown file with the configref markers")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}

	vars, err := collect(dir)
	if err != nil {
		fmt.Fprintf(stderr, "configref: %v\n", err)
		return 1
	}

	table := render(vars)

	if *checkOnly {
		if err := check(*file, table); err != nil {
			fmt.Fprintf(stderr, "configref: %v\n", err)
			return 1
		}

		return 0
	}

	cur, err := os.ReadFile(*file)
	if err != nil {
		fmt.Fprintf(stderr, "configref: %v\n", err)
		return 1
	}

	out, err := splice(string(cur), table)
	if err != nil {
		fmt.Fprintf(stderr, "configref: %s: %v\n", *file, err)
		return 1
	}

	err = os.WriteFile(*file, []byte(out), 0o600) //nolint:gosec // G703: -file is an operator flag
	if err != nil {
		fmt.Fprintf(stderr, "configref: %v\n", err)
		return 1
	}

	return 0
}
