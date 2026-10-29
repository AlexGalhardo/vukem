package jsonrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/errs"
)

type subtractParams struct {
	Minuend    int `json:"minuend"`
	Subtrahend int `json:"subtrahend"`
}

type userKey struct{}

func newServer(notified *atomic.Int64) *Server {
	s := NewServer(Config{MaxBatch: 5, MaxBodyBytes: 4096})
	Method(s, "subtract", func(_ context.Context, p subtractParams) (int, error) {
		return p.Minuend - p.Subtrahend, nil
	})
	Method(s, "update", func(_ context.Context, _ struct {
		A int `json:"a"`
	},
	) (any, error) {
		notified.Add(1)
		return nil, nil
	})
	Method(s, "greet", func(_ context.Context, p struct {
		Name string `json:"name" validate:"required,max=10"`
	},
	) (string, error) {
		return "hi " + p.Name, nil
	})
	Method(s, "fail", func(_ context.Context, p struct {
		Kind string `json:"kind"`
	},
	) (any, error) {
		switch p.Kind {
		case "notfound":
			return nil, errs.New(http.StatusNotFound, "item not found")
		case "custom":
			return nil, &Error{Code: 42, Message: "custom", Data: "x"}
		case "panic":
			panic("secret internals")
		default:
			return nil, errors.New("db password=hunter2 refused")
		}
	})
	Method(s, "admin.purge", func(_ context.Context, _ struct{}) (bool, error) {
		notified.Add(1)
		return true, nil
	}, func(ctx context.Context) error {
		if ctx.Value(userKey{}) != "admin" {
			return errs.ErrForbidden
		}

		return nil
	})

	return s
}

func post(t *testing.T, h http.Handler, body string) (int, string) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/rpc", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec.Code, strings.TrimSpace(rec.Body.String())
}

func TestSpecConformance(t *testing.T) {
	t.Parallel()

	var notified atomic.Int64

	s := newServer(&notified)

	for _, tc := range []struct {
		name, in, want string
		status         int
	}{
		{
			"positional", `{"jsonrpc":"2.0","method":"subtract","params":[42,23],"id":1}`,
			`{"jsonrpc":"2.0","result":19,"id":1}`, 200,
		},
		{
			"named", `{"jsonrpc":"2.0","method":"subtract","params":{"subtrahend":23,"minuend":42},"id":"a"}`,
			`{"jsonrpc":"2.0","result":19,"id":"a"}`, 200,
		},
		{"null id", `{"jsonrpc":"2.0","method":"subtract","params":[1,1],"id":null}`, `{"jsonrpc":"2.0","result":0,"id":null}`, 200},
		{"null result", `{"jsonrpc":"2.0","method":"update","params":{"a":1},"id":2}`, `{"jsonrpc":"2.0","result":null,"id":2}`, 200},
		{"notification", `{"jsonrpc":"2.0","method":"update","params":[1]}`, ``, 204},
		{"unknown method notification", `{"jsonrpc":"2.0","method":"nope"}`, ``, 204},
		{
			"method not found", `{"jsonrpc":"2.0","method":"foobar","id":"1"}`,
			`{"jsonrpc":"2.0","error":{"code":-32601,"message":"Method not found"},"id":"1"}`, 200,
		},
		{
			"parse error", `{"jsonrpc":"2.0","method":"foobar,"params":"bar","baz]`,
			`{"jsonrpc":"2.0","error":{"code":-32700,"message":"Parse error"},"id":null}`, 200,
		},
		{
			"invalid request", `{"jsonrpc":"2.0","method":1,"params":"bar"}`,
			`{"jsonrpc":"2.0","error":{"code":-32600,"message":"Invalid Request"},"id":null}`, 200,
		},
		{
			"wrong version", `{"jsonrpc":"1.0","method":"subtract","id":3}`,
			`{"jsonrpc":"2.0","error":{"code":-32600,"message":"Invalid Request"},"id":3}`, 200,
		},
		{
			"object id", `{"jsonrpc":"2.0","method":"subtract","id":{}}`,
			`{"jsonrpc":"2.0","error":{"code":-32600,"message":"Invalid Request"},"id":null}`, 200,
		},
		{
			"unknown member", `{"jsonrpc":"2.0","method":"subtract","id":4,"extra":1}`,
			`{"jsonrpc":"2.0","error":{"code":-32600,"message":"Invalid Request"},"id":null}`, 200,
		},
		{
			"unknown param", `{"jsonrpc":"2.0","method":"subtract","params":{"minuend":1,"x":2},"id":5}`,
			`{"jsonrpc":"2.0","error":{"code":-32602,"message":"Invalid params","data":{"fields":{"x":["The x field is not allowed."]}}},"id":5}`, 200,
		},
		{
			"too many positional", `{"jsonrpc":"2.0","method":"subtract","params":[1,2,3],"id":6}`,
			`{"jsonrpc":"2.0","error":{"code":-32602,"message":"Invalid params"},"id":6}`, 200,
		},
		{
			"validation", `{"jsonrpc":"2.0","method":"greet","params":{},"id":7}`,
			`{"jsonrpc":"2.0","error":{"code":-32602,"message":"Invalid params","data":{"fields":{"name":["The name field is required."]}}},"id":7}`, 200,
		},
		{"valid", `{"jsonrpc":"2.0","method":"greet","params":{"name":"Ana"},"id":8}`, `{"jsonrpc":"2.0","result":"hi Ana","id":8}`, 200},
		{
			"http error", `{"jsonrpc":"2.0","method":"fail","params":{"kind":"notfound"},"id":9}`,
			`{"jsonrpc":"2.0","error":{"code":-32004,"message":"item not found","data":{"status":404}},"id":9}`, 200,
		},
		{
			"custom error", `{"jsonrpc":"2.0","method":"fail","params":{"kind":"custom"},"id":10}`,
			`{"jsonrpc":"2.0","error":{"code":42,"message":"custom","data":"x"},"id":10}`, 200,
		},
		{
			"empty batch", `[]`,
			`{"jsonrpc":"2.0","error":{"code":-32600,"message":"Invalid Request"},"id":null}`, 200,
		},
		{
			"invalid batch", `[1,2]`,
			`[{"jsonrpc":"2.0","error":{"code":-32600,"message":"Invalid Request"},"id":null},` +
				`{"jsonrpc":"2.0","error":{"code":-32600,"message":"Invalid Request"},"id":null}]`, 200,
		},
		{
			"batch", `[{"jsonrpc":"2.0","method":"subtract","params":[3,1],"id":"1"},` +
				`{"jsonrpc":"2.0","method":"update","params":[7]},` +
				`{"foo":"boo"},` +
				`{"jsonrpc":"2.0","method":"foo.get","params":{"name":"myself"},"id":"5"}]`,
			`[{"jsonrpc":"2.0","result":2,"id":"1"},` +
				`{"jsonrpc":"2.0","error":{"code":-32600,"message":"Invalid Request"},"id":null},` +
				`{"jsonrpc":"2.0","error":{"code":-32601,"message":"Method not found"},"id":"5"}]`, 200,
		},
		{"notification batch", `[{"jsonrpc":"2.0","method":"update","params":[1]},{"jsonrpc":"2.0","method":"update","params":[2]}]`, ``, 204},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			status, got := post(t, s, tc.in)
			if status != tc.status {
				t.Errorf("status = %d, want %d", status, tc.status)
			}

			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("body (-want +got):\n%s", diff)
			}
		})
	}
}

func TestHTTPRules(t *testing.T) {
	t.Parallel()

	s := newServer(new(atomic.Int64))

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/rpc", nil))

	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "POST" {
		t.Errorf("GET = %d %q", rec.Code, rec.Header().Get("Allow"))
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/rpc", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "text/plain")

	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("text/plain = %d", rec.Code)
	}

	status, body := post(
		t,
		s,
		`{"jsonrpc":"2.0","method":"greet","params":{"name":"`+strings.Repeat("a", 5000)+`"},"id":1}`,
	)
	if status != http.StatusRequestEntityTooLarge || !strings.Contains(body, "-32600") {
		t.Errorf("oversized = %d %s", status, body)
	}
}

func TestSecurity_A04_JSONRPCBatchLimit(t *testing.T) {
	t.Parallel()

	var notified atomic.Int64

	s := newServer(&notified)
	batch := "[" + strings.Repeat(`{"jsonrpc":"2.0","method":"update","params":[1]},`, 6)
	batch = strings.TrimSuffix(batch, ",") + "]"

	_, body := post(t, s, batch)
	if !strings.Contains(body, "Batch too large (max 5)") {
		t.Errorf("body = %s", body)
	}

	if n := notified.Load(); n != 0 {
		t.Errorf("an oversized batch ran %d calls, want none", n)
	}
}

func TestSecurity_A04_JSONRPCDeepNesting(t *testing.T) {
	t.Parallel()

	s := NewServer(Config{})
	Method(s, "echo", func(_ context.Context, p struct {
		V any `json:"v"`
	},
	) (any, error) {
		return p.V, nil
	})

	deep := strings.Repeat("[", 20000) + strings.Repeat("]", 20000)

	status, body := post(t, s, `{"jsonrpc":"2.0","method":"echo","params":{"v":`+deep+`},"id":1}`)
	if status != http.StatusOK || !strings.Contains(body, `"code":-32700`) {
		t.Errorf("deep nesting = %d %.200s", status, body)
	}
}

func TestSecurity_A05_JSONRPCInternalErrorsHidden(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer

	s := NewServer(Config{Logger: slogTo(&logs)})
	Method(s, "fail", func(_ context.Context, p struct {
		Panic bool `json:"panic"`
	},
	) (any, error) {
		if p.Panic {
			panic("secret internals")
		}

		return nil, errors.New("db password=hunter2 refused")
	})

	for _, in := range []string{`{"panic":true}`, `{}`} {
		_, body := post(t, s, `{"jsonrpc":"2.0","method":"fail","params":`+in+`,"id":1}`)

		want := `{"jsonrpc":"2.0","error":{"code":-32603,"message":"Internal error"},"id":1}`
		if body != want {
			t.Errorf("%s: body = %s", in, body)
		}
	}

	if !strings.Contains(logs.String(), "secret internals") || !strings.Contains(logs.String(), "hunter2") {
		t.Errorf("internal errors not logged: %s", logs.String())
	}
}

func TestSecurity_A01_JSONRPCGuards(t *testing.T) {
	t.Parallel()

	var ran atomic.Int64

	s := newServer(&ran)

	_, body := post(t, s, `{"jsonrpc":"2.0","method":"admin.purge","id":1}`)
	if body != `{"jsonrpc":"2.0","error":{"code":-32003,"message":"Forbidden","data":{"status":403}},"id":1}` {
		t.Errorf("guarded call = %s", body)
	}

	// A notification gets no answer, but the guard still stops it.
	if status, _ := post(t, s, `{"jsonrpc":"2.0","method":"admin.purge"}`); status != http.StatusNoContent {
		t.Errorf("notification status = %d", status)
	}

	if n := ran.Load(); n != 0 {
		t.Errorf("guarded method ran %d times", n)
	}

	admin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey{}, "admin")))
	})

	if _, body := post(
		t,
		admin,
		`{"jsonrpc":"2.0","method":"admin.purge","id":1}`,
	); body != `{"jsonrpc":"2.0","result":true,"id":1}` {
		t.Errorf("admin call = %s", body)
	}
}

func TestSecurity_A01_JSONRPCNoEnumerationBehindAuth(t *testing.T) {
	t.Parallel()

	// Router-level auth answers before the server looks a method up, so
	// known and unknown methods are indistinguishable without credentials.
	s := newServer(new(atomic.Int64))
	authed := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		s.ServeHTTP(w, r)
	})

	s1, b1 := post(t, authed, `{"jsonrpc":"2.0","method":"subtract","params":[1,1],"id":1}`)
	s2, b2 := post(t, authed, `{"jsonrpc":"2.0","method":"nope","id":1}`)

	if s1 != http.StatusUnauthorized || s1 != s2 || b1 != b2 {
		t.Errorf("known = %d %s, unknown = %d %s", s1, b1, s2, b2)
	}
}

func TestMethodPanicsOnMisuse(t *testing.T) {
	t.Parallel()

	for name, register := range map[string]func(s *Server){
		"reserved": func(s *Server) {
			Method(s, "rpc.discover", func(context.Context, struct{}) (int, error) { return 0, nil })
		},
		"empty": func(s *Server) {
			Method(s, "", func(context.Context, struct{}) (int, error) { return 0, nil })
		},
		"duplicate": func(s *Server) {
			Method(s, "a", func(context.Context, struct{}) (int, error) { return 0, nil })
			Method(s, "a", func(context.Context, struct{}) (int, error) { return 0, nil })
		},
		"non-struct": func(s *Server) {
			Method(s, "b", func(context.Context, int) (int, error) { return 0, nil })
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				if recover() == nil {
					t.Error("no panic")
				}
			}()

			register(NewServer(Config{}))
		})
	}
}

func FuzzServer(f *testing.F) {
	for _, seed := range []string{
		`{"jsonrpc":"2.0","method":"subtract","params":[42,23],"id":1}`,
		`[{"jsonrpc":"2.0","method":"update","params":[1]},{"id":null}]`,
		`{"jsonrpc":"2.0","method":"greet","params":{"name":"x"},"id":"a"}`,
		`[]`, `{`, `null`,
	} {
		f.Add(seed)
	}

	s := newServer(new(atomic.Int64))

	f.Fuzz(func(t *testing.T, in string) {
		status, body := post(t, s, in)

		switch status {
		case http.StatusOK:
			if !json.Valid([]byte(body)) {
				t.Fatalf("invalid JSON response %q for %q", body, in)
			}
		case http.StatusNoContent, http.StatusRequestEntityTooLarge:
		default:
			t.Fatalf("status %d for %q", status, in)
		}
	})
}

func slogTo(w *bytes.Buffer) *slog.Logger { return slog.New(slog.NewTextHandler(w, nil)) }
