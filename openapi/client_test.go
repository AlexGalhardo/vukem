package openapi_test

import (
	"bytes"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/api"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/openapi"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

func checkGolden(t *testing.T, file, got string) {
	t.Helper()

	golden := filepath.Join("testdata", file)
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(string(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))), got); diff != "" {
		t.Errorf("%s (-want +got):\n%s", file, diff)
	}
}

func TestTypeScriptClientGolden(t *testing.T) {
	t.Parallel()

	got, err := openapi.TypeScriptClient(openapi.Generate(routes(), openapi.Info{PathPrefix: "/api"}))
	if err != nil {
		t.Fatal(err)
	}

	checkGolden(t, "shop.client.ts", got)
}

func TestGoClientGolden(t *testing.T) {
	t.Parallel()

	got, err := openapi.GoClient(
		openapi.Generate(routes(), openapi.Info{Title: "Shop", PathPrefix: "/api"}),
		"shopclient",
	)
	if err != nil {
		t.Fatal(err)
	}

	checkGolden(t, "shop.client.go", string(got))
}

func TestGoClientRejectsBadPackage(t *testing.T) {
	t.Parallel()

	if _, err := openapi.GoClient(openapi.Generate(routes(), openapi.Info{}), "api-client"); err == nil {
		t.Fatal("GoClient accepted an invalid package name")
	}
}

type rtShow struct {
	ID   int64    `json:"id"   validate:"required"`
	Q    string   `json:"q"    validate:"max=20"`
	Tags []string `json:"tags"`
}

type rtStore struct {
	Name   string  `json:"name"   validate:"required,min=2"`
	Price  float64 `json:"price"  validate:"min=0"`
	Status string  `json:"status" validate:"oneof=draft published"`
}

type rtItem struct {
	ID     int64    `json:"id"`
	Name   string   `json:"name"`
	Q      string   `json:"q,omitempty"`
	Tags   []string `json:"tags,omitempty"`
	Status string   `json:"status,omitempty"`
}

func roundTripServer(t *testing.T) (*httptest.Server, *openapi.Document) {
	t.Helper()

	r := router.New(nil)
	r.Group("/api", func(g *router.Group) {
		api.Register(g, "GET /items/{id}", api.Op{Name: "items.show"}, func(_ *web.Ctx, in rtShow) (rtItem, error) {
			if in.ID == 404 {
				return rtItem{}, errs.ErrNotFound
			}

			return rtItem{ID: in.ID, Name: "item", Q: in.Q, Tags: in.Tags}, nil
		})
		api.Register(g, "POST /items", api.Op{Name: "items.store"}, func(_ *web.Ctx, in rtStore) (rtItem, error) {
			return rtItem{ID: 1, Name: in.Name, Status: in.Status}, nil
		})
		api.Register(g, "DELETE /items/{id}", api.Op{Name: "items.destroy"},
			func(*web.Ctx, struct{}) (struct{}, error) { return struct{}{}, nil })
	})

	h, err := r.Handler()
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	return srv, openapi.Generate(r.Routes(), openapi.Info{Title: "Items", PathPrefix: "/api"})
}

// roundTripTest exercises the generated client against the live server.
const roundTripTest = `package itemsclient_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"

	"example.com/roundtrip"
)

func TestRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := itemsclient.New(os.Getenv("VUKEM_API_URL"))
	q := "a b&c"

	item, err := c.ItemsShow(ctx, 7, itemsclient.ItemsShowQuery{Q: &q, Tags: []string{"x", "y/z"}})
	if err != nil {
		t.Fatalf("ItemsShow: %v", err)
	}
	if item.ID == nil || *item.ID != 7 || item.Q == nil || *item.Q != q || !slices.Equal(item.Tags, []string{"x", "y/z"}) {
		t.Fatalf("ItemsShow = %+v", item)
	}

	created, err := c.ItemsStore(ctx, itemsclient.ItemsStoreBody{Name: "lamp", Status: new(itemsclient.ItemsStoreBodyStatusPublished)})
	if err != nil {
		t.Fatalf("ItemsStore: %v", err)
	}
	if created.Name == nil || *created.Name != "lamp" || created.Status == nil || *created.Status != "published" {
		t.Fatalf("ItemsStore = %+v", created)
	}

	_, err = c.ItemsStore(ctx, itemsclient.ItemsStoreBody{Name: "x"})
	apiErr, ok := errors.AsType[*itemsclient.Error](err)
	if !ok || apiErr.Status != 422 || len(apiErr.Problem.Errors["name"]) == 0 {
		t.Fatalf("ItemsStore invalid = %#v", err)
	}

	_, err = c.ItemsShow(ctx, 404, itemsclient.ItemsShowQuery{})
	if apiErr, ok := errors.AsType[*itemsclient.Error](err); !ok || apiErr.Status != 404 || apiErr.Problem.Title == "" {
		t.Fatalf("ItemsShow missing = %#v", err)
	}

	if err := c.ItemsDestroy(ctx, "7"); err != nil {
		t.Fatalf("ItemsDestroy: %v", err)
	}
}
`

func TestGoClientRoundTrip(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("compiles Go programs")
	}

	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go not on PATH")
	}

	srv, doc := roundTripServer(t)

	src, err := openapi.GoClient(doc, "itemsclient")
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	files := map[string]string{
		"go.mod":         "module example.com/roundtrip\n\ngo 1.26\n",
		"client.go":      string(src),
		"client_test.go": roundTripTest,
	}

	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	for _, args := range [][]string{{"vet", "./..."}, {"test", "-count=1", "./..."}} {
		cmd := exec.CommandContext(t.Context(), gobin, args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "VUKEM_API_URL="+srv.URL, "GOWORK=off", "GOFLAGS=-mod=mod", "GOTOOLCHAIN=local")

		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s: %v\n%s\n--- client.go ---\n%s", strings.Join(args, " "), err, out, src)
		}
	}
}
