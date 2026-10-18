package mediatype_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/mediatype"
)

// A109: the framework had a MIME-to-extension table only inside upload,
// keyed on what http.DetectContentType can detect and framed entirely as
// input validation. A service that generates a file needs the other
// direction: what to call the thing it just made.
func TestExtensionAndLabel(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		typ, ext, label string
	}{
		{"image/png", ".png", "PNG image"},
		{"image/jpeg", ".jpg", "JPEG image"},
		// Detectable or not is irrelevant here: this is what the
		// application produced, not what a client sent.
		{"image/avif", ".avif", "AVIF image"},
		{"image/tiff", ".tiff", "TIFF image"},
		{"application/pdf", ".pdf", "PDF document"},
		{"video/mp4", ".mp4", "MP4 video"},
		// Parameters are ignored.
		{"text/csv; charset=utf-8", ".csv", "CSV spreadsheet"},
		{"IMAGE/PNG", ".png", "PNG image"},
		// A type this package does not name answers nothing, rather than
		// guessing an extension from the subtype.
		{"application/x-made-up", "", ""},
		{"", "", ""},
	} {
		if got := mediatype.Extension(tt.typ); got != tt.ext {
			t.Errorf("Extension(%q) = %q, want %q", tt.typ, got, tt.ext)
		}

		if got := mediatype.Label(tt.typ); got != tt.label {
			t.Errorf("Label(%q) = %q, want %q", tt.typ, got, tt.label)
		}
	}
}

func TestByExtension(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ ext, typ string }{
		{".png", "image/png"},
		{"png", "image/png"},
		{".PNG", "image/png"},
		{".mp4", "video/mp4"},
		// Only the canonical spelling: .jpg is what Extension returns, so
		// .jpg is what comes back.
		{".jpg", "image/jpeg"},
		{".jpeg", ""},
		{".nope", ""},
		{"", ""},
	} {
		if got := mediatype.ByExtension(tt.ext); got != tt.typ {
			t.Errorf("ByExtension(%q) = %q, want %q", tt.ext, got, tt.typ)
		}
	}
}

// Every type round-trips through its own extension, so the two tables
// cannot drift apart.
func TestRoundTrip(t *testing.T) {
	t.Parallel()

	for _, typ := range typesInSource(t) {
		ext := mediatype.Extension(typ)
		if ext == "" {
			t.Errorf("Extension(%q) is empty", typ)
			continue
		}

		if got := mediatype.ByExtension(ext); got != typ {
			t.Errorf("ByExtension(Extension(%q)) = %q", typ, got)
		}

		if mediatype.Label(typ) == "" {
			t.Errorf("Label(%q) is empty", typ)
		}
	}
}

// The constraint from the finding, enforced rather than documented: this
// package must not become the set of types an application **accepts**. An
// input policy belongs to the application, and is often the only thing
// keeping a vulnerable decoder unreachable — an app may rely on "no tool
// here accepts XML input" or "EXR is not a recognised type", and a shared
// list that grew a type would remove that argument with no edit to the
// app.
//
// So there is nothing to iterate: no All, no Types, no Known, no Has. A
// caller cannot turn these lookups into an allowlist, and adding one of
// those functions fails here.
func TestNoExportedListToMistakeForAnInputAllowlist(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, "mediatype.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]bool{"Extension": true, "Label": true, "ByExtension": true}

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if !d.Name.IsExported() {
				continue
			}

			if !want[d.Name.Name] {
				t.Errorf("mediatype exports %s; this package must stay three lookups so it "+
					"cannot be used as an input allowlist", d.Name.Name)
			}

			delete(want, d.Name.Name)
		case *ast.GenDecl:
			// No exported variables, constants or types either: an
			// exported map or slice is an iterable list by another name.
			if d.Tok != token.VAR && d.Tok != token.TYPE && d.Tok != token.CONST {
				continue
			}

			for _, name := range declaredNames(d) {
				if ast.IsExported(name) {
					t.Errorf("mediatype exports %s; an exported list is an input allowlist "+
						"waiting to happen", name)
				}
			}
		}
	}

	for name := range want {
		t.Errorf("mediatype no longer exports %s", name)
	}
}

func declaredNames(gd *ast.GenDecl) []string {
	var out []string

	for _, spec := range gd.Specs {
		switch s := spec.(type) {
		case *ast.ValueSpec:
			for _, n := range s.Names {
				out = append(out, n.Name)
			}
		case *ast.TypeSpec:
			out = append(out, s.Name.Name)
		}
	}

	return out
}

// typesInSource reads the media types out of the package source, so the
// round-trip test covers the real table without the package exporting it.
func typesInSource(t *testing.T) []string {
	t.Helper()

	fset := token.NewFileSet()

	file, err := parser.ParseFile(fset, "mediatype.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	var out []string

	ast.Inspect(file, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}

		key, ok := kv.Key.(*ast.BasicLit)
		if !ok || key.Kind != token.STRING || !strings.Contains(key.Value, "/") {
			return true
		}

		out = append(out, strings.Trim(key.Value, `"`))

		return true
	})

	if len(out) < 20 {
		t.Fatalf("found %d types in the source, expected the whole table", len(out))
	}

	return out
}
