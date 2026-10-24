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
	// Security lists the security schemes the OpenAPI document declares
	// (e.g. "bearer"). It is documentation only and does not authenticate
	// the route: add auth.Required (and auth.Ability) to the route or group.
	Security []string
	// ValidateResponse checks the output against its `validate` tags
	// (z.For[Out]) before writing it, when web.Services.CheckResponses is
	// on: the app sets it whenever APP_ENV is not production, so a staging
	// deployment with APP_ENV=development or test runs the check too. A
	// mismatch is a server bug: it is logged and answered with a generic 500
	// instead of sending data that breaks the documented contract. Out must
	// be a struct; Register panics otherwise.
	ValidateResponse bool
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
// as JSON, checked first when Op.ValidateResponse is set. GET, HEAD and
// DELETE ignore the body. Pattern is "METHOD /path". It panics on a
// malformed pattern or when Op.ValidateResponse is set for a non-struct
// Out, so mistakes surface at boot.
//
//	api.Register(g, "POST /products", api.Op{Name: "products.store"},
//		func(c *web.Ctx, in StoreProduct) (ProductJSON, error) { ... })
//
// Use [RegisterWith] when the input needs a rule `validate` tags cannot
// express.
func Register[In, Out any](g Registrar, pattern string, op Op, fn func(*web.Ctx, In) (Out, error)) *router.Route {
	return register("Register", g, pattern, op, nil, fn)
}

// RegisterWith adds a typed handler that validates its input with schema
// instead of In's `validate` tags, for the rules tags cannot express: a
// bound computed from a value (z.Slice(z.String()).Max(len(allowed))) or a
// cross-field z.Refine. Everything else is [Register]: the same binding
// order (query string, body, path parameters, which win), the same 422 with
// field errors, the same output handling. It panics on a nil schema, so the
// mistake surfaces at boot.
//
// The OpenAPI document is generated from In and Out (their `json`,
// `validate` and `doc` tags), so schema never reaches it: the document
// describes exactly what [Register] would have documented. Keep the static
// rules in the tags and let schema add only what they cannot say, or the
// document under-describes what the route enforces.
//
//	schema := z.Struct[CreateKey](z.Shape{
//		"name":      z.String().Min(1).Max(20),
//		"abilities": z.Slice(z.String()).Min(1).Max(len(abilities.All())),
//	}).Refine(checkKey)
//	api.RegisterWith(g, "POST /keys", api.Op{Name: "keys.store"}, schema,
//		func(c *web.Ctx, in CreateKey) (KeyJSON, error) { ... })
func RegisterWith[In, Out any](
	g Registrar,
	pattern string,
	op Op,
	schema z.Schema[In],
	fn func(*web.Ctx, In) (Out, error),
) *router.Route {
	if schema == nil {
		panic(fmt.Sprintf("api: RegisterWith %q needs a schema; Register derives one from In's tags", pattern))
	}

	return register("RegisterWith", g, pattern, op, schema, fn)
}

// register backs Register and RegisterWith: a nil schema is derived from
// In's tags, and stays nil when In has no fields (no input to bind). who
// names the caller, so a boot panic points at the function the app called.
//
//nolint:gocognit // boot-time wiring of one route; steps depend on shared locals
func register[In, Out any](
	who string,
	g Registrar,
	pattern string,
	op Op,
	schema z.Schema[In],
	fn func(*web.Ctx, In) (Out, error),
) *router.Route {
	method, path, ok := strings.Cut(pattern, " ")
	if !ok || method == "" || !strings.HasPrefix(path, "/") {
		panic(fmt.Sprintf("api: %s pattern %q must be \"METHOD /path\"", who, pattern))
	}

	method = strings.ToUpper(method)

	var params []string // filled from the full path (group prefixes too) below

	inType, outType := reflect.TypeFor[In](), reflect.TypeFor[Out]()
	noInput := inType.Kind() == reflect.Struct && inType.NumField() == 0
	noOutput := outType.Kind() == reflect.Struct && outType.NumField() == 0

	if schema == nil && !noInput {
		schema = z.For[In]() // panics at boot on bad tags
	}

	var outSchema z.Schema[Out]
	if op.ValidateResponse && !noOutput {
		if outType.Kind() != reflect.Struct {
			panic(fmt.Sprintf("api: %s %q: ValidateResponse needs a struct output, got %s", who, pattern, outType))
		}

		outSchema = z.For[Out]() // panics at boot on bad tags
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

		if schema != nil {
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
			if in, err = web.ParseMap(c, schema, input); err != nil {
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

		if outSchema != nil {
			return web.JSONWith(c, status, outSchema, out)
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
