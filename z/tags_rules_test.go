package z

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// implementedRules returns every rule name the tag parser branches on, read
// from the source: each `case "..."` of a rule switch in the tags*.go files.
func implementedRules(t *testing.T) []string {
	t.Helper()

	files, err := filepath.Glob("tags*.go")
	if err != nil {
		t.Fatal(err)
	}

	var names []string

	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}

		file, err := parser.ParseFile(token.NewFileSet(), name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}

		collectCases(file, &names)
	}

	slices.Sort(names)

	return names
}

func collectCases(file *ast.File, names *[]string) {
	ast.Inspect(file, func(n ast.Node) bool {
		clause, ok := n.(*ast.CaseClause)
		if !ok {
			return true
		}

		for _, expr := range clause.List {
			lit, ok := expr.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}

			name, err := strconv.Unquote(lit.Value)
			if err != nil || name == "" {
				continue // the empty rule of parseRules, or a non-literal case
			}

			if !slices.Contains(*names, name) {
				*names = append(*names, name)
			}
		}

		return true
	})
}

func ruleNames() []string {
	names := make([]string, 0, len(tagRules()))
	for _, r := range tagRules() {
		names = append(names, r.name)
	}

	slices.Sort(names)

	return names
}

// TestTagRules_MatchImplementation fails when a rule is implemented without
// being listed in tagRules (and so documented), or listed without being
// implemented.
func TestTagRules_MatchImplementation(t *testing.T) {
	t.Parallel()

	if diff := cmp.Diff(implementedRules(t), ruleNames()); diff != "" {
		t.Fatalf("tagRules out of step with tags*.go (-implemented +listed):\n%s", diff)
	}
}

// TestTagRules_DocumentedOnFromTags checks the rule block of the FromTags
// doc comment lists every rule under every scope it applies to. Only that
// block counts, so a rule named in the surrounding prose does not stand in
// for a missing list entry.
func TestTagRules_DocumentedOnFromTags(t *testing.T) {
	t.Parallel()

	file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(".", "tags.go"), nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse tags.go: %v", err)
	}

	var doc string

	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Name.Name == "FromTags" {
			doc = fn.Doc.Text()
		}
	}

	if doc == "" {
		t.Fatal("FromTags has no doc comment")
	}

	sections := docScopes(t, doc)

	for _, rule := range tagRules() {
		for _, scope := range rule.scopes {
			if !wordRE(rule.name).MatchString(sections[scope]) {
				t.Errorf("rule %q is implemented but missing from the %q line of the FromTags doc comment",
					rule.name, scope)
			}
		}
	}
}

// docScopes splits the indented "scope: rule, rule" block of the FromTags
// doc comment into one text per scope, tolerating wrapped lines.
func docScopes(t *testing.T, doc string) map[tagScope]string {
	t.Helper()

	sections := map[tagScope]string{}
	current := tagScope("")

	for line := range strings.SplitSeq(doc, "\n") {
		if !strings.HasPrefix(line, "\t") {
			current = "" // out of the indented block

			continue
		}

		text := strings.TrimSpace(line)

		for _, scope := range []tagScope{scopeAny, scopeString, scopeNumber, scopeList} {
			if label := string(scope) + ":"; strings.HasPrefix(text, label) {
				current = scope
				text = strings.TrimPrefix(text, label)

				break
			}
		}

		if current != "" {
			sections[current] += " " + text
		}
	}

	if len(sections) != 4 {
		t.Fatalf("FromTags doc comment lists %d scopes, want 4 (any, string, number, list)", len(sections))
	}

	return sections
}

// TestTagRules_DocumentedInValidationDocs checks the first column of the rule
// table in docs/validation.md is exactly the implemented rule set.
func TestTagRules_DocumentedInValidationDocs(t *testing.T) {
	t.Parallel()

	page := filepath.Join("..", "docs", "validation.md")

	b, err := os.ReadFile(page)
	if err != nil {
		t.Fatalf("read %s: %v", page, err)
	}

	if diff := cmp.Diff(ruleNames(), tableRules(t, string(b))); diff != "" {
		t.Fatalf("the rule table of %s is out of step with the implementation (-implemented +documented):\n%s",
			page, diff)
	}
}

// tableRules returns the rule names of the "| Rule | Applies to |" table of
// the validation page: the first cell of each row, without its backticks and
// its example argument.
func tableRules(t *testing.T, page string) []string {
	t.Helper()

	lines := strings.Split(page, "\n")

	start := -1

	for i, line := range lines {
		if strings.HasPrefix(line, "| Rule |") && strings.Contains(line, "Applies to") {
			start = i

			break
		}
	}

	if start < 0 {
		t.Fatal(`docs/validation.md has no "| Rule | Applies to |" table`)
	}

	var names []string

	bare := regexp.MustCompile(`^[a-z]+$`)

	for _, line := range lines[start+1:] {
		if !strings.HasPrefix(line, "|") {
			break
		}

		cell, _, _ := strings.Cut(strings.TrimPrefix(line, "|"), "|")
		cell = strings.Trim(strings.TrimSpace(cell), "`")

		if name, _, _ := strings.Cut(cell, "="); bare.MatchString(name) {
			names = append(names, name)
		}
	}

	slices.Sort(names)

	return names
}

func wordRE(name string) *regexp.Regexp {
	return regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
}

// TestTagRules_ApplyToTheirScopes checks every listed rule is accepted on the
// field kinds tagRules claims and rejected on the others.
func TestTagRules_ApplyToTheirScopes(t *testing.T) {
	t.Parallel()

	kinds := map[tagScope]reflect.Type{
		scopeString: reflect.TypeFor[string](),
		scopeNumber: reflect.TypeFor[int](),
		scopeList:   reflect.TypeFor[[]string](),
	}

	for _, rule := range tagRules() {
		tag := rule.name
		if rule.arg != "" {
			tag += "=" + rule.arg
		}

		anyKind := slices.Contains(rule.scopes, scopeAny)

		// A bool takes no rule but the scopeAny ones.
		_, err := schemaForType(reflect.TypeFor[bool](), []string{tag}, 0)
		if anyKind && err != nil {
			t.Errorf("rule %q on a bool: %v, want it accepted on any kind", tag, err)
		}

		if !anyKind && err == nil {
			t.Errorf("rule %q on a bool: accepted, want an error", tag)
		}

		for scope, rt := range kinds {
			_, err := schemaForType(rt, []string{tag}, 0)

			want := anyKind || slices.Contains(rule.scopes, scope)
			if want && err != nil {
				t.Errorf("rule %q on a %s (%s): %v, want it accepted", tag, scope, rt, err)
			}

			if !want && err == nil {
				t.Errorf("rule %q on a %s (%s): accepted, want an error", tag, scope, rt)
			}
		}
	}
}
