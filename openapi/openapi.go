// Package openapi generates an OpenAPI 3.1 document from the router: typed
// routes (api.Register) are described from their input and output types
// and `validate` tags; plain routes get a generic entry.
//
//	doc := openapi.Generate(r.Routes(), openapi.Info{Title: "Shop", Version: "1.0.0"})
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/api.md.
package openapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/AlexGalhardo/vukem/api"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

// Info configures the document.
type Info struct {
	Title       string
	Version     string
	Description string
	Servers     []string
	// PathPrefix keeps only routes under it, matching whole segments
	// ("/api/v1" keeps /api/v1 and /api/v1/..., not /api/v10); empty keeps
	// all.
	PathPrefix string
	// StripPrefix keeps only routes under it, like PathPrefix, and removes
	// it from their paths, for a server URL that already ends with it:
	// Servers "https://api.example.com/v1" with StripPrefix "/api/v1"
	// documents the route /api/v1/items as /items. Document.ValidateResponse
	// strips it from the request path too.
	StripPrefix string
	// SecuritySchemes adds or replaces schemes; "bearer" (HTTP bearer
	// token) is predefined.
	SecuritySchemes map[string]Schema
	// License is the API's license (info.license); nil leaves it out.
	License *License
	// Security names the schemes every route registered without
	// api.Register requires (each must be predefined or in
	// SecuritySchemes). A plain route with .Doc(openapi.Public{}) requires
	// none. Routes from api.Register keep their api.Op.Security.
	Security []string
	// RequiredOutputs lists in `required` every field of a response
	// schema the server always sends: not a pointer, not omitempty or
	// omitzero. A named type used by both a request and a response is
	// then split into <Name> (response) and <Name>Input (request, required
	// from `validate:"required"` only). Off, one component per type
	// serves both directions and only validate rules mark fields required.
	RequiredOutputs bool
	// TypedOnly makes Build fail with ErrUntypedRoute when a route under
	// PathPrefix was registered without api.Register and is not marked
	// .Doc(Skip{}): such routes are documented with an untyped body and
	// response, which some client generators drop.
	TypedOnly bool
}

// License is the info.license object: Name plus either an SPDX
// Identifier or a URL (OpenAPI 3.1 allows one of them, not both).
type License struct {
	Name       string `json:"name"`
	Identifier string `json:"identifier,omitempty"`
	URL        string `json:"url,omitempty"`
}

// Public marks a plain route as needing no security when Info.Security
// sets a default: r.Post("/login", h).Doc(openapi.Public{}).
type Public struct{}

// Skip hides a route from the document: r.Get(...).Doc(openapi.Skip{}).
type Skip struct{}

// Document is an OpenAPI 3.1 document.
type Document struct {
	OpenAPI    string                           `json:"openapi"`
	Info       docInfo                          `json:"info"`
	Servers    []server                         `json:"servers,omitempty"`
	Paths      map[string]map[string]*Operation `json:"paths"`
	Components Components                       `json:"components"`

	strip string // Info.StripPrefix, applied to request paths in ValidateResponse
}

type docInfo struct {
	Title       string   `json:"title"`
	Version     string   `json:"version"`
	Description string   `json:"description,omitempty"`
	License     *License `json:"license,omitempty"`
}

type server struct {
	URL string `json:"url"`
}

// Components holds reusable schemas, responses and security schemes.
type Components struct {
	Schemas         map[string]Schema   `json:"schemas,omitempty"`
	Responses       map[string]Response `json:"responses,omitempty"`
	SecuritySchemes map[string]Schema   `json:"securitySchemes,omitempty"`
}

// Operation is one method on a path.
type Operation struct {
	OperationID string                `json:"operationId,omitempty"`
	Summary     string                `json:"summary,omitempty"`
	Description string                `json:"description,omitempty"`
	Tags        []string              `json:"tags,omitempty"`
	Deprecated  bool                  `json:"deprecated,omitempty"`
	Parameters  []Parameter           `json:"parameters,omitempty"`
	RequestBody *RequestBody          `json:"requestBody,omitempty"`
	Responses   map[string]Response   `json:"responses"`
	Security    []map[string][]string `json:"security,omitempty"`
}

// MarshalJSON writes a non-nil empty Security as "security": [], the
// OpenAPI way to say an operation needs no authentication.
func (o Operation) MarshalJSON() ([]byte, error) {
	type plain Operation

	if o.Security != nil && len(o.Security) == 0 {
		return json.Marshal(struct {
			plain
			Security []map[string][]string `json:"security"`
		}{plain(o), o.Security})
	}

	return json.Marshal(plain(o))
}

// Parameter is a path or query parameter.
type Parameter struct {
	Name        string `json:"name"`
	In          string `json:"in"`
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
	Schema      Schema `json:"schema"`
	// Example is a single example value, from the field's `example` tag
	// when the generator built this parameter.
	Example any `json:"example,omitempty"`
	// Examples are named alternatives; OpenAPI ignores Example when it is
	// set.
	Examples map[string]Example `json:"examples,omitempty"`
}

// RequestBody is an operation's body.
type RequestBody struct {
	Required bool                 `json:"required"`
	Content  map[string]MediaType `json:"content"`
}

// MediaType wraps a schema for one content type.
type MediaType struct {
	Schema Schema `json:"schema"`
	// Example is a single example value for this content type.
	Example any `json:"example,omitempty"`
	// Examples are named alternatives, so two responses that share one
	// component schema can still show different payloads; OpenAPI ignores
	// Example when it is set.
	Examples map[string]Example `json:"examples,omitempty"`
}

// Example is one named example value (an OpenAPI Example Object).
type Example struct {
	Summary     string `json:"summary,omitempty"`
	Description string `json:"description,omitempty"`
	Value       any    `json:"value,omitempty"`
}

// Response is a response or a $ref to a shared one.
type Response struct {
	Ref         string               `json:"$ref,omitempty"`
	Description string               `json:"description,omitempty"`
	Content     map[string]MediaType `json:"content,omitempty"`
}

var (
	paramRe  = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)(?:\.{3})?\}`)
	problemJ = "application/problem+json"
)

// ErrUnknownSecurityScheme is returned by Build when an operation lists a
// security scheme (api.Op.Security) that is neither predefined ("bearer")
// nor declared in Info.SecuritySchemes.
var ErrUnknownSecurityScheme = errors.New("openapi: unknown security scheme")

// ErrEmptyDocument is returned by Build when Info.PathPrefix is set and
// filters out every route: the resulting document has no paths, which
// looks exactly like a broken app, and the usual cause is a mistyped or
// shell-rewritten prefix. A router with no documentable route at all is
// still an empty document and no error.
var ErrEmptyDocument = errors.New("openapi: path prefix matches no route")

// ErrDuplicateOperationID is returned by Build when two routes in the
// document share an operationId: the route's own name, so groups that
// reuse a local name (api.v1.index and api.v2.index) collide, and the
// generated clients would get clashing methods.
var ErrDuplicateOperationID = errors.New("openapi: duplicate operationId")

// ErrUntypedRoute is returned by Build with Info.TypedOnly when routes
// under the prefix were registered without api.Register; the error names
// them.
var ErrUntypedRoute = errors.New("openapi: untyped routes")

// Generate builds the document for routes (router.Routes()). It panics on
// the errors Build returns (duplicate operationIds, an undeclared security
// scheme, a path prefix matching no route): they are
// programming errors that should surface at boot or in a test.
func Generate(routes []router.RouteInfo, info Info) *Document {
	doc, err := Build(routes, info)
	if err != nil {
		panic(err)
	}

	return doc
}

// Build is Generate returning its error. The operationId of every route is
// its own name, without group name prefixes (Op.Name for a typed route),
// typed or not. A plain POST, PUT
// or PATCH route documents an optional JSON body of any shape, and plain
// routes document a default response of any content, so generated clients
// can send a body and ValidateResponse accepts what they return.
//
//nolint:gocognit // document assembly over routes; steps share state
func Build(routes []router.RouteInfo, info Info) (*Document, error) {
	s := NewSchemas()
	doc := &Document{
		OpenAPI: "3.1.0",
		Info: docInfo{
			Title:       info.Title,
			Version:     info.Version,
			Description: info.Description,
			License:     info.License,
		},
		Paths: map[string]map[string]*Operation{},
		strip: strings.TrimSuffix(info.StripPrefix, "/"),
	}

	if doc.Info.Title == "" {
		doc.Info.Title = "API"
	}

	if l := info.License; l != nil && (l.Name == "" || l.Identifier != "" && l.URL != "") {
		return nil, errors.New("openapi: Info.License needs a Name and at most one of Identifier and URL")
	}

	if doc.Info.Version == "" {
		doc.Info.Version = "1.0.0"
	}

	for _, u := range info.Servers {
		doc.Servers = append(doc.Servers, server{URL: u})
	}

	schemes := map[string]Schema{"bearer": {"type": "http", "scheme": "bearer"}}
	maps.Copy(schemes, info.SecuritySchemes)

	if info.RequiredOutputs {
		s.outputs = outputTypes(routes, info.PathPrefix)
	}

	var untyped []string

	usedSchemes := map[string]bool{}
	idOwner := map[string]string{} // operationId → route that took it
	documentable := 0              // routes the prefix could have kept

	for _, rt := range routes {
		if _, skip := rt.Doc.(Skip); skip || rt.Method == "" || rt.Method == "ANY" {
			continue
		}

		documentable++

		path := oaPath(rt.Path)
		if !underPrefix(path, info.PathPrefix) || !underPrefix(path, doc.strip) {
			continue
		}

		path = cmpOr(strings.TrimPrefix(path, doc.strip), "/")

		var op *Operation
		if typed, ok := rt.Doc.(*api.Operation); ok {
			op = typedOperation(s, typed)
			op.OperationID = cmpOr(rt.LocalName, typed.Name)
			op.Summary = cmpOr(typed.Summary, op.OperationID)
		} else {
			if info.TypedOnly {
				untyped = append(untyped, cmpOr(rt.Name, rt.Method+" "+rt.Path))
			}

			op = plainOperation(s, rt)

			if _, public := rt.Doc.(Public); public {
				op.Security = []map[string][]string{}
			} else {
				for _, name := range info.Security {
					op.Security = append(op.Security, map[string][]string{name: {}})
				}
			}
		}

		if id := op.OperationID; id != "" {
			who := cmpOr(rt.Name, rt.Method+" "+rt.Path)
			if prev, dup := idOwner[id]; dup {
				return nil, fmt.Errorf("%w %q: routes %s and %s "+
					"(rename one, or build one document per group with PathPrefix)",
					ErrDuplicateOperationID, id, prev, who)
			}

			idOwner[id] = who
		}

		for _, req := range op.Security {
			for name := range req {
				usedSchemes[name] = true
			}
		}

		method := strings.ToLower(rt.Method)
		if doc.Paths[path] == nil {
			doc.Paths[path] = map[string]*Operation{}
		}

		doc.Paths[path][method] = op
	}

	if len(untyped) > 0 {
		return nil, fmt.Errorf("%w under %q: %s (register them with api.Register or mark them .Doc(openapi.Skip{}))",
			ErrUntypedRoute, info.PathPrefix, strings.Join(untyped, ", "))
	}

	if prefix := cmpOr(info.PathPrefix, info.StripPrefix); len(doc.Paths) == 0 && documentable > 0 && prefix != "" {
		return nil, fmt.Errorf("%w %q: none of the %d documentable routes is under it "+
			"(check Info.PathPrefix / OPENAPI_PREFIX and Info.StripPrefix / OPENAPI_STRIP_PREFIX)",
			ErrEmptyDocument, prefix, documentable)
	}

	// Only the error responses an operation references: an unused
	// component is a lint error.
	refs := map[string]bool{}

	for _, ops := range doc.Paths {
		for _, op := range ops {
			for _, r := range op.Responses {
				refs[strings.TrimPrefix(r.Ref, "#/components/responses/")] = true
			}
		}
	}

	if refs["Problem"] || refs["ValidationError"] {
		problem := s.For(reflect.TypeFor[web.Problem]())
		doc.Components.Responses = map[string]Response{}

		if refs["Problem"] {
			doc.Components.Responses["Problem"] = Response{
				Description: "Error (RFC 9457 problem details)",
				Content:     map[string]MediaType{problemJ: {Schema: problem}},
			}
		}

		if refs["ValidationError"] {
			doc.Components.Responses["ValidationError"] = Response{
				Description: "Validation failed; errors maps each field to its messages",
				Content:     map[string]MediaType{problemJ: {Schema: problem}},
			}
		}
	}

	doc.Components.Schemas = s.Components()

	for name := range usedSchemes {
		if doc.Components.SecuritySchemes == nil {
			doc.Components.SecuritySchemes = map[string]Schema{}
		}

		sc, ok := schemes[name]
		if !ok {
			return nil, fmt.Errorf("%w %q: declare it in openapi.Info.SecuritySchemes", ErrUnknownSecurityScheme, name)
		}

		doc.Components.SecuritySchemes[name] = sc
	}

	return doc, nil
}

// outputTypes returns the named types the responses of the typed routes
// under prefix reach, nested ones included.
func outputTypes(routes []router.RouteInfo, prefix string) map[reflect.Type]bool {
	pre := NewSchemas()
	pre.outputs = map[reflect.Type]bool{}

	for _, rt := range routes {
		if typed, ok := rt.Doc.(*api.Operation); ok && underPrefix(oaPath(rt.Path), prefix) {
			pre.For(typed.Out)
		}
	}

	out := map[reflect.Type]bool{}
	for k := range pre.names {
		out[k.t] = true
	}

	return out
}

// plainOperation is the generic entry of a route registered without
// api.Register: its input and output types are unknown, so the body and
// the response accept anything.
func plainOperation(s *Schemas, rt router.RouteInfo) *Operation {
	op := &Operation{
		OperationID: rt.LocalName,
		Summary:     rt.LocalName,
		Parameters:  pathParams(s, rt.Path, nil),
		Responses: map[string]Response{
			"default": {
				Description: "Response",
				Content:     map[string]MediaType{"*/*": {Schema: Schema{}}},
			},
			// Every vukem error is problem+json, plain routes included.
			"4XX": {Ref: "#/components/responses/Problem"},
		},
	}

	switch rt.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		op.RequestBody = &RequestBody{Content: map[string]MediaType{"application/json": {Schema: Schema{}}}}
	}

	return op
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}

	return b
}

// oaPath converts ServeMux syntax ({id...}, {$}) to OpenAPI templates.
func oaPath(p string) string {
	p = strings.ReplaceAll(p, "{$}", "")
	p = paramRe.ReplaceAllString(p, "{$1}")

	if p == "" {
		return "/"
	}

	return p
}

func pathParams(s *Schemas, path string, types map[string]Field) []Parameter {
	var out []Parameter

	for _, m := range paramRe.FindAllStringSubmatch(path, -1) {
		p := Parameter{Name: m[1], In: "path", Required: true, Schema: Schema{"type": "string"}}
		if f, ok := types[m[1]]; ok {
			p.Schema = s.of(f.Type, f.Rules)
			p.Description = f.Doc
			p.Example = f.ExampleValue()
		}

		out = append(out, p)
	}

	return out
}

func typedOperation(s *Schemas, t *api.Operation) *Operation {
	op := &Operation{
		OperationID: t.Name,
		Summary:     cmpOr(t.Summary, t.Name),
		Description: t.Description,
		Tags:        t.Tags,
		Deprecated:  t.Deprecated,
		Responses:   map[string]Response{"default": {Ref: "#/components/responses/Problem"}},
	}

	for _, name := range t.Security {
		op.Security = append(op.Security, map[string][]string{name: {}})
	}

	hasInput := t.In.Kind() == reflect.Struct && t.In.NumField() > 0
	fields := map[string]Field{}
	s.input = true

	if hasInput {
		for _, f := range Fields(t.In) {
			fields[f.Key] = f
		}
	}

	op.Parameters = pathParams(s, t.Path, fields)
	inPath := map[string]bool{}

	for _, p := range op.Parameters {
		inPath[p.Name] = true
	}

	bodyless := t.Method == http.MethodGet || t.Method == http.MethodHead || t.Method == http.MethodDelete

	if hasInput {
		if bodyless {
			for _, f := range Fields(t.In) {
				if inPath[f.Key] {
					continue
				}

				op.Parameters = append(op.Parameters, Parameter{
					Name: f.Key, In: "query", Required: f.Required, Description: f.Doc,
					Schema: s.of(f.Type, f.Rules), Example: f.ExampleValue(),
				})
			}
		} else {
			op.RequestBody = &RequestBody{Required: true, Content: map[string]MediaType{
				"application/json": {Schema: s.Object(t.In, inPath)},
			}}
		}

		op.Responses["422"] = Response{Ref: "#/components/responses/ValidationError"}
	}

	s.input = false
	status := t.Status
	noOutput := t.Out.Kind() == reflect.Struct && t.Out.NumField() == 0

	switch {
	case status != 0:
	case noOutput:
		status = http.StatusNoContent
	case t.Method == http.MethodPost:
		status = http.StatusCreated
	default:
		status = http.StatusOK
	}

	resp := Response{Description: http.StatusText(status)}
	if status != http.StatusNoContent && !noOutput {
		resp.Content = map[string]MediaType{"application/json": {Schema: s.For(t.Out)}}
	}

	op.Responses[strconv.Itoa(status)] = resp

	if len(op.Security) > 0 {
		op.Responses["401"] = Response{Ref: "#/components/responses/Problem"}
	}

	slices.SortStableFunc(op.Parameters, func(a, b Parameter) int {
		if a.In != b.In {
			return strings.Compare(b.In, a.In) // path before query
		}

		return 0
	})

	return op
}

func underPrefix(path, prefix string) bool {
	prefix = strings.TrimSuffix(prefix, "/")

	return prefix == "" || path == prefix || strings.HasPrefix(path, prefix+"/")
}
