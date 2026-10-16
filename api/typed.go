package api

import (
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"regexp"
	"strings"

	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
	"github.com/AlexGalhardo/vukem/z"
)

// Op describes a typed operation for the router and the OpenAPI document.
type Op struct {
	// Name is the route name (and the OpenAPI operationId).
	Name        string
	Summary     string
	Description string
	Tags        []string
	// Status is the success status; default 201 for POST, 204 when the
	// output type is struct{}, 200 otherwise.
	Status     int
	Deprecated bool
	// Security lists the security schemes required (e.g. "bearer").
	Security []string
}

// Operation is attached to every typed route (router.RouteInfo.Doc) so
// generators know its input and output types.
type Operation struct {
	Op
	Method string
	Path   string
	Params []string
	In     reflect.Type
	Out    reflect.Type
}

// Registrar is a router.Router or router.Group.
type Registrar interface {
	Match(methods []string, path string, h web.HandlerFunc) []*router.Route
}

var paramRe = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)(?:\.{3})?\}`) //nolint:gochecknoglobals // compiled once

// Register adds a typed handler. The input is built from the query string,
// the JSON or form body and the path parameters (which win), and validated
// with In's `validate` tags (422 with field errors). The output is written
// as JSON. GET, HEAD and DELETE ignore the body. Pattern is "METHOD /path".
//
//	api.Register(g, "POST /products", api.Op{Name: "products.store"},
//		func(c *web.Ctx, in StoreProduct) (ProductJSON, error) { ... })
func Register[In, Out any](g Registrar, pattern string, op Op, fn func(*web.Ctx, In) (Out, error)) *router.Route {
	method, path, ok := strings.Cut(pattern, " ")
	if !ok || method == "" || !strings.HasPrefix(path, "/") {
		panic(fmt.Sprintf("api: Register pattern %q must be \"METHOD /path\"", pattern))
	}

	method = strings.ToUpper(method)

	var params []string // filled from the full path (group prefixes too) below

	inType, outType := reflect.TypeFor[In](), reflect.TypeFor[Out]()
	noInput := inType.Kind() == reflect.Struct && inType.NumField() == 0
	noOutput := outType.Kind() == reflect.Struct && outType.NumField() == 0

	var schema z.Schema[In]
	if !noInput {
		schema = z.For[In]() // panics at boot on bad tags
	}

	// GET, HEAD and DELETE read the query string only.
	bodyless := method == http.MethodGet || method == http.MethodHead || method == http.MethodDelete

	status := op.Status
	switch {
	case status != 0:
	case noOutput:
		status = http.StatusNoContent
	case method == http.MethodPost:
		status = http.StatusCreated
	default:
		status = http.StatusOK
	}

	h := func(c *web.Ctx) error {
		var in In

		if !noInput {
			input := c.QueryInput()
			if bodyless {
				input = maps.Clone(input)
			} else {
				var err error
				if input, err = c.Input(); err != nil {
					return err
				}
			}
			for _, p := range params {
				input[p] = c.Param(p)
			}

			var err error
			if in, err = web.ParseInput(schema, input); err != nil {
				return err
			}
		}

		out, err := fn(c, in)
		if err != nil {
			return err
		}

		if status == http.StatusNoContent {
			return c.NoContent()
		}

		return c.JSON(status, out)
	}

	rt := g.Match([]string{method}, path, h)[0]
	if op.Name != "" {
		rt.Name(op.Name)
	}

	for _, m := range paramRe.FindAllStringSubmatch(rt.Path(), -1) {
		params = append(params, m[1])
	}

	return rt.Doc(&Operation{Op: op, Method: method, Path: rt.Path(), Params: params, In: inType, Out: outType})
}
