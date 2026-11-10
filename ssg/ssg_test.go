package ssg_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

func TestSecurity_A01_SSGDoesNotCreateDirectoriesThroughLinks(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "dist")
	outside := filepath.Join(parent, "outside")

	for _, d := range []string{dir, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	linkDir(t, outside, filepath.Join(dir, "linked"))

	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("nope"))
	})

	_, err := ssg.Generate(t.Context(), h, []ssg.Page{{Path: "/linked/deep/page"}}, ssg.Options{OutDir: dir})
	if !errors.Is(err, ssg.ErrEscape) {
		t.Fatalf("Generate through linked dir = %v, want ErrEscape", err)
	}

	entries, err := os.ReadDir(outside)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 0 {
		t.Fatalf("outside dir has entries: %v", entries)
	}
}

// linkDir makes link point at target: a symlink, or an NTFS junction when Windows denies symlinks.
func linkDir(t *testing.T, target, link string) {
	t.Helper()

	err := os.Symlink(target, link)
	if err == nil {
		return
	}

	if runtime.GOOS != "windows" {
		t.Skip("symlinks are not available: " + err.Error())
	}

	cmd := exec.CommandContext(t.Context(), "cmd", "/c", "mklink", "/J", link, target)
	if out, jerr := cmd.CombinedOutput(); jerr != nil {
		t.Fatalf("mklink /J: %v: %s", jerr, out)
	}
}

func TestSecurity_A01_SSGRefusesCollidingPaths(t *testing.T) {
	t.Parallel()

	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("x"))
	})

	tests := []struct {
		name  string
		pages []string
	}{
		{"case", []string{"/About", "/about"}},
		{"trailing dot", []string{"/about."}},
		{"trailing space", []string{"/about%20"}},
		{"con", []string{"/con"}},
		{"nul", []string{"/docs/NUL"}},
		{"aux", []string{"/aux"}},
		{"prn", []string{"/prn"}},
		{"com1", []string{"/com1"}},
		{"lpt1", []string{"/lpt1"}},
		{"device with extension", []string{"/con.txt"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pages := make([]ssg.Page, len(tt.pages))
			for i, p := range tt.pages {
				pages[i] = ssg.Page{Path: p}
			}

			_, err := ssg.Generate(t.Context(), h, pages, ssg.Options{OutDir: t.TempDir()})
			if !errors.Is(err, ssg.ErrInvalidPath) {
				t.Fatalf("Generate(%v) = %v, want ErrInvalidPath", tt.pages, err)
			}
		})
	}

	both := []ssg.Page{{Path: "/About"}, {Path: "/about"}}

	_, err := ssg.Generate(t.Context(), h, both, ssg.Options{OutDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "/About") || !strings.Contains(err.Error(), "/about") {
		t.Fatalf("collision error = %v, want both paths named", err)
	}
}

func TestSSGDecodesPathOnce(t *testing.T) {
	t.Parallel()

	var got string

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Path
		_, _ = w.Write([]byte("x"))
	})

	for _, p := range []string{"/x%2541", "/50%25"} {
		_, err := ssg.Generate(t.Context(), h, []ssg.Page{{Path: p}}, ssg.Options{OutDir: t.TempDir()})
		if !errors.Is(err, ssg.ErrInvalidPath) {
			t.Fatalf("Generate(%q) = %v, want ErrInvalidPath", p, err)
		}
	}

	dir := t.TempDir()

	report, err := ssg.Generate(t.Context(), h, []ssg.Page{{Path: "/a%20b"}}, ssg.Options{OutDir: dir})
	if err != nil {
		t.Fatal(err)
	}

	if len(report.Files) != 1 || report.Files[0].Path != "a b/index.html" {
		t.Fatalf("files = %+v, want a b/index.html", report.Files)
	}

	if got != "/a b" {
		t.Fatalf("handler path = %q, want %q", got, "/a b")
	}

	if _, err := os.Stat(filepath.Join(dir, "a b", "index.html")); err != nil {
		t.Fatal(err)
	}
}
