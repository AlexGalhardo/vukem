package mcp_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/mcp"
	"github.com/AlexGalhardo/vukem/mcp/mcptest"
)

type noArgs struct{}

// VF-111: outside a tool call, a *ToolError or an *errs.HTTPError is a JSON-RPC
// error, never a tool-shaped result (which resources/read and prompts/get do
// not define). A tool call keeps its result.
func TestNonToolFailuresAreJSONRPCErrors(t *testing.T) {
	t.Parallel()

	secret := "pq: password=hunter2"

	tests := []struct {
		name    string
		err     error
		code    int
		message string
		data    string
	}{
		{"tool error", mcp.ToolErrorf("the budget is closed"), mcp.CodeInvalidParams, "the budget is closed", ""},
		{"wrapped tool error", fmt.Errorf("read: %w", mcp.ToolErrorf("closed")), mcp.CodeInvalidParams, "closed", ""},
		{"400", &errs.HTTPError{Status: http.StatusBadRequest, Message: "bad id"}, mcp.CodeInvalidParams, "bad id", ""},
		{
			"422",
			&errs.HTTPError{Status: http.StatusUnprocessableEntity},
			mcp.CodeInvalidParams,
			"Unprocessable Entity",
			"",
		},
		{
			"404",
			&errs.HTTPError{Status: http.StatusNotFound, Message: "no such " + secret},
			mcp.CodeInvalidParams,
			"Not found",
			"",
		},
		{"401", &errs.HTTPError{Status: http.StatusUnauthorized}, mcp.CodeForbidden, "Unauthorized", ""},
		{
			"403",
			&errs.HTTPError{Status: http.StatusForbidden, Message: "not yours"},
			mcp.CodeForbidden,
			"not yours",
			"",
		},
		{
			"429 with Retry-After",
			(&errs.HTTPError{Status: http.StatusTooManyRequests, Message: "usage limit reached"}).WithHeader(
				"Retry-After",
				"30",
			),
			mcp.CodeRateLimited,
			"usage limit reached",
			"30",
		},
		{
			"429 without a wait",
			&errs.HTTPError{Status: http.StatusTooManyRequests},
			mcp.CodeRateLimited,
			"Too Many Requests",
			"",
		},
		{
			"wrapped 4xx",
			fmt.Errorf("read: %w", &errs.HTTPError{Status: http.StatusForbidden}),
			mcp.CodeForbidden,
			"Forbidden",
			"",
		},
		{
			"5xx stays internal and hides its cause",
			&errs.HTTPError{Status: http.StatusBadGateway, Message: secret, Err: errors.New(secret)},
			mcp.CodeInternal, "Internal error", "",
		},
		{"plain error stays internal", errors.New(secret), mcp.CodeInternal, "Internal error", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s, _ := newServer(t, mcp.Config{})
			fail := tc.err

			mcp.AddResource(s, mcp.Resource{URI: "app://r", Name: "r"},
				func(context.Context) ([]mcp.ResourceContents, error) { return nil, fail })
			mcp.AddResourceTemplate(
				s,
				mcp.ResourceTemplate{URITemplate: "app://t/{id}", Name: "t"},
				func(context.Context, string, map[string]string) ([]mcp.ResourceContents, error) { return nil, fail },
				nil,
			)
			mcp.AddPrompt(s, mcp.Prompt{Name: "p"},
				func(context.Context, noArgs) (mcp.PromptResult, error) { return mcp.PromptResult{}, fail }, nil)

			c := mcptest.NewClient(t, s)

			for method, params := range map[string]map[string]any{
				"resources/read (resource)": {"uri": "app://r"},
				"resources/read (template)": {"uri": "app://t/1"},
				"prompts/get":               {"name": "p"},
			} {
				rpc := strings.Fields(method)[0]
				rep := c.Call(rpc, params)

				if rep.Error == nil || rep.Error.Code != tc.code || rep.Error.Message != tc.message {
					t.Fatalf(
						"%s = %+v (result %s), want error %d %q",
						method,
						rep.Error,
						rep.Result,
						tc.code,
						tc.message,
					)
				}

				if len(rep.Result) > 0 && string(rep.Result) != "null" {
					t.Errorf("%s carries a result next to the error: %s", method, rep.Result)
				}

				if strings.Contains(fmt.Sprint(rep.Error.Data), "hunter2") ||
					strings.Contains(rep.Error.Message, "hunter2") {
					t.Errorf("%s leaked a cause: %+v", method, rep.Error)
				}

				if tc.data != "" {
					if data, _ := rep.Error.Data.(map[string]any); data["retryAfter"] != tc.data {
						t.Errorf("%s data = %v, want retryAfter %s", method, rep.Error.Data, tc.data)
					}
				}
			}
		})
	}
}

// A tool call keeps the tool-shaped result for the same errors.
func TestToolCallsKeepToolShapedErrors(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, mcp.Config{})
	mcp.AddTool(s, mcp.Tool{Name: "soft"}, func(context.Context, echoIn) (echoOut, error) {
		return echoOut{}, mcp.ToolErrorf("the invoice is closed")
	})
	mcp.AddTool(s, mcp.Tool{Name: "http"}, func(context.Context, echoIn) (echoOut, error) {
		return echoOut{}, &errs.HTTPError{Status: http.StatusTooManyRequests, Message: "slow down"}
	})

	c := mcptest.NewClient(t, s)

	for name, want := range map[string]string{"soft": "the invoice is closed", "http": "slow down"} {
		if res := c.CallTool(name, map[string]any{"text": "x"}); !res.IsError || res.Text() != want {
			t.Errorf("%s = %+v, want a tool error result %q", name, res, want)
		}
	}
}
