package cli

import (
	"os"
	"strings"
	"testing"
)

// A published port without a host address binds on every interface, which
// exposes Postgres (default user and password) and Mailpit to the network.
// Every entry must be bound to loopback, in flow and block style.
func TestSecurity_A02_ComposeTemplateBindsLoopback(t *testing.T) {
	t.Parallel()

	for _, f := range []string{"standards/compose.yaml.tmpl", "../../.config/compose.yaml"} {
		t.Run(f, func(t *testing.T) {
			t.Parallel()

			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}

			ports := composePorts(string(b))
			if len(ports) == 0 {
				t.Fatalf("%s: no ports entries found, the check would pass vacuously", f)
			}

			for _, p := range ports {
				if !strings.HasPrefix(p, "127.0.0.1:") {
					t.Errorf("%s publishes %q on all interfaces, want 127.0.0.1:<host>:<container>", f, p)
				}
			}
		})
	}
}

// composePorts returns the unquoted entries under every ports: key, written
// either as ports: ["a", "b"] or as a block of "- a" lines.
func composePorts(src string) []string {
	var out []string
	inBlock := false

	for line := range strings.SplitSeq(src, "\n") {
		trimmed := strings.TrimSpace(line)

		switch {
		case strings.HasPrefix(trimmed, "ports:"):
			rest := strings.TrimSpace(strings.TrimPrefix(trimmed, "ports:"))
			inBlock = rest == ""
			if strings.HasPrefix(rest, "[") && strings.HasSuffix(rest, "]") {
				for item := range strings.SplitSeq(rest[1:len(rest)-1], ",") {
					if v := unquote(item); v != "" {
						out = append(out, v)
					}
				}
			}
		case inBlock && strings.HasPrefix(trimmed, "- "):
			if v := unquote(strings.TrimPrefix(trimmed, "- ")); v != "" {
				out = append(out, v)
			}
		default:
			inBlock = false
		}
	}

	return out
}

func unquote(s string) string {
	return strings.Trim(strings.TrimSpace(s), `"'`)
}
