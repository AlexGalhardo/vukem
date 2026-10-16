package web

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

type memSession map[string]any

func (m memSession) Get(k string) any    { return m[k] }
func (m memSession) Put(k string, v any) { m[k] = v }
func (m memSession) Forget(ks ...string) {
	for _, k := range ks {
		delete(m, k)
	}
}
func (m memSession) Pull(k string) any     { v := m[k]; delete(m, k); return v }
func (m memSession) Flash(k string, v any) { m[k] = v }
func (memSession) Regenerate()             {}
func (memSession) Invalidate()             {}
func (memSession) ID() string              { return "id" }

func TestSessionHelpers(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(t.Context(), "GET", "/", http.NoBody)

	// Without session middleware.
	c, _ := newCtx(t, r, nil)
	if c.Session() != nil || c.Flash("a", 1) == nil || c.FlashValue("a") != nil || c.Old("x") != "" ||
		c.Errors() != nil {
		t.Fatal("helpers without session")
	}

	sess := memSession{
		OldInputKey: map[string]any{
			"title":  "T",
			"tags":   []any{"go", "web"},
			"n":      3,
			"author": map[string]any{"email": "e"},
			"empty":  []any{},
		},
		ErrorsKey: map[string][]string{"title": {"bad"}},
	}
	c, _ = newCtx(t, r.WithContext(WithSession(r.Context(), sess)), nil)

	if err := c.Flash("status", "ok"); err != nil || c.FlashValue("status") != "ok" {
		t.Fatal("Flash")
	}

	for field, want := range map[string]string{"title": "T", "tags": "go", "n": "3", "author.email": "e", "missing": "", "title.x": "", "empty": ""} {
		if got := c.Old(field); got != want {
			t.Errorf("Old(%q) = %q, want %q", field, got, want)
		}
	}

	if c.Errors()["title"][0] != "bad" {
		t.Fatal("Errors typed")
	}

	sess[ErrorsKey] = map[string]any{"title": []any{"from json"}}
	if c.Errors()["title"][0] != "from json" {
		t.Fatal("Errors decoded")
	}

	sess[ErrorsKey] = 42
	if c.Errors() != nil {
		t.Fatal("Errors bad type")
	}
}

type brokenView struct{}

func (brokenView) Render(context.Context, io.Writer) error { return errors.New("boom") }

func TestRenderWithoutViewsAndBrokenView(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(t.Context(), "GET", "/", http.NoBody)
	r.Header.Set("HX-Request", "true")

	c, _ := newCtx(t, r, nil)
	if err := c.Render("x", nil); err == nil {
		t.Fatal("Render without views")
	}
	if err := c.View(brokenView{}); err == nil {
		t.Fatal("broken view")
	}
	if !c.IsPartial() {
		t.Fatal("IsPartial")
	}

	c.HTMX().Trigger("x", nil)
	if c.Writer().Header().Get("HX-Trigger") != `{"x":null}` {
		t.Fatal("HTMX builder")
	}
}
