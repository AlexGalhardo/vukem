package mcp

import "encoding/json"

// Revision constants.
const (
	// Version is the protocol revision this package implements.
	Version = "2026-07-28"

	metaVersion      = "io.modelcontextprotocol/protocolVersion"
	metaCapabilities = "io.modelcontextprotocol/clientCapabilities"
	metaClientInfo   = "io.modelcontextprotocol/clientInfo"
	metaServerInfo   = "io.modelcontextprotocol/serverInfo"
	metaSubscription = "io.modelcontextprotocol/subscriptionId"
)

// legacyVersions are the revisions that start with an initialize handshake.
var legacyVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

// JSON-RPC and MCP error codes.
const (
	CodeParse                      = -32700
	CodeInvalidRequest             = -32600
	CodeMethodNotFound             = -32601
	CodeInvalidParams              = -32602
	CodeInternal                   = -32603
	CodeHeaderMismatch             = -32020
	CodeMissingClientCapability    = -32021
	CodeUnsupportedProtocolVersion = -32022
	CodeRateLimited                = -32023
	CodeForbidden                  = -32030
)

// Error is a JSON-RPC error object.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Error implements error.
func (e *Error) Error() string { return e.Message }

// message is one inbound JSON-RPC message.
type message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}

// response is one outbound JSON-RPC response.
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type notification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// Implementation names and versions a server.
type Implementation struct {
	Name    string `json:"name"`
	Title   string `json:"title,omitempty"`
	Version string `json:"version"`
}

// Icon is a visual identifier for a tool, prompt, resource or server.
type Icon struct {
	Src      string   `json:"src"`
	MIMEType string   `json:"mimeType,omitempty"`
	Sizes    []string `json:"sizes,omitempty"`
}

// Annotations are optional hints on content and resources.
type Annotations struct {
	Audience     []string `json:"audience,omitempty"`
	Priority     *float64 `json:"priority,omitempty"`
	LastModified string   `json:"lastModified,omitempty"`
}

// Content is one content block of a tool result or prompt message.
type Content struct {
	Type        string            `json:"type"`
	Text        string            `json:"text,omitempty"`
	Data        string            `json:"data,omitempty"`
	MIMEType    string            `json:"mimeType,omitempty"`
	URI         string            `json:"uri,omitempty"`
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Resource    *ResourceContents `json:"resource,omitempty"`
	Annotations *Annotations      `json:"annotations,omitempty"`
}

// Text returns a text content block.
func Text(s string) Content { return Content{Type: "text", Text: s} }

// ResourceLink returns a resource_link content block.
func ResourceLink(uri, name, mimeType string) Content {
	return Content{Type: "resource_link", URI: uri, Name: name, MIMEType: mimeType}
}

// ResourceContents is the text or binary body of a resource.
type ResourceContents struct {
	URI      string `json:"uri"`
	MIMEType string `json:"mimeType,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     string `json:"blob,omitempty"` // base64
}

// requestMeta is the per-request metadata every modern request carries.
type requestMeta struct {
	Version       string          `json:"io.modelcontextprotocol/protocolVersion"`
	Capabilities  json.RawMessage `json:"io.modelcontextprotocol/clientCapabilities"`
	ClientInfo    *Implementation `json:"io.modelcontextprotocol/clientInfo"`
	ProgressToken json.RawMessage `json:"progressToken"`
}

// ClientCapabilities is the subset of client capabilities the server reads.
type ClientCapabilities struct {
	Elicitation map[string]any `json:"elicitation,omitempty"`
}
