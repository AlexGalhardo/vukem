package signed

import (
	"errors"
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

func TestSecurity_A04_SignedURLKeyRotation(t *testing.T) {
	t.Parallel()

	oldKey := []byte(strings.Repeat("o", MinKeyLen))
	newKey := []byte(strings.Repeat("n", MinKeyLen))
	other := []byte(strings.Repeat("x", MinKeyLen))

	oldLink, err := New(oldKey).Sign("https://app.test/unsubscribe/7", 0)
	if err != nil {
		t.Fatal(err)
	}

	rotated := New(newKey).WithPreviousKeys(other, oldKey)
	if err := rotated.Verify(req(t, oldLink)); err != nil {
		t.Errorf("Verify(link signed with a previous key) = %v, want nil", err)
	}

	newLink, err := rotated.Sign("https://app.test/unsubscribe/7", 0)
	if err != nil {
		t.Fatal(err)
	}

	if err := New(newKey).Verify(req(t, newLink)); err != nil {
		t.Errorf("Verify(link signed after rotation) with the current key only = %v, want nil", err)
	}

	if err := New(oldKey).Verify(req(t, newLink)); !errors.Is(err, ErrInvalid) {
		t.Errorf("Verify(new link) with the old key = %v, want ErrInvalid (Sign must use the current key)", err)
	}

	if err := New(newKey).Verify(req(t, oldLink)); !errors.Is(err, ErrInvalid) {
		t.Errorf("Verify(old link) after the previous key is removed = %v, want ErrInvalid", err)
	}
}
