package admin

import (
	"database/sql"
	"reflect"
	"testing"

	"github.com/AlexGalhardo/vukem/z"
)

type flag struct {
	ID         int64
	Title      string
	Resolution sql.NullString
	Note       sql.Null[string]
	Weight     sql.NullInt64
	Hidden     sql.NullBool
	Seen       sql.NullTime
}

type flagInput struct {
	Title string `form:"title"`
}

func TestNullableColumnsFilterAndSearch(t *testing.T) {
	t.Parallel()

	r := NewResource[flag](z.Struct[flagInput](z.Shape{"title": z.String()})).
		Filter("resolution", "kept", "removed").
		Filter("weight").
		Filter("hidden").
		Search("resolution", "note")
	if err := r.check(); err != nil {
		t.Fatalf("check() = %v, want nullable string, int and bool columns accepted", err)
	}

	bad := NewResource[flag](z.Struct[flagInput](z.Shape{"title": z.String()})).Filter("seen").Search("weight")
	if err := bad.check(); err == nil {
		t.Fatal("check() accepted a nullable time filter and a nullable int search")
	}

	tests := []struct {
		typ  reflect.Type
		in   string
		want any
		ok   bool
	}{
		{reflect.TypeFor[sql.NullString](), "kept", "kept", true},
		{reflect.TypeFor[sql.Null[string]](), "x", "x", true},
		{reflect.TypeFor[sql.NullInt64](), "7", int64(7), true},
		{reflect.TypeFor[sql.NullInt64](), "seven", nil, false},
		{reflect.TypeFor[sql.NullBool](), "true", true, true},
		{reflect.TypeFor[sql.NullTime](), "2026-01-01", nil, false},
	}
	for _, tt := range tests {
		got, ok := filterValue(tt.typ, tt.in)
		if ok != tt.ok || (ok && got != tt.want) {
			t.Errorf("filterValue(%s, %q) = %v, %v, want %v, %v", tt.typ, tt.in, got, ok, tt.want, tt.ok)
		}
	}
}
