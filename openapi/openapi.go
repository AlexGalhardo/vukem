// Package openapi generates an OpenAPI 3.1 document from the router: typed
// routes (api.Register) are described from their input and output types
// and `validate` tags; plain routes get a generic entry.
//
//	doc := openapi.Generate(r.Routes(), openapi.Info{Title: "Shop", Version: "1.0.0"})
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/api.md.
package openapi

import (
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
	// SecuritySchemes adds or replaces schemes; "bearer" (HTTP bearer
	// token) is predefined.
	SecuritySchemes map[string]Schema
}

// Skip hides a route from the document: r.Get(...).Doc(openapi.Skip{}).
type Skip struct{}

// Document is an OpenAPI 3.1 document.
type Document struct {
	OpenAPI    string                           `json:"openapi"`
	Info       docInfo                          `json:"info"`
	Servers    []server                         `json:"servers,omitempty"`
	Paths      map[string]map[string]*Operation `json:"paths"`
	Components Components                       `json:"components"`
}

type docInfo struct {
	Title       string `json:"title"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
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

// Parameter is a path or query parameter.
type Parameter struct {
	Name        string `json:"name"`
	In          string `json:"in"`
	Required    bool   `json:"required,omitempty"`
	Description string `json:"description,omitempty"`
	Schema      Schema `json:"schema"`
}

// RequestBody is an operation's body.
type RequestBody struct {
	Required bool                 `json:"required"`
	Content  map[string]MediaType `json:"content"`
}

// MediaType wraps a schema for one content type.
type MediaType struct {
	Schema Schema `json:"schema"`
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

// Generate builds the document for routes (router.Routes()).
func Generate(routes []router.RouteInfo, info Info) *Document {
	s := NewSchemas()
	doc := &Document{
		OpenAPI: "3.1.0",
		Info:    docInfo{Title: info.Title, Version: info.Version, Description: info.Description},
		Paths:   map[string]map[string]*Operation{},
	}

	if doc.Info.Title == "" {
		doc.Info.Title = "API"
	}

	if doc.Info.Version == "" {
		doc.Info.Version = "1.0.0"
	}

	for _, u := range info.Servers {
		doc.Servers = append(doc.Servers, server{URL: u})
	}

	schemes := map[string]Schema{"bearer": {"type": "http", "scheme": "bearer"}}
	maps.Copy(schemes, info.SecuritySchemes)

	usedSchemes := map[string]bool{}

	for _, rt := range routes {
		if _, skip := rt.Doc.(Skip); skip || rt.Method == "" || rt.Method == "ANY" {
			continue
		}

		path := oaPath(rt.Path)
		if !underPrefix(path, info.PathPrefix) {
			continue
		}

		var op *Operation
		if typed, ok := rt.Doc.(*api.Operation); ok {
			op = typedOperation(s, typed)
		} else {
			op = &Operation{
				OperationID: rt.Name,
				Summary:     rt.Name,
				Parameters:  pathParams(s, rt.Path, nil),
				Responses:   map[string]Response{"default": {Description: "Response"}},
			}
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

	problem := s.For(reflect.TypeFor[web.Problem]())
	doc.Components.Responses = map[string]Response{
		"Problem": {
			Description: "Error (RFC 9457 problem details)",
			Content:     map[string]MediaType{problemJ: {Schema: problem}},
		},
		"ValidationError": {
			Description: "Validation failed; errors maps each field to its messages",
			Content:     map[string]MediaType{problemJ: {Schema: problem}},
		},
	}
	doc.Components.Schemas = s.Components()

	for name := range usedSchemes {
		if doc.Components.SecuritySchemes == nil {
			doc.Components.SecuritySchemes = map[string]Schema{}
		}

		sc, ok := schemes[name]
		if !ok {
			sc = Schema{"type": "http", "scheme": "bearer"}
		}

		doc.Components.SecuritySchemes[name] = sc
	}

	return doc
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
		sc := Schema{"type": "string"}
		if f, ok := types[m[1]]; ok {
			sc = s.of(f.Type, f.Rules)
		}

		out = append(out, Parameter{Name: m[1], In: "path", Required: true, Schema: sc})
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
					Name: f.Key, In: "query", Required: f.Required, Description: f.Doc, Schema: s.of(f.Type, f.Rules),
				})
			}
		} else {
			op.RequestBody = &RequestBody{Required: true, Content: map[string]MediaType{
				"application/json": {Schema: s.Object(t.In, inPath)},
			}}
		}

		op.Responses["422"] = Response{Ref: "#/components/responses/ValidationError"}
	}

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
