package main

import (
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestExtract(t *testing.T) {
	mdx := "text\n" +
		"```go title=\"routes/web.go\"\nr.Get(\"/\", h)\n```\n" +
		"```go\nuntitled()\n```\n" +
		"```go title=\"a.go\" nocompile\nskipped()\n```\n" +
		"```sh title=\"x.go\"\nnot go\n```\n"

	got, err := extract("page.mdx", strings.NewReader(mdx))
	if err != nil {
		t.Fatal(err)
	}

	want := []block{{file: "page.mdx", line: 3, code: "r.Get(\"/\", h)"}}
	if diff := cmp.Diff(want, got, cmp.AllowUnexported(block{})); diff != "" {
		t.Errorf("extract mismatch (-want +got):\n%s", diff)
	}
}

func TestCheck(t *testing.T) {
	t.Chdir("../../..")

	c, err := newChecker()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		code    string
		wantErr string
	}{
		{"valid handler", "func h(c *web.Ctx) error {\n\treturn c.String(http.StatusOK, \"ok\")\n}", ""},
		{"valid fragment ignores locals", "r.Get(\"/\", func(c *web.Ctx) error { return c.String(200, \"x\") })", ""},
		{
			"unknown method",
			"r.Get(\"/\", func(c *web.Ctx) error { return c.NaoExiste() })",
			"has no field or method NaoExiste",
		},
		{"unknown package member", "var _ = web.NaoExiste", "undefined: web.NaoExiste"},
		{"unparseable", "func (", "cannot parse"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := strings.Join(c.check(tt.code), "\n")

			if tt.wantErr == "" && got != "" {
				t.Errorf("check() = %q, want no problems", got)
			}

			if !strings.Contains(got, tt.wantErr) {
				t.Errorf("check() = %q, want it to contain %q", got, tt.wantErr)
			}
		})
	}
}
