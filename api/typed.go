package api

import (
	"fmt"
	"maps"
	"mime"
	"net/http"
	"reflect"
	"regexp"
	"strings"

	"github.com/AlexGalhardo/vukem/errs"
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
	// MaxBody caps the request body of this route at that many bytes,
	// replacing the router-wide limit in both directions: a route that
	// takes a file raises it, one that takes a tiny command lowers it. Zero
	// keeps the router's limit. An over-limit body answers 413. The OpenAPI
	// document shows it as x-max-body-bytes. Register panics on a negative
	// value.
	MaxBody int64
	// StrictBody refuses a JSON body key that In does not declare, at any
	// depth (nested structs, lists of structs), with the usual 422 whose
	// issue path names the key ("items.1.nmae"). Query parameters and path
	// parameters stay free, and form bodies are not checked. Without it an
	// unknown key is silently dropped. It has no effect on GET, HEAD and
	// DELETE, which have no body. Keys are matched against In's field names
	// and json, form and query tags, also under RegisterWith.
	StrictBody bool
	// Produces lists the media types of a success response the handler
	// writes itself (c.Blob, c.Stream), such as "text/calendar" for an .ics
	// file. It is documentation: the OpenAPI document lists each type under
	// the success response, as a string (a text/* type) or a binary string,
	// and the generated clients ask for them in Accept and hand back the body
	// undecoded. A route whose Out is struct{} is then documented as 200 (201
	// for POST) instead of 204. Register panics on a type that is not a valid
	// media type.
	Produces []string
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
	// Schema is the input schema given to RegisterWith, nil for Register
	// (whose schema comes from In's tags, which generators already read).
	Schema z.AnySchema
}

// RouteSummary returns the operation summary; it makes Operation a
// router.Summarizer, so route:list can print it.
func (o *Operation) RouteSummary() string { return o.Summary }

// Registrar is a router.Router or router.Group.
type Registrar interface {
	Match(methods []string, path string, h web.HandlerFunc) []*router.Route
}

var paramRe = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)(?:\.{3})?\}`) //nolint:gochecknoglobals // compiled once

// Register adds a typed handler. The input is built from the query string,
// the JSON or form body and the path parameters (which win), and validated
// with In's `validate` tags (422 with field errors). The output is written
// as JSON, checked first when Op.ValidateResponse is set. GET, HEAD and
// DELETE ignore the body. A field of type [RawBody] receives the unread body
// instead (a file upload), and `query:"name"` tags bind and document query
// parameters. Pattern is "METHOD /path". It panics on a
// malformed pattern or when Op.ValidateResponse is set for a non-struct
// Out, so mistakes surface at boot.
//
//	api.Register(g, "POST /products", api.Op{Name: "products.store"},
//		func(c *web.Ctx, in StoreProduct) (ProductJSON, error) { ... })
//
// A handler that writes the response itself (a 304 from c.NotModified, a
// redirect) returns the zero Out and a nil error; Register then writes
// nothing more:
//
//	if c.NotModified(etag, item.UpdatedAt) {
//		return ItemJSON{}, nil
//	}
//
// In is the HTTP input type, kept apart from the domain type: a json.RawMessage
// field makes z.For panic at boot (Parse receives decoded JSON, so the
// original bytes are gone). Declare the field as map[string]any or any and
// convert to the domain type in the handler.
//
// Once the handler has touched the response writer (c.JSON, c.Writer().WriteHeader,
// a redirect) the response is the handler's: Register writes no body and
// ignores Out, so a handler that picks the status per request writes it
// itself and returns the zero Out:
//
//	if in.DryRun {
//		return ProjectJSON{}, c.JSON(http.StatusOK, preview)
//	}
//
// Op.Status is the only status Register writes. Op.MaxBody sets the body
// limit of the route before the handler runs.
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
// The OpenAPI document describes schema: its constraint keywords replace the
// tag-derived ones on the top-level inputs (path and query parameters, body
// properties) and required follows it. Nested structs keep the $ref derived
// from their tags, and a z.Refine or z.Transform has no JSON Schema form, so
// the document cannot show it.
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
func register[In, Out any](
	who string,
	g Registrar,
	pattern string,
	op Op,
	schema z.Schema[In],
	fn func(*web.Ctx, In) (Out, error),
) *router.Route {
	var given z.AnySchema
	if schema != nil {
		given = schema
	}

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

	for _, mt := range op.Produces {
		if _, _, err := mime.ParseMediaType(mt); err != nil || !strings.Contains(mt, "/") {
			panic(fmt.Sprintf("api: %s %q: Produces has an invalid media type %q", who, pattern, mt))
		}
	}

	if op.MaxBody < 0 {
		panic(fmt.Sprintf("api: %s %q: MaxBody must not be negative, got %d", who, pattern, op.MaxBody))
	}

	// GET, HEAD and DELETE read the query string only.
	bodyless := method == http.MethodGet || method == http.MethodHead || method == http.MethodDelete

	rawIndex, hasRaw := rawBodyField(who, pattern, inType)
	if hasRaw && bodyless {
		panic(fmt.Sprintf("api: %s %q: a RawBody needs a method with a body, not %s", who, pattern, method))
	}

	status := responseStatus(op, method, noOutput)

	h := func(c *web.Ctx) error {
		in, err := bindRequest(c, schema, op, inType, bodyless, rawIndex, params)
		if err != nil {
			return err
		}

		out, err := fn(c, in)
		if err != nil {
			return err
		}

		return respond(c, status, noOutput, outSchema, out)
	}

	rt := g.Match([]string{method}, path, h)[0]
	if op.Name != "" {
		rt.Name(op.Name)
	}

	for _, m := range paramRe.FindAllStringSubmatch(rt.Path(), -1) {
		params = append(params, m[1])
	}

	return rt.Doc(&Operation{
		Op: op, Method: method, Path: rt.Path(), Params: params, In: inType, Out: outType, Schema: given,
	})
}

// formParsedPastLimit reports an urlencoded body that middleware (MethodOverride)
// already parsed under the router-wide limit: parsing is done, so the lower
// limit would never be read against. A declared length over limit is refused,
// and so is an undeclared one (chunked), whose size is unknown: fail closed.
// A JSON request also has a non-nil PostForm after ParseForm, so the media
// type decides.
func formParsedPastLimit(r *http.Request, limit int64) bool {
	return formParsed(r) && (r.ContentLength > limit || r.ContentLength < 0)
}

// formParsed reports an urlencoded body that middleware already consumed.
func formParsed(r *http.Request) bool {
	if r.PostForm == nil {
		return false
	}

	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))

	return ct == "application/x-www-form-urlencoded"
}

// bindRequest applies the route's body limit, then binds and validates In;
// a non-nil rawIndex (the RawBody field) gets the unread body instead of a
// decoded one.
func bindRequest[In any](
	c *web.Ctx,
	schema z.Schema[In],
	op Op,
	inType reflect.Type,
	bodyless bool,
	rawIndex []int,
	params []string,
) (In, error) {
	var in In

	if op.MaxBody > 0 {
		c.SetRequest(web.LimitBody(c.Writer(), c.Request(), op.MaxBody))

		if formParsedPastLimit(c.Request(), op.MaxBody) {
			return in, errs.ErrRequestTooLarge
		}
	}

	if schema == nil {
		return in, nil
	}

	in, err := bindInput(c, schema, inType, bodyless || rawIndex != nil, op.StrictBody, params)
	if err != nil {
		return in, err
	}

	if rawIndex != nil {
		body, err := openRawBody(c)
		if err != nil {
			return in, err
		}

		reflect.ValueOf(&in).Elem().FieldByIndex(rawIndex).Set(reflect.ValueOf(&body).Elem())
	}

	return in, nil
}

func responseStatus(op Op, method string, noOutput bool) int {
	switch {
	case op.Status != 0:
		return op.Status
	case noOutput:
		return http.StatusNoContent
	case method == http.MethodPost:
		return http.StatusCreated
	default:
		return http.StatusOK
	}
}

// bindInput merges the query string, the body (unless bodyless) and the path
// parameters, then validates the result against schema. With strict, a JSON
// body key that in does not declare is refused before the merge hides which
// map it came from.
func bindInput[In any](
	c *web.Ctx,
	schema z.Schema[In],
	inType reflect.Type,
	bodyless, strict bool,
	params []string,
) (In, error) {
	var in In

	input := c.QueryInput()

	if !bodyless {
		body, isJSON, err := c.BodyInput()
		if err != nil {
			return in, err
		}

		if strict && isJSON {
			if err := web.IssuesError(c, z.Unrecognized(inType, body), "body"); err != nil {
				return in, err
			}
		}

		maps.Copy(input, body)
	}

	for _, p := range params {
		input[p] = c.Param(p)
	}

	return web.ParseMap(c, schema, input)
}

func respond[Out any](c *web.Ctx, status int, noOutput bool, outSchema z.Schema[Out], out Out) error {
	// The handler finished the response itself (c.NotModified, a redirect):
	// Out is ignored, neither validated nor written.
	if c.Writer().Written() {
		return nil
	}

	if status == http.StatusNoContent {
		return c.NoContent()
	}

	// No output type means no body, whatever the status (202 Accepted).
	if noOutput {
		c.Writer().WriteHeader(status)
		return nil
	}

	if outSchema != nil {
		return web.JSONWith(c, status, outSchema, out)
	}

	return c.JSON(status, out)
}
