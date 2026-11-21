package mcp_test

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"testing"

	"github.com/AlexGalhardo/vukem/mcp"
)

// A confirmation answered through one OAuth client must not be usable by
// another client of the same user.
func TestSecurity_A01_MCPRequestStateBoundToClient(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, mcp.Config{})
	ran := 0

	mcp.AddTool(s, mcp.Tool{Name: "delete"}, func(ctx context.Context, in confirmIn) (echoOut, error) {
		ok, err := mcp.Confirm(ctx, "Delete "+in.ID+"?")
		if err != nil {
			return echoOut{}, err
		}

		if ok {
			ran++
		}

		return echoOut{Echo: in.ID}, nil
	})

	h, err := s.HTTPHandler(mcp.HTTPConfig{
		Verifier: &verifier{tokens: map[string]*mcp.TokenInfo{
			"a": {Subject: "u1", ClientID: "app-a", Resource: resourceURL},
			"b": {Subject: "u1", ClientID: "app-b", Resource: resourceURL},
		}},
		Resource: resourceURL, AuthorizationServers: []string{"https://auth.example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}

	send := func(token string, retry map[string]any) *struct {
		Result struct {
			ResultType   string `json:"resultType"`
			RequestState string `json:"requestState"`
		} `json:"result"`
		Error *mcp.Error `json:"error"`
	} {
		t.Helper()

		params := map[string]any{
			"name":      "delete",
			"arguments": map[string]any{"id": "1"},
			"_meta": map[string]any{
				"io.modelcontextprotocol/protocolVersion":    "2026-07-28",
				"io.modelcontextprotocol/clientCapabilities": map[string]any{"elicitation": map[string]any{}},
			},
		}
		maps.Copy(params, retry)

		body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call", "params": params})
		if err != nil {
			t.Fatal(err)
		}

		hdr := modernHeaders("tools/call", "delete")
		hdr["Authorization"] = "Bearer " + token

		rec := do(h, req{body: string(body), headers: hdr})
		if rec.Code != http.StatusOK && rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
		}

		out := &struct {
			Result struct {
				ResultType   string `json:"resultType"`
				RequestState string `json:"requestState"`
			} `json:"result"`
			Error *mcp.Error `json:"error"`
		}{}
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("body %q: %v", rec.Body.String(), err)
		}

		return out
	}

	first := send("a", nil)
	if first.Result.ResultType != "input_required" || first.Result.RequestState == "" {
		t.Fatalf("first call = %+v, want input_required", first)
	}

	retry := map[string]any{
		"requestState":   first.Result.RequestState,
		"inputResponses": map[string]any{"confirm": map[string]any{"action": "accept"}},
	}

	if got := send("b", retry); got.Error == nil || ran != 0 {
		t.Fatalf("another client reused the confirmation: %+v, ran = %d", got, ran)
	}

	if got := send("a", retry); got.Error != nil || ran != 1 {
		t.Fatalf("the same client was refused: %+v, ran = %d", got, ran)
	}
}
