package studio

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

var (
	inlineScript = regexp.MustCompile(`(?is)<script\b(?:[^>]*)>`)
	srcAttr      = regexp.MustCompile(`(?i)\bsrc\s*=`)
	inlineStyle  = regexp.MustCompile(`(?i)<style\b`)
	styleAttr    = regexp.MustCompile(`(?i)\sstyle\s*=`)
)

// cspViolations lists what the strict CSP (no unsafe-eval, no unsafe-inline)
// would block or what would need a relaxed policy, in the files of a build.
func cspViolations(files fs.FS) ([]string, error) {
	var out []string

	err := fs.WalkDir(files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}

		data, err := fs.ReadFile(files, p)
		if err != nil {
			return err
		}

		text := string(data)

		switch path.Ext(p) {
		case ".js":
			for _, bad := range []string{"eval(", "new Function("} {
				if strings.Contains(text, bad) {
					out = append(out, p+": contains "+bad)
				}
			}
		case ".html":
			for _, tag := range inlineScript.FindAllString(text, -1) {
				if !srcAttr.MatchString(tag) {
					out = append(out, p+": inline <script>")
				}
			}

			if inlineStyle.MatchString(text) {
				out = append(out, p+": inline <style>")
			}

			if styleAttr.MatchString(text) {
				out = append(out, p+": style attribute")
			}
		}

		return nil
	})

	return out, err
}

func TestBuiltUIIsCSPClean(t *testing.T) {
	t.Parallel()

	sub, err := fs.Sub(uiFS, "ui")
	if err != nil {
		t.Fatal(err)
	}

	got, err := cspViolations(sub)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) > 0 {
		t.Errorf("the built UI breaks the CSP: %v", got)
	}
}

func TestCSPScannerCatchesViolations(t *testing.T) {
	t.Parallel()

	clean := fstest.MapFS{
		"index.html":   {Data: []byte(`<script type="module" src="/ui/assets/a.js"></script><link rel="stylesheet" href="/ui/assets/a.css">`)},
		"assets/a.js":  {Data: []byte(`console.log(1)`)},
		"assets/a.css": {Data: []byte(`body{margin:0}`)},
	}

	if got, err := cspViolations(clean); err != nil || len(got) != 0 {
		t.Fatalf("cspViolations(clean) = %v, %v, want none", got, err)
	}

	tests := map[string]fstest.MapFS{
		"eval":          {"assets/a.js": {Data: []byte(`x=eval("1")`)}},
		"new Function":  {"assets/a.js": {Data: []byte(`x=new Function("return 1")`)}},
		"inline script": {"index.html": {Data: []byte(`<script>alert(1)</script>`)}},
		"inline style":  {"index.html": {Data: []byte(`<style>a{}</style>`)}},
		"style attr":    {"index.html": {Data: []byte(`<div style="color:red"></div>`)}},
	}

	for name, files := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got, err := cspViolations(files); err != nil || len(got) == 0 {
				t.Errorf("cspViolations(%s) = %v, %v, want a violation", name, got, err)
			}
		})
	}
}
