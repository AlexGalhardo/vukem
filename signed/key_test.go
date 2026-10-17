package signed

import (
	"fmt"
	"strings"
	"testing"
)

func TestSecurity_A04_SignedRefusesWeakKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  []byte
	}{
		{name: "nil", key: nil},
		{name: "empty", key: []byte{}},
		{name: "short", key: []byte("0123456789abcdef0123456789abcde")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			msg := func() (msg string) {
				defer func() { msg = fmt.Sprint(recover()) }()

				New(tt.key)

				return ""
			}()
			if !strings.Contains(msg, "32 bytes") {
				t.Fatalf("New(%d-byte key) panic = %q, want a key length message", len(tt.key), msg)
			}
		})
	}
}
