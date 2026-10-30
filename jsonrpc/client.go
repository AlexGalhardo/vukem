package jsonrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strconv"
	"sync/atomic"
)

// Client calls a JSON-RPC 2.0 endpoint over HTTP.
//
//	c := jsonrpc.NewClient("https://api.example.com/rpc", &http.Client{Timeout: 10 * time.Second})
//	item, err := jsonrpc.Call[Item](ctx, c, "items.get", GetItem{ID: 7})
//
// A JSON-RPC error answer is returned as an *Error (use errors.AsType).
type Client struct {
	url    string
	http   *http.Client
	nextID atomic.Int64
	// Header is added to every request (Authorization, for example).
	Header http.Header
}

// maxResponseBytes caps a response body the client reads.
const maxResponseBytes = 10 << 20

// NewClient returns a Client posting to url with hc (http.DefaultClient
// when nil).
func NewClient(url string, hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}

	return &Client{url: url, http: hc, Header: http.Header{}}
}

// Call calls method with params (a struct, a map or nil) and decodes the
// result into R.
func Call[R any](ctx context.Context, c *Client, method string, params any) (R, error) {
	var out R

	id := strconv.FormatInt(c.nextID.Add(1), 10)

	body, err := c.post(ctx, method, params, json.RawMessage(id))
	if err != nil {
		return out, err
	}

	var res struct {
		Result json.RawMessage `json:"result"`
		Error  *Error          `json:"error"`
		ID     json.RawMessage `json:"id"`
	}

	if err := json.Unmarshal(body, &res); err != nil {
		return out, fmt.Errorf("jsonrpc: %s: decode response: %w", method, err)
	}

	if res.Error != nil {
		return out, res.Error
	}

	if string(res.ID) != id {
		return out, fmt.Errorf("jsonrpc: %s: response id %s, want %s", method, res.ID, id)
	}

	if err := json.Unmarshal(res.Result, &out); err != nil {
		return out, fmt.Errorf("jsonrpc: %s: decode result: %w", method, err)
	}

	return out, nil
}

// Notify sends method as a notification: the server answers nothing, so
// only transport errors are reported.
func (c *Client) Notify(ctx context.Context, method string, params any) error {
	_, err := c.post(ctx, method, params, nil)
	return err
}

func (c *Client) post(ctx context.Context, method string, params any, id json.RawMessage) ([]byte, error) {
	req := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		req["params"] = params
	}

	if id != nil {
		req["id"] = id
	}

	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("jsonrpc: %s: encode params: %w", method, err)
	}

	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("jsonrpc: %s: %w", method, err)
	}

	maps.Copy(hr.Header, c.Header)

	hr.Header.Set("Content-Type", "application/json")
	hr.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(hr)
	if err != nil {
		return nil, fmt.Errorf("jsonrpc: %s: %w", method, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("jsonrpc: %s: read response: %w", method, err)
	}

	switch {
	case resp.StatusCode == http.StatusNoContent && id == nil:
		return nil, nil
	case resp.StatusCode != http.StatusOK || id == nil && resp.StatusCode != http.StatusNoContent:
		// A JSON-RPC error body (413, parse error) still says why.
		var res struct {
			Error *Error `json:"error"`
		}
		if json.Unmarshal(body, &res) == nil && res.Error != nil {
			return nil, res.Error
		}

		return nil, fmt.Errorf("jsonrpc: %s: HTTP %d", method, resp.StatusCode)
	}

	return body, nil
}
