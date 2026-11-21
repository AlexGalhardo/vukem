package api_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/api"
	"github.com/AlexGalhardo/vukem/middleware"
	"github.com/AlexGalhardo/vukem/openapi"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

type importFile struct {
	Format string      `query:"format" validate:"required,oneof=png jpeg"`
	Scale  int         `query:"scale"  validate:"min=1,max=4"`
	Body   api.RawBody `                                                  json:"-"`
}

type importResult struct {
	Format string `json:"format"`
	Scale  int    `json:"scale"`
	Bytes  int    `json:"bytes"`
}

func rawRouter(op api.Op, called *bool) *router.Router {
	r := router.New(nil)
	api.Register(r, "POST /import", op, func(_ *web.Ctx, in importFile) (importResult, error) {
		if called != nil {
			*called = true
		}

		data, err := io.ReadAll(in.Body)
		if err != nil {
			return importResult{}, err
		}

		return importResult{Format: in.Format, Scale: in.Scale, Bytes: len(data)}, nil
	})

	return r
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

func TestRawBodyReachesTheHandlerUndecoded(t *testing.T) {
	t.Parallel()

	r := rawRouter(api.Op{Status: http.StatusOK}, nil)

	tests := []struct {
		name, target, contentType, body string
		status                          int
		want                            string
	}{
		{
			"binary body and query options",
			"/import?format=png&scale=2",
			"application/octet-stream",
			"\x00\x01\x02\x03",
			200,
			`{"format":"png","scale":2,"bytes":4}`,
		},
		{
			"json media type is not decoded", "/import?format=jpeg&scale=1", "application/json", `{not json`,
			200, `{"format":"jpeg","scale":1,"bytes":9}`,
		},
		{"query is validated", "/import?format=gif&scale=2", "application/octet-stream", "x", 422, `"format"`},
		{"required query is enforced", "/import?scale=2", "application/octet-stream", "x", 422, `"format"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := serveTo(t, r, tt.target, tt.contentType, tt.body, false)
			if rec.Code != tt.status || !strings.Contains(rec.Body.String(), tt.want) {
				t.Fatalf("POST %s = %d %s, want %d containing %s", tt.target, rec.Code, rec.Body, tt.status, tt.want)
			}
		})
	}
}

func serveTo(t *testing.T, h http.Handler, target, contentType, body string, chunked bool) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")

	if chunked {
		req.ContentLength = -1
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func TestRawBodyRefusesAFormAlreadyParsed(t *testing.T) {
	t.Parallel()

	var called bool

	h := middleware.MethodOverride()(rawRouter(api.Op{Status: http.StatusOK}, &called))

	rec := serveTo(t, h, "/import?format=png&scale=1", "application/x-www-form-urlencoded", "a=1", false)
	if rec.Code != http.StatusUnsupportedMediaType || called {
		t.Fatalf("POST = %d (handler called: %v), want 415 and no call", rec.Code, called)
	}

	rec = serveTo(t, h, "/import?format=png&scale=1", "application/octet-stream", "a=1", false)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"bytes":3`) {
		t.Fatalf("POST binary = %d %s, want 200 with 3 bytes", rec.Code, rec.Body)
	}
}

func TestSecurity_RawBodyFailsClosedOverTheLimit(t *testing.T) {
	t.Parallel()

	const target = "/import?format=png&scale=1"

	t.Run("declared length over MaxBody never reaches the handler", func(t *testing.T) {
		t.Parallel()

		var called bool

		rec := serveTo(t, rawRouter(api.Op{MaxBody: 16}, &called), target, "application/octet-stream",
			strings.Repeat("a", 100), false)
		if rec.Code != http.StatusRequestEntityTooLarge || called {
			t.Fatalf("POST = %d (handler called: %v), want 413 and no call", rec.Code, called)
		}
	})

	t.Run("undeclared length over MaxBody fails the read", func(t *testing.T) {
		t.Parallel()

		rec := serveTo(t, rawRouter(api.Op{MaxBody: 16}, nil), target, "application/octet-stream",
			strings.Repeat("a", 100), true)
		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("POST = %d %s, want 413", rec.Code, rec.Body)
		}
	})

	t.Run("a body within MaxBody passes", func(t *testing.T) {
		t.Parallel()

		rec := serveTo(
			t,
			rawRouter(api.Op{MaxBody: 16, Status: http.StatusOK}, nil),
			target,
			"application/octet-stream",
			strings.Repeat("a", 16),
			false,
		)
		if rec.Code != http.StatusOK {
			t.Fatalf("POST = %d %s, want 200", rec.Code, rec.Body)
		}
	})

	t.Run("MaxBody raises the router limit", func(t *testing.T) {
		t.Parallel()

		h := globalLimit(rawRouter(api.Op{MaxBody: 4096, Status: http.StatusOK}, nil), 8)
		if rec := serveTo(
			t,
			h,
			target,
			"application/octet-stream",
			strings.Repeat("a", 1000),
			false,
		); rec.Code != http.StatusOK {
			t.Fatalf("POST = %d %s, want 200", rec.Code, rec.Body)
		}
	})

	t.Run("no limit declared uses the default", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target,
			io.LimitReader(zeroReader{}, web.DefaultMaxBody+10))
		req.Header.Set("Content-Type", "application/octet-stream")
		req.Header.Set("Accept", "application/json")

		rec := httptest.NewRecorder()
		rawRouter(api.Op{}, nil).ServeHTTP(rec, req)

		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("POST = %d %s, want 413", rec.Code, rec.Body)
		}
	})
}

func TestRawBodyPanicsAtBoot(t *testing.T) {
	t.Parallel()

	type noTag struct {
		Body api.RawBody
	}

	type twice struct {
		A api.RawBody `json:"-"`
		B api.RawBody `json:"-"`
	}

	tests := []struct {
		name string
		reg  func()
	}{
		{"GET has no body", func() {
			api.Register(router.New(nil), "GET /x", api.Op{},
				func(_ *web.Ctx, _ importFile) (importResult, error) { return importResult{}, nil })
		}},
		{"missing json dash", func() {
			api.Register(router.New(nil), "POST /x", api.Op{},
				func(_ *web.Ctx, _ noTag) (importResult, error) { return importResult{}, nil })
		}},
		{"two raw bodies", func() {
			api.Register(router.New(nil), "POST /x", api.Op{},
				func(_ *web.Ctx, _ twice) (importResult, error) { return importResult{}, nil })
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				if recover() == nil {
					t.Fatal("Register did not panic")
				}
			}()

			tt.reg()
		})
	}
}

type queryAndBody struct {
	Mode string `query:"mode"`
	Name string `             json:"name" validate:"required"`
}

func TestRawBodyAndQueryReachOpenAPI(t *testing.T) {
	t.Parallel()

	r := router.New(nil)
	api.Register(r, "POST /import", api.Op{Name: "import", MaxBody: 64 << 20},
		func(_ *web.Ctx, _ importFile) (importResult, error) { return importResult{}, nil })
	api.Register(r, "POST /mixed", api.Op{Name: "mixed"},
		func(_ *web.Ctx, _ queryAndBody) (importResult, error) { return importResult{}, nil })

	doc, err := openapi.Build(r.Routes(), openapi.Info{Title: "T", Version: "1"})
	if err != nil {
		t.Fatalf("Build() = %v", err)
	}

	imp := doc.Paths["/import"]["post"]

	body := imp.RequestBody.Content
	if got, want := body["application/octet-stream"].Schema, (openapi.Schema{"type": "string", "format": "binary"}); !cmp.Equal(
		got,
		want,
	) {
		t.Fatalf("raw body schema = %v, want %v", got, want)
	}

	if _, json := body["application/json"]; json {
		t.Fatal("raw route documents a JSON body, want only the binary one")
	}

	var params []string
	for _, p := range imp.Parameters {
		if p.In != "query" {
			t.Fatalf("parameter %s is in %s, want query", p.Name, p.In)
		}

		params = append(params, p.Name)
	}

	if diff := cmp.Diff([]string{"format", "scale"}, params); diff != "" {
		t.Fatalf("query parameters mismatch (-want +got):\n%s", diff)
	}

	mixed := doc.Paths["/mixed"]["post"]

	props, _ := mixed.RequestBody.Content["application/json"].Schema["properties"].(map[string]any)
	if _, ok := props["mode"]; ok || props["name"] == nil {
		t.Fatalf("JSON body properties = %v, want name only (mode is a query parameter)", props)
	}

	if len(mixed.Parameters) != 1 || mixed.Parameters[0].Name != "mode" || mixed.Parameters[0].In != "query" {
		t.Fatalf("mixed parameters = %+v, want the mode query parameter", mixed.Parameters)
	}
}
