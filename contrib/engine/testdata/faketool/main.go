// Command faketool stands in for ffmpeg in engine's tests: a real
// binary, started as a real child process, so the environment, the
// working directory, the temp pinning, the output bounds and the
// cancellation are exercised against a process rather than a mock.
//
// It is built by the test into a temporary directory; nothing imports it.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "faketool: need a mode")
		os.Exit(2)
	}

	switch os.Args[1] {
	case "-version":
		// On stdout, like ffmpeg.
		fmt.Println("faketool version 1.2.3")
	case "-version-stderr":
		// On stderr with exit 0, like poppler's pdftotext: a probe that
		// reads only stdout records "unknown" for a working tool.
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "faketool stderr 4.5.6")
	case "env":
		// Print the whole environment, so the test can assert what the
		// child can and cannot see.
		for _, kv := range os.Environ() {
			fmt.Println(kv)
		}
	case "cwd":
		dir, err := os.Getwd()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		fmt.Println(dir)
	case "write-temp":
		// Write into the temp directory the environment points at, which
		// is what -dSAFER permits Ghostscript to do.
		dir := os.TempDir()

		p := filepath.Join(dir, "scratch.txt")
		if err := os.WriteFile(p, []byte("scratch"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		fmt.Println(p)
	case "spew":
		// Many lines on both streams, to exercise the tail.
		n, _ := strconv.Atoi(os.Args[2])
		for i := range n {
			fmt.Printf("out %d\n", i)
			fmt.Fprintf(os.Stderr, "err %d\n", i)
		}
	case "stream":
		// Bytes on stdout, as a tool whose product is its output.
		n, _ := strconv.Atoi(os.Args[2])
		buf := make([]byte, 1024)
		for i := range buf {
			buf[i] = 'x'
		}

		for range n {
			_, _ = os.Stdout.Write(buf)
		}
	case "sleep":
		d, _ := time.ParseDuration(os.Args[2])
		time.Sleep(d)
	case "fail":
		code, _ := strconv.Atoi(os.Args[2])
		fmt.Fprintln(os.Stderr, "faketool: deliberate failure")
		os.Exit(code)
	default:
		fmt.Fprintf(os.Stderr, "faketool: unknown mode %q\n", os.Args[1])
		os.Exit(2)
	}
}
