package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestFindWrappers(t *testing.T) {
	t.Parallel()

	got, err := findWrappers(filepath.Join("testdata", "mod"))
	if err != nil {
		t.Fatal(err)
	}

	type summary struct {
		Name    string
		Recv    string
		Params  []param
		Results int
		Body    string
		Imports []string
	}

	var sums []summary

	for _, w := range got {
		s := summary{Name: w.String(), Params: w.Params, Results: w.Results, Body: w.Body, Imports: w.Imports}
		if w.Recv != nil {
			s.Recv = w.Recv.Name + " " + w.Recv.Type
		}

		sums = append(sums, s)
	}

	want := []summary{
		{
			Name:    "lib.NewMiddleware",
			Params:  []param{{Name: "b", Type: "*Bundle"}, {Name: "cfg", Type: "Config"}},
			Results: 2,
			Body:    "Middleware(b, cfg)",
		},
		{
			Name:    "lib.Swap",
			Params:  []param{{Name: "cfg", Type: "Config"}, {Name: "b", Type: "*Bundle"}},
			Results: 1,
			Body:    "Pair(b, cfg)",
		},
		{
			Name: "lib.(*Bundle).Do",
			Recv: "b *Bundle",
			Params: []param{
				{Name: "ctx", Type: "context.Context"},
				{Name: "path", Type: "string"},
				{Name: "args", Type: "string", Variadic: true},
			},
			Results: 1,
			Body:    "b.Call(ctx, path, args...)",
			Imports: []string{`context "context"`},
		},
		{Name: "lib.Log", Params: []param{{Name: "msg", Type: "string"}}, Body: `Logf("%s", msg)`},
	}
	if diff := cmp.Diff(want, sums); diff != "" {
		t.Errorf("findWrappers(testdata/mod) mismatch (-want +got):\n%s", diff)
	}
}

func TestFindWrappers_Unsupported(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, src, wantErr string
	}{
		{
			name:    "type parameters",
			src:     "package p\n\n//go:fix inline\nfunc Old[T any](v T) T { return New(v) }\n\nfunc New[T any](v T) T { return v }\n",
			wantErr: "type parameters",
		},
		{
			name:    "two statements",
			src:     "package p\n\n//go:fix inline\nfunc Old(v int) int { v++; return New(v) }\n\nfunc New(v int) int { return v }\n",
			wantErr: "not a single statement",
		},
		{
			name:    "not a call",
			src:     "package p\n\n//go:fix inline\nfunc Old(v int) int { return v + 1 }\n",
			wantErr: "not a single forwarded call",
		},
		{
			name:    "build constraint",
			src:     "//go:build linux\n\npackage p\n\n//go:fix inline\nfunc Old(v int) int { return New(v) }\n\nfunc New(v int) int { return v }\n",
			wantErr: "build constraint",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "p.go"), []byte(tt.src), 0o600); err != nil {
				t.Fatal(err)
			}

			_, err := findWrappers(dir)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("findWrappers() = %v, want an error containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestFindWrappers_SkipsTestsAndTestdata(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := []byte("package p\n\n//go:fix inline\nfunc Old[T any](v T) T { return v }\n")

	for _, rel := range []string{"p_test.go", filepath.Join("testdata", "p.go"), filepath.Join(".hidden", "p.go")} {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, src, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	got, err := findWrappers(dir)
	if err != nil || len(got) != 0 {
		t.Errorf("findWrappers() = %v, %v, want no wrappers", got, err)
	}
}

func TestGenCases(t *testing.T) {
	t.Parallel()

	w := wrapper{
		Package: "i18n", Name: "NewMiddleware", Results: 2, Body: "Middleware(b, cfg)",
		Params: []param{{Name: "b", Type: "*Bundle"}, {Name: "cfg", Type: "MiddlewareConfig"}},
	}

	src, got, err := genCases(w)
	if err != nil {
		t.Fatal(err)
	}

	want := []caseCall{
		{
			Wrapper: "i18n.NewMiddleware",
			Kind:    "same",
			Call:    "_, _ = NewMiddleware(b, cfg)",
			Want:    "_, _ = Middleware(b, cfg)",
		},
		{
			Wrapper: "i18n.NewMiddleware", Kind: "rotated",
			Call: "_, _ = NewMiddleware(cfg, b)", Want: "_, _ = Middleware(cfg, b)",
		},
		{
			Wrapper: "i18n.NewMiddleware", Kind: "literal-0",
			Call: "_, _ = NewMiddleware(*new(*Bundle), cfg)", Want: "_, _ = Middleware(*new(*Bundle), cfg)",
		},
		{
			Wrapper: "i18n.NewMiddleware",
			Kind:    "literal-1",
			Call:    "_, _ = NewMiddleware(b, *new(MiddlewareConfig))",
			Want:    "_, _ = Middleware(b, *new(MiddlewareConfig))",
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("genCases() mismatch (-want +got):\n%s", diff)
	}

	for _, frag := range []string{
		"func _(cfg *Bundle, b MiddlewareConfig) {\n\t_, _ = NewMiddleware(cfg, b)\n}",
		"func _(b *Bundle, cfg MiddlewareConfig) {\n\t_ = cfg\n\t_, _ = NewMiddleware(b, *new(MiddlewareConfig))\n}",
	} {
		if !strings.Contains(src, frag) {
			t.Errorf("genCases() source = %q, want it to contain %q", src, frag)
		}
	}
}

func TestSubstitute(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, body string
		subst      map[string]string
		want       string
	}{
		{"simultaneous swap", "New(b, cfg)", map[string]string{"b": "cfg", "cfg": "b"}, "New(cfg, b)"},
		{"selector field kept", "New(cfg.b, b)", map[string]string{"b": "x", "cfg": "y"}, "New(y.b, x)"},
		{"composite key kept", "New(Opts{b: b})", map[string]string{"b": "x"}, "New(Opts{b: x})"},
		{"method receiver", "c.Call(ctx, args...)", map[string]string{"c": "ctx", "ctx": "c"}, "ctx.Call(c, args...)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := substitute(tt.body, tt.subst)
			if err != nil || got != tt.want {
				t.Errorf("substitute(%q) = %q, %v, want %q", tt.body, got, err, tt.want)
			}
		})
	}
}

func TestChangedLines(t *testing.T) {
	t.Parallel()

	file := filepath.Join("tmp", "pkg0.go")
	other := filepath.Join("repo", "i18n", "middleware.go")
	diff := "--- " + other + " (old)\r\n+++ " + other + " (new)\r\n" +
		"@@ -1,1 +1,1 @@\r\n-\tfor i := 0; i < n; i++ {\r\n+\tfor i := range n {\r\n" +
		"--- " + file + " (old)\n+++ " + file + " (new)\n@@ -4,3 +4,3 @@\n func _(b *Bundle) {\n" +
		"-\t_ = Old(b)\n+\t_ = New(b)\n }\n"

	removed, added := changedLines(diff, file)
	if diff := cmp.Diff([][]string{{"_ = Old(b)"}, {"_ = New(b)"}}, [][]string{removed, added}); diff != "" {
		t.Errorf("changedLines() mismatch (-want +got):\n%s", diff)
	}
}

func TestVerify(t *testing.T) {
	t.Parallel()

	cases := []caseCall{
		{
			Wrapper: "i18n.NewMiddleware",
			Kind:    "same",
			Call:    "_, _ = NewMiddleware(b, cfg)",
			Want:    "_, _ = Middleware(b, cfg)",
		},
		{
			Wrapper: "i18n.NewMiddleware", Kind: "literal-1",
			Call: "_, _ = NewMiddleware(b, *new(Config))", Want: "_, _ = Middleware(b, *new(Config))",
		},
	}

	tests := []struct {
		name           string
		removed, added []string
		wantNotes      int
		wantErr        string
	}{
		{
			name:    "all inlined",
			removed: []string{"_, _ = NewMiddleware(b, cfg)", "_, _ = NewMiddleware(b, *new(Config))"},
			added:   []string{"_, _ = Middleware(b, cfg)", "_, _ =   Middleware(b, *new(Config))"},
		},
		{
			// The Go 1.27.2 report: NewMiddleware(b, MiddlewareConfig{}) became
			// Middleware(MiddlewareConfig{}, cfg).
			name:    "swapped arguments fail",
			removed: []string{"_, _ = NewMiddleware(b, cfg)", "_, _ = NewMiddleware(b, *new(Config))"},
			added:   []string{"_, _ = Middleware(b, cfg)", "_, _ = Middleware(*new(Config), cfg)"},
			wantErr: `rewrote "_, _ = NewMiddleware(b, *new(Config))" into "_, _ = Middleware(*new(Config), cfg)"`,
		},
		{
			name:      "a left-alone literal call is a note",
			removed:   []string{"_, _ = NewMiddleware(b, cfg)"},
			added:     []string{"_, _ = Middleware(b, cfg)"},
			wantNotes: 1,
		},
		{
			name:      "a left-alone same call fails",
			removed:   []string{"_, _ = NewMiddleware(b, *new(Config))"},
			added:     []string{"_, _ = Middleware(b, *new(Config))"},
			wantErr:   `did not inline "_, _ = NewMiddleware(b, cfg)"`,
			wantNotes: 0,
		},
		{
			name:    "a binding declaration fails",
			removed: []string{"_, _ = NewMiddleware(b, cfg)"},
			added:   []string{"var cfg = cfg", "_, _ = Middleware(b, cfg)"},
			wantErr: "did not replace each call by one line",
		},
		{
			name: "extra changes fail",
			removed: []string{
				"_, _ = NewMiddleware(b, cfg)", "_, _ = NewMiddleware(b, *new(Config))", "x := 1",
			},
			added:   []string{"_, _ = Middleware(b, cfg)", "_, _ = Middleware(b, *new(Config))", "x := 2"},
			wantErr: "not generated calls",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			notes, errs := verify(cases, tt.removed, tt.added)
			if len(notes) != tt.wantNotes {
				t.Errorf("verify() notes = %q, want %d", notes, tt.wantNotes)
			}

			var msg []string
			for _, err := range errs {
				msg = append(msg, err.Error())
			}

			got := strings.Join(msg, "\n")
			if (tt.wantErr == "") != (got == "") || !strings.Contains(got, tt.wantErr) {
				t.Errorf("verify() errors = %q, want %q", got, tt.wantErr)
			}
		})
	}
}

// TestCheck runs go fix on the fixture module: its wrappers reuse the
// parameter names callers use (b, cfg, ctx), the shape that was mis-inlined.
func TestCheck(t *testing.T) {
	t.Parallel()

	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not in PATH")
	}

	var out bytes.Buffer
	if err := check(t.Context(), filepath.Join("testdata", "mod"), &out, true); err != nil {
		t.Fatalf("check(testdata/mod) = %v\n%s", err, out.String())
	}

	for _, want := range []string{
		"lib.NewMiddleware rotated: _, _ = NewMiddleware(cfg, b) -> _, _ = Middleware(cfg, b)",
		"lib.Swap same: _ = Swap(cfg, b) -> _ = Pair(b, cfg)",
		"fixinline: 4 wrappers, 14 generated calls, none mis-inlined",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("check() output = %q, want it to contain %q", out.String(), want)
		}
	}
}
