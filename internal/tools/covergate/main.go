// Command covergate fails when a core package is below its coverage gate
// (docs/testing.md): 80% by default, 90% for the strict packages. CI runs
// it on the ubuntu coverage profile:
//
//	go test -coverprofile=coverage.out ./...
//	go run ./internal/tools/covergate coverage.out
//
// -v prints every package, not only the failing ones.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("covergate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	verbose := fs.Bool("v", false, "print every package")

	if err := fs.Parse(args); err != nil {
		return 2
	}

	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: covergate [-v] <coverprofile>")
		return 2
	}

	f, err := os.Open(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "covergate: %v\n", err)
		return 1
	}
	defer f.Close()

	results, err := Parse(f)
	if err != nil {
		fmt.Fprintf(stderr, "covergate: %s: %v\n", fs.Arg(0), err)
		return 1
	}

	failed := 0

	for _, r := range results {
		status := "ok  "
		if !r.Pass() {
			status = "FAIL"
			failed++
		}

		if *verbose || !r.Pass() {
			fmt.Fprintf(stdout, "%s %-24s %5.1f%% (gate %.0f%%)\n", status, display(r.Package), r.Percent(), r.Gate)
		}
	}

	for _, pkg := range Missing(results) {
		fmt.Fprintf(stdout, "FAIL %-24s missing from the profile (gate %.0f%%)\n", pkg, strictGates[pkg])
		failed++
	}

	if failed > 0 {
		fmt.Fprintf(stderr, "covergate: %d package(s) below the coverage gate (docs/testing.md)\n", failed)
		return 1
	}

	fmt.Fprintf(stdout, "covergate: %d core packages meet their gate\n", len(results))

	return 0
}

func display(pkg string) string {
	if pkg == "" {
		return "(root)"
	}

	return pkg
}
