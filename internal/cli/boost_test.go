package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func readJSON(t *testing.T, p string) map[string]any {
	t.Helper()

	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("%s: %v", p, err)
	}

	return m
}

func TestBoostInstall(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, d := range []string{".cursor", ".vscode"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o750); err != nil {
			t.Fatal(err)
		}
	}

	files := map[string]string{
		"AGENTS.md": "# My app\r\n\r\nOwn notes.\r\n",
		".mcp.json": `{"mcpServers":{"other":{"command":"x"}},"keep":true}`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	for range 2 { // idempotent
		if code, out, errOut := vukem(t, dir, "boost:install"); code != 0 {
			t.Fatalf("boost:install = %d %s %s", code, out, errOut)
		}
	}

	agents, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}

	if s := string(agents); !strings.HasPrefix(s, "# My app\r\n\r\nOwn notes.") ||
		strings.Count(s, "<!-- vukem-boost:start -->") != 1 || !strings.Contains(s, "## Vukem (Go web framework)") {
		t.Errorf("AGENTS.md =\n%s", s)
	}

	if b, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md")); err != nil || string(b) != "@AGENTS.md\n" {
		t.Errorf("CLAUDE.md = %q, %v", b, err)
	}

	for _, s := range []string{"http", "validation", "database", "testing", "frontend", "cli"} {
		p := filepath.Join(dir, ".claude", "skills", "vukem-"+s, "SKILL.md")
		if b, err := os.ReadFile(p); err != nil || !strings.HasPrefix(string(b), "---\nname: vukem-"+s+"\n") {
			t.Errorf("skill %s = %v", s, err)
		}
	}

	server := map[string]any{"command": "go", "args": []any{"run", "./cmd/app", "boost:mcp"}}

	mcp := readJSON(t, filepath.Join(dir, ".mcp.json"))
	want := map[string]any{
		"keep":       true,
		"mcpServers": map[string]any{"other": map[string]any{"command": "x"}, "vukem-boost": server},
	}
	if diff := cmp.Diff(want, mcp); diff != "" {
		t.Errorf(".mcp.json (-want +got):\n%s", diff)
	}

	if got := readJSON(t, filepath.Join(dir, ".cursor", "mcp.json"))["mcpServers"]; !cmp.Equal(
		got,
		map[string]any{"vukem-boost": server},
	) {
		t.Errorf(".cursor/mcp.json servers = %v", got)
	}

	vscode := map[string]any{"type": "stdio", "command": "go", "args": []any{"run", "./cmd/app", "boost:mcp"}}
	if got := readJSON(t, filepath.Join(dir, ".vscode", "mcp.json"))["servers"]; !cmp.Equal(
		got,
		map[string]any{"vukem-boost": vscode},
	) {
		t.Errorf(".vscode/mcp.json servers = %v", got)
	}
}

func TestBoostInstallRejectsBrokenJSON(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}

	if code, _, errOut := vukem(t, dir, "boost:install"); code != 1 || !strings.Contains(errOut, ".mcp.json") {
		t.Errorf("boost:install with broken .mcp.json = %d %q, want 1 naming the file", code, errOut)
	}

	if b, _ := os.ReadFile(filepath.Join(dir, ".mcp.json")); string(b) != "{broken" {
		t.Errorf(".mcp.json overwritten: %q", b)
	}
}
