package openapi_test

import (
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/api"
	"github.com/AlexGalhardo/vukem/openapi"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

// Named after TypeScript globals: the generated names must not shadow them.
type (
	Number struct {
		Value int `json:"value"`
	}
	Date struct {
		Day string `json:"day"`
	}
	HeadersInit struct {
		Name string `json:"name"`
	}
	RequestInfo struct {
		Path string `json:"path"`
	}
	Response struct {
		Number  Number      `json:"number"`
		Date    *Date       `json:"date"`
		Headers HeadersInit `json:"headers"`
		Request RequestInfo `json:"request"`
	}
)

func reservedRoutes() *router.Router {
	r := router.New(nil)
	api.Register(r, "POST /numbers", api.Op{Name: "numbers.store"},
		func(_ *web.Ctx, in struct {
			N Number `json:"n" validate:"required"`
		},
		) (Response, error) {
			return Response{Number: in.N}, nil
		})

	return r
}

func TestTypeScriptReservedNames(t *testing.T) {
	t.Parallel()

	doc := openapi.Generate(reservedRoutes().Routes(), openapi.Info{})

	zod, err := openapi.TypeScript(doc, true)
	if err != nil {
		t.Fatal(err)
	}

	checkGolden(t, "reserved.zod.ts", zod)

	client, err := openapi.TypeScriptClient(doc)
	if err != nil {
		t.Fatal(err)
	}

	checkGolden(t, "reserved.client.ts", client)
}

// Names that collide once generated.
type (
	NumberType struct {
		V int `json:"v"`
	}
	ItemsStoreBody struct {
		V int `json:"v"`
	}
	ItemsShowParams struct {
		V int `json:"v"`
	}
)

func TestTypeScriptNameCollisions(t *testing.T) {
	t.Parallel()

	type withNumber struct {
		A Number     `json:"a"`
		B NumberType `json:"b"`
	}

	tests := []struct {
		name   string
		client bool
		routes func(r *router.Router)
		want   string
	}{
		{
			name: "suffixed component vs component",
			routes: func(r *router.Router) {
				api.Register(r, "GET /n", api.Op{Name: "n.show"},
					func(*web.Ctx, struct{}) (withNumber, error) { return withNumber{}, nil })
			},
			want: "NumberType",
		},
		{
			name: "operation body vs component",
			routes: func(r *router.Router) {
				api.Register(r, "POST /items", api.Op{Name: "items.store"},
					func(*web.Ctx, struct {
						V int `json:"v"`
					},
					) (ItemsStoreBody, error) {
						return ItemsStoreBody{}, nil
					})
			},
			want: "ItemsStoreBody",
		},
		{
			name:   "client params vs component",
			client: true,
			routes: func(r *router.Router) {
				api.Register(r, "GET /items/{id}", api.Op{Name: "items.show"},
					func(*web.Ctx, struct {
						ID int `json:"id"`
					},
					) (ItemsShowParams, error) {
						return ItemsShowParams{}, nil
					})
			},
			want: "ItemsShowParams",
		},
		{
			name: "two operation ids with one TypeScript name",
			routes: func(r *router.Router) {
				api.Register(r, "GET /a", api.Op{Name: "items.list"},
					func(*web.Ctx, struct{}) (Number, error) { return Number{}, nil })
				api.Register(r, "GET /b", api.Op{Name: "itemsList"},
					func(*web.Ctx, struct{}) (Number, error) { return Number{}, nil })
			},
			want: "ItemsListResponse",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := router.New(nil)
			tt.routes(r)
			doc := openapi.Generate(r.Routes(), openapi.Info{})

			var err error
			if tt.client {
				_, err = openapi.TypeScriptClient(doc)
			} else {
				_, err = openapi.TypeScript(doc, true)
			}

			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("TypeScript() error = %v, want a collision on %s", err, tt.want)
			}
		})
	}
}
