package web

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/AlexGalhardo/golang-framework/errs"
	"github.com/AlexGalhardo/golang-framework/z"
)

type storePost struct {
	Title string   `json:"title" form:"title"`
	Tags  []string `json:"tags"  form:"tags"`
	Draft bool     `json:"draft" form:"draft"`
}

var storePostSchema = z.Struct[storePost](z.Shape{
	"title": z.String().Trim().Min(3),
	"tags":  z.Slice(z.String().Alphanum()).Max(2).Optional(),
	"draft": z.Bool().Default(false),
})

type loginInput struct {
	Email string `json:"email" validate:"required,email"`
}

func runHandler(t *testing.T, r *http.Request, s *Services, h HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()

	if s == nil {
		s = &Services{}
	}
	s.Log = slog.New(slog.DiscardHandler)

	rec := httptest.NewRecorder()
	NewCtx(rec, r, s).Serve(h)

	return rec
}

func TestParseJSONSuccessAndProblem(t *testing.T) {
	t.Parallel()

	handler := func(c *Ctx) error {
		in, err := Parse(c, storePostSchema)
		if err != nil {
			return err
		}
		return c.JSON(http.StatusCreated, in)
	}

	r := httptest.NewRequestWithContext(
		t.Context(),
		"POST",
		"/",
		strings.NewReader(`{"title":"  Hello ","tags":["go"]}`),
	)
	r.Header.Set("Content-Type", "application/json")
	rec := runHandler(t, r, nil, handler)
	if rec.Code != 201 || rec.Body.String() != `{"title":"Hello","tags":["go"],"draft":false}` {
		t.Fatalf("success = %d %s", rec.Code, rec.Body.String())
	}

	r = httptest.NewRequestWithContext(
		t.Context(),
		"POST",
		"/",
		strings.NewReader(`{"title":"x","tags":["a","b","c!"]}`),
	)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")
	rec = runHandler(t, r, nil, handler)

	var p Problem
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if rec.Code != 422 || len(p.Errors["title"]) != 1 || len(p.Errors["tags"]) != 1 || len(p.Errors["tags.2"]) != 1 {
		t.Fatalf("422 problem = %d %s", rec.Code, rec.Body.String())
	}
}

func TestParseFormHookAndHTMX(t *testing.T) {
	t.Parallel()

	form := url.Values{"title": {"x"}, "draft": {"on"}}.Encode()
	handler := func(c *Ctx) error { _, err := Parse(c, storePostSchema); return err }

	// Classic form post: the validation hook (installed by sessions) redirects back.
	r := httptest.NewRequestWithContext(t.Context(), "POST", "/posts", strings.NewReader(form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Referer", "/posts/create")

	var flashed *errs.ValidationError
	r = r.WithContext(WithValidationHook(r.Context(), func(c *Ctx, v *errs.ValidationError) bool {
		flashed = v
		_ = c.Back("/")
		return true
	}))

	rec := runHandler(t, r, nil, handler)
	if rec.Code != 303 || rec.Header().Get("Location") != "/posts/create" || flashed.First("title") == "" {
		t.Fatalf("form = %d %q %v", rec.Code, rec.Header().Get("Location"), flashed)
	}

	// htmx: 422 fragment.
	r = httptest.NewRequestWithContext(t.Context(), "POST", "/posts", strings.NewReader(form))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("HX-Request", "true")
	rec = runHandler(t, r, nil, handler)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), `data-field="title"`) {
		t.Fatalf("htmx = %d %s", rec.Code, rec.Body.String())
	}
}

func TestBindFromTags(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(
		t.Context(),
		"POST",
		"/",
		strings.NewReader(url.Values{"email": {"a@b.co"}}.Encode()),
	)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var got loginInput
	rec := runHandler(t, r, nil, func(c *Ctx) error {
		var err error
		got, err = Bind[loginInput](c)
		return err
	})
	if rec.Code != 200 || got.Email != "a@b.co" {
		t.Fatalf("Bind = %d %+v", rec.Code, got)
	}

	r = httptest.NewRequestWithContext(t.Context(), "POST", "/", strings.NewReader(`{"email":`))
	r.Header.Set("Content-Type", "application/json")
	rec = runHandler(t, r, nil, func(c *Ctx) error { _, err := Bind[loginInput](c); return err })
	if rec.Code != 400 {
		t.Fatalf("malformed body = %d", rec.Code)
	}
}

func TestParseQueryAndParams(t *testing.T) {
	t.Parallel()

	type filters struct {
		Page int    `json:"page"`
		Sort string `json:"sort"`
	}
	qs := z.Struct[filters](z.Shape{"page": z.Int().Min(1).Default(1), "sort": z.Enum("new", "old").Default("new")})

	r := httptest.NewRequestWithContext(t.Context(), "GET", "/?page=3&sort=old", http.NoBody)
	c, _ := newCtx(t, r, nil)
	if f, err := ParseQuery(c, qs); err != nil || f.Page != 3 || f.Sort != "old" {
		t.Fatalf("ParseQuery = %+v %v", f, err)
	}

	r = httptest.NewRequestWithContext(t.Context(), "GET", "/?page=0", http.NoBody)
	c, _ = newCtx(t, r, nil)
	if _, err := ParseQuery(c, qs); errs.Status(err) != 422 {
		t.Fatalf("ParseQuery invalid = %v", err)
	}

	type ids struct {
		ID int64 `json:"id"`
	}
	ps := z.Struct[ids](z.Shape{"id": z.Int64().Positive()})

	r = httptest.NewRequestWithContext(t.Context(), "GET", "/", http.NoBody)
	r.SetPathValue("id", "42")
	c, _ = newCtx(t, r, nil)
	if v, err := ParseParams(c, ps, "id"); err != nil || v.ID != 42 {
		t.Fatalf("ParseParams = %+v %v", v, err)
	}

	r.SetPathValue("id", "abc")
	if _, err := ParseParams(c, ps, "id"); !errors.Is(err, errs.ErrNotFound) {
		t.Fatalf("ParseParams invalid = %v, want 404", err)
	}
}

func TestJSONWithChecksOutput(t *testing.T) {
	t.Parallel()

	type out struct {
		Email string `json:"email"`
	}
	schema := z.Struct[out](z.Shape{"email": z.String().Email()})

	r := httptest.NewRequestWithContext(t.Context(), "GET", "/", http.NoBody)
	r.Header.Set("Accept", "application/json")

	rec := runHandler(t, r, &Services{CheckResponses: true}, func(c *Ctx) error {
		return JSONWith(c, 200, schema, out{Email: "not-an-email"})
	})
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "not-an-email") {
		t.Fatalf("contract violation = %d %s", rec.Code, rec.Body.String())
	}

	rec = runHandler(t, r, &Services{CheckResponses: true}, func(c *Ctx) error {
		return JSONWith(c, 200, schema, out{Email: "a@b.co"})
	})
	if rec.Code != 200 {
		t.Fatalf("valid output = %d", rec.Code)
	}

	rec = runHandler(t, r, &Services{}, func(c *Ctx) error {
		return JSONWith(c, 200, schema, out{Email: "unchecked"})
	})
	if rec.Code != 200 {
		t.Fatalf("checks disabled = %d", rec.Code)
	}
}
