package session

import (
	"bytes"
	"net/http"
	"testing"
)

func TestSecurity_A04_CookieSessionKeyRotation(t *testing.T) {
	t.Parallel()

	oldKey, newKey := bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)

	before, _ := newManager(t, Config{CookieKey: oldKey})
	rotated, _ := newManager(
		t,
		Config{CookieKey: newKey, PreviousCookieKeys: [][]byte{bytes.Repeat([]byte{3}, 32), oldKey}},
	)
	dropped, _ := newManager(t, Config{CookieKey: newKey})

	old := before.Cookies(t.Context(), map[string]any{"user": "alice"})

	if got := rotated.Peek(t.Context(), old)["user"]; got != "alice" {
		t.Fatalf("Peek(cookie sealed with a previous key)[user] = %v, want alice", got)
	}

	// Reading the session on a request re-seals it with the current key.
	c := newClient(t, appHandler(rotated, &bytes.Buffer{}))
	c.cookies[rotated.CookieName()] = old[0]

	if got := c.do(http.MethodGet, "/get", nil).Body.String(); got != "alice" {
		t.Fatalf("GET /get with an old-key cookie = %q, want alice", got)
	}

	resealed := c.cookies[rotated.CookieName()]
	if resealed.Value == old[0].Value {
		t.Fatal("session opened with a previous key was not re-sealed")
	}

	if got := dropped.Peek(t.Context(), []*http.Cookie{resealed})["user"]; got != "alice" {
		t.Errorf("Peek(re-sealed cookie) with the current key only [user] = %v, want alice", got)
	}

	if got := dropped.Peek(t.Context(), old); len(got) != 0 {
		t.Errorf("Peek(old cookie) after the previous key is removed = %v, want an empty session", got)
	}

	if _, err := New(Config{CookieKey: newKey, PreviousCookieKeys: [][]byte{[]byte("short")}}); err == nil {
		t.Error("New(short PreviousCookieKeys entry) error = nil, want an error")
	}
}
