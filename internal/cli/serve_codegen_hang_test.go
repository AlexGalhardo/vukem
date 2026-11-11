package cli

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// hangsOnCodegenApp serves "v1" on PORT but, asked to run codegen, never
// returns: the case of a binary that does not understand the command.
const hangsOnCodegenApp = `package main

import (
	"net/http"
	"os"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "codegen" {
		time.Sleep(time.Hour)
		return
	}

	http.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("v1")) })
	_ = http.ListenAndServe("127.0.0.1:"+os.Getenv("PORT"), nil)
}
`

func (s *syncWriter) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.w.String()
}

// A codegen that hangs must not hold back the app: the restart does not wait
// for it, it is stopped at the timeout and only a warning is logged.
func TestServeCodegenThatHangsDoesNotBlockTheRestart(
	t *testing.T,
) { //nolint:paralleltest // t.Setenv passes PORT to the app
	requireGo(t)

	old := codegenTimeout
	codegenTimeout = 3 * time.Second

	t.Cleanup(func() { codegenTimeout = old })

	dir := stdlibApp(t, hangsOnCodegenApp)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()

	t.Setenv("PORT", strconv.Itoa(port))

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan int, 1)
	out := &syncWriter{w: &bytes.Buffer{}}

	go func() {
		done <- Run(ctx, []string{"serve", "-poll", "100ms"}, Env{Dir: dir, Stdout: out, Stderr: out})
	}()

	waitFor(t, port, "v1", 30*time.Second)

	main := strings.Replace(hangsOnCodegenApp, `"v1"`, `"v2"`, 1)
	if err := os.WriteFile(filepath.Join(dir, "cmd", "app", "main.go"), []byte(main), 0o600); err != nil {
		t.Fatal(err)
	}

	waitFor(t, port, "v2", 30*time.Second)

	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(out.String(), "codegen timed out") && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond) //nolint:forbidigo // polling a real process
	}

	if !strings.Contains(out.String(), "vukem serve: codegen timed out after 3s") {
		t.Errorf("no timeout warning in the serve log:\n%s", out.String())
	}

	cancel()

	if code := <-done; code != 0 {
		t.Fatalf("serve exited %d", code)
	}
}
