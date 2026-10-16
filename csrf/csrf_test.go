package csrf

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/web"
)

type fakeSession struct{ data map[string]any }

func (f *fakeSession) Get(k string) any    { return f.data[k] }
func (f *fakeSession) Put(k string, v any) { f.data[k] = v }
func (f *fakeSession) Forget(...string)    {}
func (f *fakeSession) Pull(string) any     { return nil }
func (f *fakeSession) Flash(string, any)   {}
func (f *fakeSession) Regenerate()         {}
func (f *fakeSession) Invalidate()         {}
func (f *fakeSession) ID() string          { return "id" }

func setup(t *testing.T, cfg Config, sess web.Session) (http.Handler, *bytes.Buffer) {
	t.Helper()

	mw, err := Middleware(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	s := &web.Services{Log: slog.New(slog.NewTextHandler(&logs, nil))}

	h := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := web.WithServices(r.Context(), s)
		if sess != nil {
			ctx = web.WithSession(ctx, sess)
		}
		h.ServeHTTP(w, r.WithContext(ctx))
	}), &logs
}

func TestSecurity_A01_CrossOriginProtection(t *testing.T) {
	t.Parallel()

	h, logs := setup(t, Config{
		TrustedOrigins: []string{"https://admin.example.com"},
		BypassPatterns: []string{"POST /webhooks/"},
	}, nil)

	tests := []struct {
		name, method, path string
		headers            map[string]string
		want               int
	}{
		{
			name:    "cross-site post",
			method:  "POST",
			path:    "/posts",
			headers: map[string]string{"Sec-Fetch-Site": "cross-site"},
			want:    403,
		},
		{
			name:    "same-origin post",
			method:  "POST",
			path:    "/posts",
			headers: map[string]string{"Sec-Fetch-Site": "same-origin"},
			want:    200,
		},
		{
			name:    "same-site sibling post",
			method:  "POST",
			path:    "/posts",
			headers: map[string]string{"Sec-Fetch-Site": "same-site"},
			want:    403,
		},
		{
			name:    "origin mismatch",
			method:  "POST",
			path:    "/posts",
			headers: map[string]string{"Origin": "https://evil.com"},
			want:    403,
		},
		{
			name:    "origin match",
			method:  "POST",
			path:    "/posts",
			headers: map[string]string{"Origin": "http://example.com"},
			want:    200,
		},
		{
			name:    "trusted origin",
			method:  "POST",
			path:    "/posts",
			headers: map[string]string{"Sec-Fetch-Site": "cross-site", "Origin": "https://admin.example.com"},
			want:    200,
		},
		{
			name:    "bypass webhook",
			method:  "POST",
			path:    "/webhooks/stripe",
			headers: map[string]string{"Sec-Fetch-Site": "cross-site"},
			want:    200,
		},
		{
			name:    "cross-site get allowed",
			method:  "GET",
			path:    "/posts",
			headers: map[string]string{"Sec-Fetch-Site": "cross-site"},
			want:    200,
		},
		{name: "non-browser client", method: "POST", path: "/api/posts", want: 200},
	}

	for _, tt := range tests {
		r := httptest.NewRequestWithContext(t.Context(), tt.method, "http://example.com"+tt.path, http.NoBody)
		for k, v := range tt.headers {
			r.Header.Set(k, v)
		}

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)

		if rec.Code != tt.want {
			t.Errorf("%s: status = %d, want %d", tt.name, rec.Code, tt.want)
		}
	}

	if !strings.Contains(logs.String(), "event=csrf.rejected") {
		t.Fatalf("rejections not logged: %s", logs.String())
	}
}

func TestTokenFallback(t *testing.T) {
	t.Parallel()

	sess := &fakeSession{data: map[string]any{}}
	token := Token(web.WithSession(context.Background(), sess))
	if token == "" || Token(web.WithSession(context.Background(), sess)) != token {
		t.Fatal("Token not stable")
	}
	if Token(context.Background()) != "" {
		t.Fatal("Token without session")
	}

	h, _ := setup(t, Config{TokenFallback: true}, sess)

	post := func(header, field string, cookie bool) int {
		body := url.Values{}
		if field != "" {
			body.Set(FieldName, field)
		}

		r := httptest.NewRequestWithContext(
			t.Context(),
			"POST",
			"http://example.com/posts",
			strings.NewReader(body.Encode()),
		)
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if header != "" {
			r.Header.Set(HeaderName, header)
		}
		if cookie {
			r.AddCookie(&http.Cookie{Name: "vukem_session", Value: "x"})
		}

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)

		return rec.Code
	}

	if code := post("", "", true); code != 403 {
		t.Errorf("missing token = %d", code)
	}
	if code := post("wrong", "", true); code != 403 {
		t.Errorf("wrong token = %d", code)
	}
	if code := post(token, "", true); code != 200 {
		t.Errorf("header token = %d", code)
	}
	if code := post("", token, true); code != 200 {
		t.Errorf("form token = %d", code)
	}
	if code := post("", "", false); code != 200 {
		t.Errorf("no session cookie (API client) = %d", code)
	}
}

func TestConfigErrors(t *testing.T) {
	t.Parallel()

	if _, err := Middleware(Config{TrustedOrigins: []string{"not a url"}}); err == nil {
		t.Error("bad trusted origin accepted")
	}
	if _, err := Middleware(Config{BypassPatterns: []string{"GET /{bad"}}); err == nil {
		t.Error("bad bypass pattern accepted")
	}
}
