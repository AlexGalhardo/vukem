package orm

import (
	"database/sql"
	"encoding/json"
	"reflect"
	"testing"
	"time"
)

type relUser struct {
	Model

	Name    string        `json:"name"`
	Profile HasOne[Post]  `json:"profile,omitzero"`
	Posts   HasMany[Post] `json:"posts,omitzero"`
}

type relPost struct {
	Model

	UserID int64
	User   BelongsTo[relUser]  `json:"user,omitzero"`
	Tags   ManyToMany[relUser] `json:"tags,omitzero"`
}

func TestRelationValuesAndJSON(t *testing.T) {
	t.Parallel()

	var u relUser
	if u.Posts.Loaded() || u.Profile.Loaded() {
		t.Fatal("zero relations report loaded")
	}

	if b := marshal(t, u); string(
		b,
	) != `{"id":0,"created_at":"0001-01-01T00:00:00Z","updated_at":"0001-01-01T00:00:00Z","name":""}` {
		t.Fatalf("unloaded JSON = %s", b)
	}

	u.Posts.setLoaded(nil)
	u.Profile.setLoaded([]reflect.Value{reflect.ValueOf(Post{Title: "bio"})})

	b := marshal(t, u)

	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}

	if posts, ok := m["posts"].([]any); !ok || len(posts) != 0 || m["profile"].(map[string]any)["Title"] != "bio" {
		t.Fatalf("loaded JSON = %s", b)
	}

	var p relPost
	if p.User.Loaded() || p.Tags.Loaded() {
		t.Fatal("zero relations report loaded")
	}

	p.User.setLoaded(nil)
	p.Tags.setLoaded([]reflect.Value{reflect.ValueOf(relUser{Name: "x"})})

	if got, err := p.User.Get(); err != nil || got != nil {
		t.Fatalf("empty belongs-to = %v %v", got, err)
	}

	if b := marshal(t, p); !json.Valid(b) || string(mustField(t, b, "user")) != "null" ||
		string(
			mustField(t, b, "tags"),
		) != `[{"id":0,"created_at":"0001-01-01T00:00:00Z","updated_at":"0001-01-01T00:00:00Z","name":"x"}]` {
		t.Fatalf("post JSON = %s", b)
	}
}

func mustField(t *testing.T, b []byte, key string) json.RawMessage {
	t.Helper()

	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}

	return m[key]
}

func TestNullTimeJSON(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)

	for _, tt := range []struct {
		in   NullTime
		want string
	}{
		{NullTime{}, "null"},
		{NullTime{sql.Null[time.Time]{V: at, Valid: true}}, `"2026-05-06T07:08:09Z"`},
	} {
		b, err := json.Marshal(tt.in)
		if err != nil || string(b) != tt.want {
			t.Fatalf("Marshal = %s %v", b, err)
		}

		var back NullTime
		if err := json.Unmarshal(b, &back); err != nil || back.Valid != tt.in.Valid || !back.V.Equal(tt.in.V) {
			t.Fatalf("Unmarshal(%s) = %+v %v", b, back, err)
		}

		if back.IsZero() == tt.in.Valid {
			t.Fatal("IsZero")
		}
	}

	var bad NullTime
	if err := json.Unmarshal([]byte(`"yesterday"`), &bad); err == nil {
		t.Fatal("invalid time accepted")
	}
}

func TestKeyNormalization(t *testing.T) {
	t.Parallel()

	var nilPtr *int64

	one := int64(1)

	for _, tt := range []struct{ in, want any }{
		{int8(1), int64(1)}, {uint16(1), int64(1)}, {&one, int64(1)}, {"a", "a"}, {[]byte("b"), "b"}, {2.5, 2.5},
	} {
		if got := normKey(tt.in); got != tt.want {
			t.Errorf("normKey(%v) = %v (%T), want %v", tt.in, got, got, tt.want)
		}
	}

	if _, ok := keyOf(reflect.ValueOf(nilPtr)); ok {
		t.Error("nil pointer key")
	}

	if _, ok := keyOf(reflect.ValueOf("")); ok {
		t.Error("empty string key")
	}

	if normKey(nil) != nil {
		t.Error("nil key")
	}
}

func marshal(t *testing.T, v any) []byte {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	return b
}
