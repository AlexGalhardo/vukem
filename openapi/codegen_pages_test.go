package openapi_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/openapi"
)

type pageAuthor struct {
	Name string `json:"name"`
}

type pageNode struct {
	Label    string      `json:"label"`
	Children []*pageNode `json:"children"`
}

type pageProject struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	Note      string    `json:"note,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Owner     pageAuthor
	Reviewer  *pageAuthor `json:"reviewer"`
	Tags      []string    `json:"tags"`
	Scores    map[string]int
	Meta      struct {
		Draft bool `json:"draft"`
	} `json:"meta"`
	Secret string `json:"-"`
}

type pageIndexProps struct {
	Projects []pageProject `json:"projects"`
	Owner    pageAuthor    `json:"owner"`
	Tree     pageNode      `json:"tree"`
}

func TestTypeScriptTypesGolden(t *testing.T) {
	t.Parallel()

	got, err := openapi.TypeScriptTypes(map[string]reflect.Type{
		"ProjectsIndexProps": reflect.TypeFor[pageIndexProps](),
		"ProjectShowProps":   reflect.TypeFor[pageProject](),
		"HomeProps":          reflect.TypeFor[struct{}](),
	})
	if err != nil {
		t.Fatal(err)
	}

	checkGolden(t, "pages.types.ts", got)
}

func TestTypeScriptTypesNameCollision(t *testing.T) {
	t.Parallel()

	// A root named like a component declares the same name twice.
	if _, err := openapi.TypeScriptTypes(
		map[string]reflect.Type{"PageAuthor": reflect.TypeFor[pageAuthor]()},
	); err == nil {
		t.Error("a root that collides with a component was accepted")
	}
}
