package web

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// The Ctx table in docs/http.md must only list methods that exist.
func TestDocsCtxTableListsRealMethods(t *testing.T) {
	raw, err := os.ReadFile("../docs/http.md")
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(raw), "## `*web.Ctx`")
	if !ok {
		t.Fatal("Ctx section not found in docs/http.md")
	}
	section, _, _ = strings.Cut(section, "\n## ")

	typ := reflect.TypeFor[*Ctx]()
	tok := regexp.MustCompile("`([A-Z][A-Za-z0-9]*)[`(\\[]")
	checked := 0
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(line, "| ") {
			continue
		}
		for _, m := range tok.FindAllStringSubmatch(line, -1) {
			checked++
			if _, ok := typ.MethodByName(m[1]); !ok {
				t.Errorf("docs/http.md lists Ctx.%s, which does not exist", m[1])
			}
		}
	}
	if checked == 0 {
		t.Fatal("no methods parsed from the Ctx table")
	}
}
