package jsonrpc

import (
	"maps"
	"slices"

	"github.com/AlexGalhardo/vukem/openapi"
)

// OpenRPCVersion is the OpenRPC specification version OpenRPC documents.
const OpenRPCVersion = "1.3.2"

// OpenRPC returns the OpenRPC document (https://spec.open-rpc.org) of the
// registered methods, ready for encoding/json: one content descriptor per
// field of the params struct (in order, required from `validate:"required"`),
// the result schema, and named structs under components.schemas, with the
// same schemas the OpenAPI export produces. Serve it from a route, or write
// it to a file for client generators:
//
//	r.Get("/rpc/openrpc.json", func(c *web.Ctx) error {
//		return c.JSON(http.StatusOK, rpc.OpenRPC("Items", "1.0.0"))
//	})
func (s *Server) OpenRPC(title, version string) map[string]any {
	schemas := openapi.NewSchemas()
	methods := make([]any, 0, len(s.methods))

	for _, name := range slices.Sorted(maps.Keys(s.methods)) {
		m := s.methods[name]
		obj := schemas.Object(m.params, nil)
		props, _ := obj["properties"].(map[string]any)
		required, _ := obj["required"].([]string)

		params := []any{}
		for _, key := range fieldNames(m.params) {
			params = append(params, map[string]any{
				"name": key, "schema": props[key], "required": slices.Contains(required, key),
			})
		}

		methods = append(methods, map[string]any{
			"name":           name,
			"params":         params,
			"paramStructure": "either",
			"result":         map[string]any{"name": "result", "schema": schemas.For(m.result)},
		})
	}

	doc := map[string]any{
		"openrpc": OpenRPCVersion,
		"info":    map[string]any{"title": title, "version": version},
		"methods": methods,
	}

	if c := schemas.Components(); len(c) > 0 {
		doc["components"] = map[string]any{"schemas": c}
	}

	return doc
}
