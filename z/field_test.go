package z

import "testing"

func TestStructField(t *testing.T) {
	t.Parallel()

	type in struct {
		Title string `form:"title"`
		Body  string
	}

	s := Struct[in](Shape{"title": String(), "body": String()})

	for key, want := range map[string]string{"title": "Title", "body": "Body"} {
		f, ok := s.Field(key)
		if !ok || f.Name != want {
			t.Errorf("Field(%q) = %q, %v, want %q", key, f.Name, ok, want)
		}
	}

	if _, ok := s.Field("missing"); ok {
		t.Error("Field(missing) found a field")
	}
}
