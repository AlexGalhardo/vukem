package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseReadsRowsAndReferences(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "PARITY.md")
	body := "# Ledger\n\n| Area | Capability | Covered by |\n| --- | --- | --- |\n" +
		"| SQL | Tables list | `go:TestA`; `e2e:grid.spec.ts#sorting by a header` |\n" +
		"| SQL | Nothing named | none |\n"

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	rows, err := parse(path)
	if err != nil || len(rows) != 2 {
		t.Fatalf("parse = %v, %v, want 2 rows", rows, err)
	}

	if got := rows[0].refs; len(got) != 2 || got[0] != (ref{kind: "go", name: "TestA"}) ||
		got[1] != (ref{kind: "e2e", name: "grid.spec.ts", title: "sorting by a header"}) {
		t.Errorf("refs = %+v", got)
	}

	problems := check(rows[1:])
	if len(problems) != 1 || !strings.Contains(problems[0], "no test is named") {
		t.Errorf("a row with no reference must be a problem: %v", problems)
	}
}

func TestCheckRefRefusesWhatDoesNotExist(t *testing.T) {
	t.Parallel()

	tests := map[string]ref{
		"a Go test that is not there": {kind: "go", name: "TestDefinitelyNotThere"},
		"a spec file that is missing": {kind: "e2e", name: "nope.spec.ts", title: "x"},
		"an unknown kind":             {kind: "cypress", name: "x"},
	}

	for name, x := range tests {
		if msg := checkRef(x, map[string]string{}); msg == "" {
			t.Errorf("%s was accepted", name)
		}
	}

	if msg := checkRef(ref{kind: "go", name: "TestA"}, map[string]string{"TestA": "."}); msg != "" {
		t.Errorf("a Go test of the root package = %q, want accepted", msg)
	}

	if msg := checkRef(ref{kind: "go", name: "TestA"}, map[string]string{"TestA": "cmd/vukem-studio"}); msg == "" {
		t.Error("a test of cmd/ named with go: was accepted")
	}
}
