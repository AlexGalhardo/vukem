package cli

import (
	"os"
	"regexp"
	"testing"
)

var bareHostPort = regexp.MustCompile(`"\d+:\d+"`)

// A published port without a host address binds on every interface, which
// exposes Postgres (default user and password) and Mailpit to the network.
func TestSecurity_A05_ComposeTemplateBindsLoopback(t *testing.T) {
	t.Parallel()

	for _, f := range []string{"standards/compose.yaml.tmpl", "../../.config/compose.yaml"} {
		t.Run(f, func(t *testing.T) {
			t.Parallel()

			b, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}

			if m := bareHostPort.FindAll(b, -1); len(m) > 0 {
				t.Errorf("%s publishes ports on all interfaces: %q, want 127.0.0.1:<host>:<container>", f, m)
			}
		})
	}
}
