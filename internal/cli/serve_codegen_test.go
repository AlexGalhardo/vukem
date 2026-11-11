package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// codegenApp stands in for an app's codegen command: it records that it ran,
// in the working directory serve gives it, and fails when "fail" exists.
const codegenApp = `package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "codegen" {
		return
	}

	if err := os.WriteFile("codegen.ran", []byte("yes"), 0o600); err != nil {
		os.Exit(3)
	}

	if _, err := os.Stat("fail"); err == nil {
		fmt.Fprintln(os.Stderr, "codegen: no routes")
		os.Exit(1)
	}
}
`

func TestRunCodegen(t *testing.T) {
	t.Parallel()
	requireGo(t)

	dir := stdlibApp(t, codegenApp)

	var out bytes.Buffer

	env := &Env{Dir: dir, Stdout: &out, Stderr: &out}

	bin, err := appBinary(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}

	if err := runCodegen(t.Context(), env, bin); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, "codegen.ran")); err != nil {
		t.Fatalf("codegen did not run in the project directory: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, "fail"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	err = runCodegen(t.Context(), env, bin)
	if err == nil || !strings.Contains(out.String(), "codegen: no routes") {
		t.Fatalf("runCodegen with a failing app = %v, output %q", err, out.String())
	}
}
