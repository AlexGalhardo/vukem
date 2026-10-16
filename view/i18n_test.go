package view

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/AlexGalhardo/vukem/i18n"
)

func TestTranslateFunc(t *testing.T) {
	t.Parallel()

	b, err := i18n.New(i18n.Options{})
	if err != nil {
		t.Fatal(err)
	}

	for locale, msgs := range map[string]map[string]any{
		"en": {"posts": map[string]any{"created": "Post <b>created</b>", "count": map[string]any{"one": "{count} post", "other": "{count} posts"}}},
		"pt": {"posts": map[string]any{"created": "Post criado"}},
	} {
		if err := b.Add(locale, msgs); err != nil {
			t.Fatal(err)
		}
	}

	fsys := fstest.MapFS{
		"page.html": {
			Data: []byte(
				`{{/* layout: none */}}<html lang="{{locale}}">{{t "posts.created"}}|{{t "posts.count" "count" 3}}|{{t "nope"}}`,
			),
		},
	}

	tests := []struct {
		name   string
		bundle *i18n.Bundle
		ctx    context.Context
		want   string
	}{
		{"default locale", b, context.Background(), `<html lang="en">Post &lt;b&gt;created&lt;/b&gt;|3 posts|nope`},
		{"request locale", b, i18n.WithLocale(context.Background(), "pt"), `<html lang="pt">Post criado|3 posts|nope`},
		{"no bundle", nil, context.Background(), `<html lang="">posts.created|posts.count|nope`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e, err := New(Config{FS: fsys, I18n: tt.bundle})
			if err != nil {
				t.Fatal(err)
			}

			if got := render(tt.ctx, t, e, "page", nil); got != tt.want {
				t.Errorf("Render(page) = %q, want %q", got, tt.want)
			}
		})
	}
}
