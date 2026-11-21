// Package mcp serves the Model Context Protocol (https://modelcontextprotocol.io),
// revision 2026-07-28, from any Vukem app: tools, resources, resource
// templates, prompts, completion, pagination, progress, cancellation,
// subscriptions and the input_required confirmation flow, over stdio and
// Streamable HTTP.
//
//	srv := mcp.NewServer(mcp.Config{Name: "billing", Version: "1.0.0"})
//	mcp.AddTool(srv, mcp.Tool{Name: "get_invoice", Description: "Read one invoice."},
//		func(ctx context.Context, in GetInvoice) (Invoice, error) {
//			return invoices.Find(ctx, in.ID)
//		})
//
//	h, err := srv.HTTPHandler(mcp.HTTPConfig{
//		AllowedOrigins: []string{"https://app.example.com"},
//		Resource:       "https://api.example.com/mcp",
//		Verifier:       verifier,
//	})
//	r.Handle("/mcp", h)
//	r.Handle("GET "+h.MetadataPath(), h.MetadataHandler())
//
// Tool inputs and outputs are Go structs: the JSON Schemas come from the
// same `json`, `validate`, `enum` and `doc` tags as OpenAPI, and the
// arguments are parsed with a strict z schema before the function runs.
//
// The server is stateless, as the revision requires: there is no session,
// no initialize handshake and no server-to-client request. Clients that
// speak the older revisions (2025-11-25, 2025-06-18, 2025-03-26 and
// 2024-11-05) are answered per request with that revision's shape, without
// ever keeping state between requests.
//
// Not implemented: the deprecated logging, roots and sampling features, the
// tasks extension, x-mcp-header parameter mirroring and JSON-RPC batches.
package mcp
