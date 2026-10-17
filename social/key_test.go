package social

import (
	"fmt"
	"strings"
	"testing"
)

func TestSecurity_A04_SocialRefusesWeakKeys(t *testing.T) {
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

			msg = func() (msg string) {
				defer func() { msg = fmt.Sprint(recover()) }()

				New([]byte(strings.Repeat("k", MinKeyLen))).WithPreviousKeys(tt.key)

				return ""
			}()
			if !strings.Contains(msg, "32 bytes") {
				t.Fatalf("WithPreviousKeys(%d-byte key) panic = %q, want a key length message", len(tt.key), msg)
			}
		})
	}
}

func TestSecurity_A04_SocialStateKeyRotation(t *testing.T) {
	t.Parallel()

	oldKey := []byte(strings.Repeat("o", MinKeyLen))
	newKey := []byte(strings.Repeat("n", MinKeyLen))
	st := statePayload{Provider: "github", State: "s", Verifier: "v", Expires: 1}

	old := New(oldKey).seal(st)
	rotated := New(newKey).WithPreviousKeys([]byte(strings.Repeat("x", MinKeyLen)), oldKey)

	if got, ok := rotated.open(old); !ok || got != st {
		t.Errorf("open(state signed with a previous key) = %+v, %v, want %+v, true", got, ok, st)
	}

	fresh := rotated.seal(st)
	if _, ok := New(newKey).open(fresh); !ok {
		t.Error("open(state signed after rotation) with the current key only = false, want true")
	}

	if _, ok := New(oldKey).open(fresh); ok {
		t.Error("open(new state) with the old key = true, want false (seal must use the current key)")
	}

	if _, ok := New(newKey).open(old); ok {
		t.Error("open(old state) after the previous key is removed = true, want false")
	}
}
