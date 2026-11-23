package cli

import (
	"regexp"
	"strings"
	"testing"
)

// The generated project lints its Markdown with MD034 on, which rejects a
// bare URL or e-mail address (VF-092). This checks the template without
// running Node: outside fenced blocks and code spans there must be neither.
func TestFinanceSetupLocalHasNoBareLinks(t *testing.T) {
	t.Parallel()

	files, err := loadFinanceScaffold()
	if err != nil {
		t.Fatal(err)
	}

	var b []byte

	for _, f := range files {
		if f.rel == "setup-local.md" {
			b = f.content
		}
	}

	if b == nil {
		t.Fatal("the finance scaffold has no setup-local.md")
	}

	action := regexp.MustCompile(`{{[^}]*}}`)
	span := regexp.MustCompile("`[^`]*`")
	bare := regexp.MustCompile(`https?://\S|[\w.+-]+@[\w-]+\.\w`)
	fenced := false

	for i, line := range strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced

			continue
		}

		if fenced {
			continue
		}

		prose := span.ReplaceAllString(action.ReplaceAllString(line, "x"), "")
		if m := bare.FindString(prose); m != "" {
			t.Errorf("setup-local.md:%d has a bare link %q outside backticks (MD034)", i+1, m)
		}
	}
}
