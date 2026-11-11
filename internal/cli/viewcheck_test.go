package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// checkApp stands in for an app's view:check. What it does is chosen by the
// "mode" file in its working directory, which serve sets to the project dir.
const checkApp = `package main

import (
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "view:check" {
		return
	}

	mode, _ := os.ReadFile("mode")

	switch strings.TrimSpace(string(mode)) {
	case "problems":
		fmt.Println("2026/11/11 booting the app")
		fmt.Println("posts/index.html:3:5: V002: field or method \"Titel\" not found in models.Post")
		fmt.Println("posts/index.html:9:92: warning: V005: hx-target \"#x\": no element with that id in this view")
		fmt.Fprintln(os.Stderr, "view:check: 1 problem(s) in 1 view(s)")
		os.Exit(1)
	case "silent":
		fmt.Fprintln(os.Stderr, "view:check: no typed views")
		os.Exit(1)
	case "crash":
		fmt.Fprintln(os.Stderr, "panic: boom")
		os.Exit(2)
	case "hang":
		time.Sleep(time.Minute)
	case "flood":
		for i := 0; i < 20000; i++ {
			fmt.Printf("posts/index.html:%d:1: V002: field or method \"Titel\" not found in models.Post\n", i+1)
		}
		os.Exit(1)
	}
}
`

// viewCheckProject builds the stand-in app, and returns the env, the binary
// and the buffer the warnings go to. WithViewPages in a Go source is what
// makes serve run the check.
func viewCheckProject(t *testing.T) (*Env, string, *bytes.Buffer) {
	t.Helper()
	requireGo(t)

	dir := stdlibApp(t, checkApp)

	if err := os.WriteFile(
		filepath.Join(dir, "views.go"),
		[]byte("package main\n\n// vukem.WithViewPages(...)\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer

	env := &Env{Dir: dir, Stdout: &out, Stderr: &out}

	bin, err := appBinary(t.Context(), env)
	if err != nil {
		t.Fatal(err)
	}

	return env, bin, &out
}

func setMode(t *testing.T, env *Env, mode string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(env.Dir, "mode"), []byte(mode), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestServeWarnsAboutViewProblems(t *testing.T) {
	t.Parallel()

	env, bin, out := viewCheckProject(t)

	// Without WithViewPages in the sources the app is left alone.
	if err := os.Rename(filepath.Join(env.Dir, "views.go"), filepath.Join(env.Dir, "views.go.off")); err != nil {
		t.Fatal(err)
	}

	setMode(t, env, "problems")
	warnViewProblems(t.Context(), env, bin)

	if out.Len() != 0 {
		t.Fatalf("warned about an app without typed views: %q", out.String())
	}

	if err := os.Rename(filepath.Join(env.Dir, "views.go.off"), filepath.Join(env.Dir, "views.go")); err != nil {
		t.Fatal(err)
	}

	warnViewProblems(t.Context(), env, bin)

	// Only the lines in file:line:col form are shown, not boot noise.
	want := "vukem serve: view:check found problems (not blocking):\n" +
		"posts/index.html:3:5: V002: field or method \"Titel\" not found in models.Post\n" +
		"posts/index.html:9:92: warning: V005: hx-target \"#x\": no element with that id in this view\n"
	if out.String() != want {
		t.Errorf("warning = %q, want %q", out.String(), want)
	}
}

func TestServeViewCheckExitCodes(t *testing.T) {
	t.Parallel()

	env, bin, out := viewCheckProject(t)

	setMode(t, env, "silent")
	warnViewProblems(t.Context(), env, bin)

	if out.Len() != 0 {
		t.Fatalf("exit 1 without findings printed %q, want silence", out.String())
	}

	setMode(t, env, "crash")
	warnViewProblems(t.Context(), env, bin)

	if got := out.String(); !strings.Contains(got, "view:check could not run") || !strings.Contains(got, "exit 2") {
		t.Errorf("exit 2 printed %q, want a could-not-run notice with the exit code", got)
	}
}

func TestServeViewCheckOutputIsBounded(t *testing.T) {
	t.Parallel()

	env, bin, out := viewCheckProject(t)

	setMode(t, env, "flood")
	warnViewProblems(t.Context(), env, bin)

	if out.Len() == 0 || out.Len() > viewCheckOutputLimit+512 {
		t.Errorf("printed %d bytes, want a non-empty output of at most %d", out.Len(), viewCheckOutputLimit)
	}

	if !strings.HasSuffix(out.String(), "\n") {
		t.Error("the bounded output ends in a partial line")
	}
}

func TestServeViewCheckStopsWhenCancelled(t *testing.T) {
	t.Parallel()

	env, bin, out := viewCheckProject(t)

	setMode(t, env, "hang")

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})

	go func() {
		defer close(done)

		warnViewProblems(ctx, env, bin)
	}()

	time.AfterFunc(300*time.Millisecond, cancel)

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("warnViewProblems kept running after its context was cancelled")
	}

	if out.Len() != 0 {
		t.Errorf("a cancelled check printed %q", out.String())
	}
}
