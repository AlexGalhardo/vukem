package jsonrpc

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestClient(t *testing.T) {
	t.Parallel()

	var notified atomic.Int64

	srv := httptest.NewServer(newServer(&notified))
	t.Cleanup(srv.Close)

	c := NewClient(srv.URL, srv.Client())

	got, err := Call[int](t.Context(), c, "subtract", subtractParams{Minuend: 42, Subtrahend: 23})
	if err != nil || got != 19 {
		t.Fatalf("Call(subtract) = %d, %v, want 19", got, err)
	}

	if got, err := Call[int](t.Context(), c, "subtract", []int{5, 2}); err != nil || got != 3 {
		t.Errorf("Call(subtract, positional) = %d, %v, want 3", got, err)
	}

	_, err = Call[any](t.Context(), c, "fail", map[string]string{"kind": "notfound"})
	if e, ok := errors.AsType[*Error](err); !ok || e.Code != -32004 || e.Message != "item not found" {
		t.Errorf("Call(fail) err = %v, want -32004", err)
	}

	if _, err := Call[any](t.Context(), c, "nope", nil); err == nil {
		t.Error("Call(nope) err = nil")
	}

	if err := c.Notify(t.Context(), "update", []int{1}); err != nil {
		t.Fatal(err)
	}

	// The handler ran before the 204 was written.
	if n := notified.Load(); n != 1 {
		t.Errorf("notifications run = %d, want 1", n)
	}

	if err := c.Notify(t.Context(), "", nil); err == nil {
		t.Error("Notify with an invalid request err = nil")
	}
}

func TestOpenRPCGolden(t *testing.T) {
	t.Parallel()

	doc, err := json.MarshalIndent(newServer(new(atomic.Int64)).OpenRPC("Test", "1.0.0"), "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	const path = "testdata/openrpc.golden.json"
	if *update {
		if err := os.WriteFile(path, doc, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(doc, want) {
		t.Errorf("OpenRPC differs from %s (go test -run %s -update):\n%s", path, t.Name(), doc)
	}
}

var _ http.Handler = (*Server)(nil)
