// Command labelsync creates or updates the GitHub labels listed in a file
// (one "name|color|description" per line) with the gh CLI. It never deletes
// labels.
//
//	go run ./internal/tools/labelsync [-dry-run] .github/labels.txt
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(ctx context.Context, argv []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("labelsync", flag.ContinueOnError)
	fs.SetOutput(stderr)

	dry := fs.Bool("dry-run", false, "print the gh commands without running them")

	if err := fs.Parse(argv); err != nil {
		return 2
	}

	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: labelsync [-dry-run] <labels-file>")
		return 2
	}

	// The path comes from the operator's command line.
	f, err := os.Open(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "labelsync: %v\n", err)
		return 1
	}
	defer f.Close()

	labels, err := parse(f)
	if err != nil {
		fmt.Fprintf(stderr, "labelsync: %v\n", err)
		return 1
	}

	for _, l := range labels {
		a := args(l)
		if *dry {
			fmt.Fprintf(stdout, "gh %s\n", strings.Join(a, " "))
			continue
		}

		// Arguments are a slice built from the labels file; no shell is involved.
		out, err := exec.CommandContext(ctx, "gh", a...).CombinedOutput()
		if err != nil {
			fmt.Fprintf(stderr, "labelsync: %s: %v\n%s", l.Name, err, out)
			return 1
		}
	}

	return 0
}
