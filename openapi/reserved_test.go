package openapi_test

import (
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
