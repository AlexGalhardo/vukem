package signed

import (
	"bytes"
	"testing"
)

func FuzzOpen(f *testing.F) {
	s := New(bytes.Repeat([]byte{7}, 32))
	f.Add(s.Token([]byte(`{"after":42}`)))
	f.Add("a.b")
	f.Add("")

	f.Fuzz(func(t *testing.T, token string) {
		payload, err := s.Open(token)
		if err != nil {
			return
		}

		if s.Token(payload) != token {
			t.Fatalf("Open(%q) accepted a token Token does not produce", token)
		}
	})
}
