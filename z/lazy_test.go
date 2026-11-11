package z

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
)

type comment struct {
	Body    string    `json:"body"`
	Replies []comment `json:"replies"`
}

// newCommentSchema builds a schema that contains itself.
func newCommentSchema() Schema[comment] {
	var self Schema[comment]

	self = Struct[comment](Shape{
		"body":    String().Min(1),
		"replies": Slice(Lazy(func() Schema[comment] { return self })).Optional(),
	})

	return self
}

func TestLazy(t *testing.T) {
	t.Parallel()

	s := newCommentSchema()

	t.Run("recursive input", func(t *testing.T) {
		t.Parallel()

		in := map[string]any{"body": "a", "replies": []any{
			map[string]any{"body": "b", "replies": []any{
				map[string]any{"body": "c", "replies": []any{map[string]any{"body": "d"}}},
			}},
		}}

		got, err := s.Parse(in)
		if err != nil {
			t.Fatal(err)
		}

		want := comment{Body: "a", Replies: []comment{
			{Body: "b", Replies: []comment{{Body: "c", Replies: []comment{{Body: "d"}}}}},
		}}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("Parse (-want +got):\n%s", diff)
		}
	})

	t.Run("error path", func(t *testing.T) {
		t.Parallel()

		in := map[string]any{"body": "a", "replies": []any{
			map[string]any{"body": "b", "replies": []any{map[string]any{"body": ""}}},
		}}

		_, err := s.Parse(in)
		if diff := cmp.Diff([]string{"replies.0.replies.0.body:too_small"}, issueSet(t, err)); diff != "" {
			t.Errorf("issues (-want +got):\n%s", diff)
		}
	})

	t.Run("check", func(t *testing.T) {
		t.Parallel()

		if err := s.Check(comment{Body: "a", Replies: []comment{{Body: "b"}}}); err != nil {
			t.Errorf("Check(valid) = %v", err)
		}

		if err := s.Check(comment{Body: "a", Replies: []comment{{Body: ""}}}); err == nil {
			t.Error("Check(empty reply) = nil")
		}
	})

	t.Run("fn runs once", func(t *testing.T) {
		t.Parallel()

		var calls atomic.Int32

		lazy := Lazy(func() Schema[string] {
			calls.Add(1)
			return String()
		})

		var wg sync.WaitGroup

		for range 50 {
			wg.Go(func() {
				if _, err := lazy.Parse("x"); err != nil {
					t.Error(err)
				}

				// A copy shares the resolver.
				if _, err := lazy.Optional().Parse("y"); err != nil {
					t.Error(err)
				}
			})
		}

		wg.Wait()

		if n := calls.Load(); n != 1 {
			t.Errorf("fn ran %d times, want 1", n)
		}
	})

	t.Run("GoType does not resolve", func(t *testing.T) {
		t.Parallel()

		lazy := Lazy(func() Schema[int] { panic("resolved") })
		if lazy.GoType().Kind().String() != "int" {
			t.Errorf("GoType = %v", lazy.GoType())
		}
	})

	t.Run("nil function", func(t *testing.T) {
		t.Parallel()

		mustPanic(t, "Lazy(nil)", func() { Lazy[int](nil) })
	})

	t.Run("modifiers", func(t *testing.T) {
		t.Parallel()

		lazy := Lazy(func() Schema[string] { return String() })

		if got, err := lazy.Default("d").Parse(nil); err != nil || got != "d" {
			t.Errorf("Default: %v, %v", got, err)
		}

		if _, err := lazy.Parse(nil); err == nil {
			t.Error("a missing value passed a required Lazy")
		}
	})
}

// A recursive schema must export without looping: the second visit of the
// Lazy becomes a $ref.
func TestLazyJSONSchema(t *testing.T) {
	t.Parallel()

	js := JSONSchema(newCommentSchema())

	defs, _ := js["$defs"].(map[string]any)
	if len(defs) != 1 {
		t.Fatalf("$defs = %v", js["$defs"])
	}

	def, ok := defs["comment"].(map[string]any)
	if !ok {
		t.Fatalf("$defs has no comment entry: %v", defs)
	}

	replies := def["properties"].(map[string]any)["replies"].(map[string]any)
	if diff := cmp.Diff(map[string]any{"$ref": "#/$defs/comment"}, replies["items"]); diff != "" {
		t.Errorf("replies.items (-want +got):\n%s", diff)
	}

	// The root is the struct itself, whose items point at the same entry.
	rootReplies := js["properties"].(map[string]any)["replies"].(map[string]any)
	if diff := cmp.Diff(map[string]any{"$ref": "#/$defs/comment"}, rootReplies["items"]); diff != "" {
		t.Errorf("root replies.items (-want +got):\n%s", diff)
	}
}

func TestLazyJSONSchemaNames(t *testing.T) {
	t.Parallel()

	// Two lazies of the same Go type that are not the same schema get
	// distinct entries; the same one used twice shares its entry.
	a := Lazy(func() Schema[string] { return String().Min(1) })
	b := Lazy(func() Schema[string] { return String().Max(2) })

	type holder struct {
		A string `json:"a"`
		B string `json:"b"`
		D string `json:"d"`
	}

	s := Struct[holder](Shape{"a": a, "b": b, "d": a})
	js := JSONSchema(s)

	defs, _ := js["$defs"].(map[string]any)
	if len(defs) != 2 {
		t.Fatalf("$defs = %v, want 2 distinct entries", defs)
	}

	props := js["properties"].(map[string]any)
	if diff := cmp.Diff(props["a"], props["d"]); diff != "" {
		t.Errorf("the same Lazy used twice must share its entry (-a +d):\n%s", diff)
	}

	if cmp.Equal(props["a"], props["b"]) {
		t.Errorf("two different Lazy schemas share one $ref: %v", props)
	}
}

// Parse must not overflow the stack on input that nests without end: a map
// that contains itself, or a list nested far deeper than any real document.
func TestLazyDepthLimit(t *testing.T) {
	t.Parallel()

	s := newCommentSchema()

	cyclic := map[string]any{"body": "a"}
	cyclic["replies"] = []any{cyclic}

	deep := map[string]any{"body": "leaf"}
	for range 5000 {
		deep = map[string]any{"body": "a", "replies": []any{deep}}
	}

	for name, in := range map[string]any{"cyclic": cyclic, "deep": deep} {
		_, err := s.Parse(in)

		is, ok := AsIssues(err)
		if !ok || len(is) != 1 || is[0].Code != CodeTooDeep {
			t.Errorf("%s: issues = %+v, want one %s issue", name, is, CodeTooDeep)
		}
	}

	// A document of a reasonable depth is not affected.
	ok := map[string]any{"body": "leaf"}
	for range 100 {
		ok = map[string]any{"body": "a", "replies": []any{ok}}
	}

	if _, err := s.Parse(ok); err != nil {
		t.Errorf("Parse(100 levels) = %v", err)
	}
}

// A panic while building the schema must come back on every use, not once.
func TestLazyResolverPanicIsRepeated(t *testing.T) {
	t.Parallel()

	lazy := Lazy(func() Schema[string] { panic("bad shape") })

	for i := range 3 {
		func() {
			defer func() {
				if r := recover(); r != "bad shape" {
					t.Errorf("use %d: recovered %v, want the original panic", i, r)
				}
			}()

			_, _ = lazy.Parse("x")
		}()
	}
}
