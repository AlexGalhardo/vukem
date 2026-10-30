package ssg_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/ssg"
)

func TestGenerateWritesIndexFiles(t *testing.T) {
	t.Parallel()

	var host string

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host = r.Host

		if !ssg.FromContext(r.Context()) {
			http.Error(w, "live", http.StatusInternalServerError)

			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte("<p>" + r.URL.Path + "</p>"))
	})

	dir := t.TempDir()
	report, err := ssg.Generate(t.Context(), h, []ssg.Page{
		{Path: "/"},
		{Path: "/about"},
		{Path: "/docs/cli"},
		{Path: "/docs//nested/"},
	}, ssg.Options{OutDir: dir})
	if err != nil {
		t.Fatal(err)
	}

	if host != ssg.DefaultHost {
		t.Fatalf("host = %q, want %s", host, ssg.DefaultHost)
	}

	want := []ssg.File{
		{Path: "index.html", Bytes: int64(len("<p>/</p>"))},
		{Path: "about/index.html", Bytes: int64(len("<p>/about</p>"))},
		{Path: "docs/cli/index.html", Bytes: int64(len("<p>/docs/cli</p>"))},
		{Path: "docs/nested/index.html", Bytes: int64(len("<p>/docs/nested</p>"))},
	}

	if len(report.Files) != len(want) {
		t.Fatalf("files = %+v, want %d", report.Files, len(want))
	}

	for i, f := range want {
		if report.Files[i] != f {
			t.Errorf("file %d = %+v, want %+v", i, report.Files[i], f)
		}

		got, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(f.Path)))
		if err != nil {
			t.Fatal(err)
		}

		if int64(len(got)) != f.Bytes {
			t.Fatalf("%s size = %d, want %d", f.Path, len(got), f.Bytes)
		}
	}

	if _, err := os.Stat(filepath.Join(dir, "secret")); !os.IsNotExist(err) {
		t.Fatalf("unexpected secret entry: %v", err)
	}
}

func TestGenerateFailClosed(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte("ok"))
		case "/go":
			http.Redirect(w, r, "/ok", http.StatusFound)
		case "/missing":
			http.NotFound(w, r)
		default:
			http.Error(w, "no", http.StatusInternalServerError)
		}
	})

	_, err := ssg.Generate(t.Context(), h, []ssg.Page{{Path: "/go"}}, ssg.Options{OutDir: dir})
	if !errors.Is(err, ssg.ErrStatus) {
		t.Fatalf("redirect = %v, want ErrStatus", err)
	}

	if _, statErr := os.Stat(filepath.Join(dir, "go", "index.html")); !os.IsNotExist(statErr) {
		t.Fatalf("redirect wrote a file: %v", statErr)
	}

	_, err = ssg.Generate(t.Context(), h, []ssg.Page{{Path: "/missing"}}, ssg.Options{OutDir: dir})
	if !errors.Is(err, ssg.ErrStatus) {
		t.Fatalf("404 = %v, want ErrStatus", err)
	}

	report, err := ssg.Generate(t.Context(), h, []ssg.Page{{Path: "/ok"}, {Path: "/boom"}}, ssg.Options{OutDir: dir})
	if !errors.Is(err, ssg.ErrStatus) {
		t.Fatalf("second page = %v", err)
	}

	if len(report.Files) != 1 || report.Files[0].Path != "ok/index.html" {
		t.Fatalf("partial report = %+v", report.Files)
	}

	if _, statErr := os.Stat(filepath.Join(dir, "boom", "index.html")); !os.IsNotExist(statErr) {
		t.Fatalf("failed page wrote a file: %v", statErr)
	}
}

func TestSecurity_A06_SSGRefusesOversizeBody(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		buf := make([]byte, ssg.MaxBody+1)
		_, _ = w.Write(buf)
	})

	_, err := ssg.Generate(t.Context(), h, []ssg.Page{{Path: "/big"}}, ssg.Options{OutDir: dir})
	if !errors.Is(err, ssg.ErrTooLarge) {
		t.Fatalf("oversize = %v, want ErrTooLarge", err)
	}

	if _, statErr := os.Stat(filepath.Join(dir, "big", "index.html")); !os.IsNotExist(statErr) {
		t.Fatalf("oversize wrote a file: %v", statErr)
	}
}

func TestSecurity_A01_SSGRejectsTraversal(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "dist")
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("nope"))
	})

	rejected := []string{
		"../secret",
		"/../secret",
		"/foo/../../secret",
		"/%2e%2e/secret",
		"/foo/%2e%2e%2fsecret",
		"https://example.com/secret",
		"//example.com/secret",
		"/secret?x=1",
		"/secret#x",
		"/sec\\ret",
		"/sec\x00ret",
		"about",
		"",
	}

	for _, p := range rejected {
		t.Run(p, func(t *testing.T) {
			_, err := ssg.Generate(t.Context(), h, []ssg.Page{{Path: p}}, ssg.Options{OutDir: dir})
			if !errors.Is(err, ssg.ErrInvalidPath) {
				t.Fatalf("path %q = %v, want ErrInvalidPath", p, err)
			}
		})
	}

	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 0 {
		t.Fatalf("parent has entries after rejected paths: %v", entries)
	}

	outside := filepath.Join(parent, "outside")
	if err := os.Mkdir(outside, 0o755); err != nil {
		t.Fatal(err)
	}

	link := filepath.Join(dir, "linked")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(outside, link); err != nil {
		t.Skip("symlinks are not available: " + err.Error())
	}

	_, err = ssg.Generate(t.Context(), h, []ssg.Page{{Path: "/linked"}}, ssg.Options{OutDir: dir})
	if err == nil || !errors.Is(err, ssg.ErrEscape) {
		t.Fatalf("symlink = %v, want ErrEscape", err)
	}

	if _, statErr := os.Stat(filepath.Join(outside, "index.html")); !os.IsNotExist(statErr) {
		t.Fatalf("wrote through symlink: %v", statErr)
	}
}

func TestGenerateRejectsBadInput(t *testing.T) {
	t.Parallel()

	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("x"))
	})
	dir := t.TempDir()

	if _, err := ssg.Generate(
		nil, //nolint:staticcheck // nil context is the case under test
		h,
		[]ssg.Page{{Path: "/"}},
		ssg.Options{OutDir: dir},
	); err == nil {
		t.Fatal("nil context accepted")
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := ssg.Generate(
		ctx,
		h,
		[]ssg.Page{{Path: "/"}},
		ssg.Options{OutDir: dir},
	); !errors.Is(
		err,
		context.Canceled,
	) {
		t.Fatalf("cancelled = %v", err)
	}

	if _, err := ssg.Generate(t.Context(), nil, nil, ssg.Options{OutDir: dir}); err == nil {
		t.Fatal("nil handler accepted")
	}

	if _, err := ssg.Generate(t.Context(), h, nil, ssg.Options{}); err == nil {
		t.Fatal("empty out dir accepted")
	}

	if _, err := ssg.Generate(t.Context(), h, nil, ssg.Options{OutDir: dir, Host: "https://evil"}); err == nil {
		t.Fatal("bad host accepted")
	}

	report, err := ssg.Generate(t.Context(), h, nil, ssg.Options{OutDir: dir, Host: "preview.internal"})
	if err != nil || len(report.Files) != 0 {
		t.Fatalf("empty pages = %+v %v", report, err)
	}
}

func TestFromContext(t *testing.T) {
	t.Parallel()

	if ssg.FromContext(t.Context()) || ssg.FromContext(nil) { //nolint:staticcheck // nil context is the case under test
		t.Fatal("plain context looked prerendered")
	}

	marked := ssg.NewContext(nil) //nolint:staticcheck // nil parent is the case under test
	if !ssg.FromContext(ssg.NewContext(t.Context())) || !ssg.FromContext(marked) {
		t.Fatal("NewContext was not visible")
	}

	req := httptest.NewRequestWithContext(
		ssg.NewContext(t.Context()),
		http.MethodGet,
		"http://ssg.invalid/",
		http.NoBody,
	)
	if !ssg.FromContext(req.Context()) {
		t.Fatal("request dropped the prerender mark")
	}
}

func TestCustomHost(t *testing.T) {
	t.Parallel()

	var got string

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Host
		_, _ = w.Write([]byte("x"))
	})

	if _, err := ssg.Generate(t.Context(), h, []ssg.Page{{Path: "/"}}, ssg.Options{
		OutDir: t.TempDir(),
		Host:   "preview.internal:443",
	}); err != nil {
		t.Fatal(err)
	}

	if got != "preview.internal:443" {
		t.Fatalf("host = %q", got)
	}
}

func TestBodyAtLimitIsWritten(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("writes an 8 MiB file")
	}

	body := strings.Repeat("a", ssg.MaxBody)
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	})
	dir := t.TempDir()

	report, err := ssg.Generate(t.Context(), h, []ssg.Page{{Path: "/"}}, ssg.Options{OutDir: dir})
	if err != nil {
		t.Fatal(err)
	}

	if len(report.Files) != 1 || report.Files[0].Bytes != ssg.MaxBody {
		t.Fatalf("report = %+v", report.Files)
	}
}
