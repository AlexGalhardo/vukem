package orm

import (
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestFieldsOf(t *testing.T) {
	t.Parallel()

	fields := FieldsOf[User]()

	got := make([]string, 0, len(fields))
	for _, f := range fields {
		got = append(got, f.Column+"="+f.Name)
	}

	want := []string{
		"id=ID", "created_at=CreatedAt", "updated_at=UpdatedAt", "name=Name", "email=Email",
		"age=Age", "active=Active", "settings=Settings",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Fatalf("FieldsOf[User]() (-want +got):\n%s", diff)
	}

	u := User{Name: "Ana"}
	if v := reflect.ValueOf(u).FieldByIndex(fields[3].Index).String(); v != "Ana" {
		t.Errorf("FieldByIndex(name) = %q, want Ana", v)
	}

	if fields[0].Type != reflect.TypeFor[int64]() {
		t.Errorf("FieldsOf[User]()[0].Type = %s, want int64", fields[0].Type)
	}

	if pk := PrimaryKeyOf[User](); pk != "id" {
		t.Errorf("PrimaryKeyOf[User]() = %q, want id", pk)
	}
}

func TestRelationsOf(t *testing.T) {
	t.Parallel()

	if diff := cmp.Diff([]string{"Posts", "Profile"}, RelationsOf[relUser]()); diff != "" {
		t.Errorf("RelationsOf[relUser]() (-want +got):\n%s", diff)
	}

	if got := RelationsOf[User](); len(got) != 0 {
		t.Errorf("RelationsOf[User]() = %v, want none", got)
	}
}
