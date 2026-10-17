package upload

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/storage"
	"github.com/AlexGalhardo/vukem/web"
)

type fakePresigner struct {
	key, contentType string
	maxSize          int64
	ttl              time.Duration
}

func (p *fakePresigner) PresignPut(
	_ context.Context,
	key, contentType string,
	maxSize int64,
	ttl time.Duration,
) (string, http.Header, error) {
	p.key, p.contentType, p.maxSize, p.ttl = key, contentType, maxSize, ttl

	return "https://bucket.example/" + key + "?sig=x", http.Header{"Content-Type": {contentType}}, nil
}

func TestDirect(t *testing.T) {
	t.Parallel()

	p := &fakePresigner{}
	rules := Images(1 << 20)
	rules.Dir = "avatars"

	h, err := Direct(DirectConfig{Presigner: p, Rules: rules, Authorize: func(*http.Request) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}

	post := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/presign", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Accept", "application/json")

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)

		return rec
	}

	rec := post(`{"filename":"../me.png","content_type":"image/png","size":2048}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST = %d %s", rec.Code, rec.Body)
	}

	var out Presigned
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(out.Key, "avatars/") || !strings.HasSuffix(out.Key, ".png") ||
		strings.Contains(out.Key, "..") || strings.Contains(out.Key, "me.png") ||
		out.Method != http.MethodPut ||
		out.Headers["Content-Type"] != "image/png" ||
		!strings.Contains(out.URL, out.Key) {
		t.Errorf("Presigned = %+v", out)
	}

	if p.key != out.Key || p.contentType != "image/png" || p.maxSize != 2048 || p.ttl != DefaultPresignTTL {
		t.Errorf("PresignPut got %+v", p)
	}

	for body, want := range map[string]int{
		`{"filename":"x.png","content_type":"image/svg+xml","size":10}`:  422,
		`{"filename":"x.jpg","content_type":"image/png","size":10}`:      422,
		`{"filename":"x.png","content_type":"image/png","size":2097152}`: 413,
		`{"filename":"x.png","content_type":"image/png","size":0}`:       422,
		`{"filename":"x.png","extra":1}`:                                 400,
	} {
		if rec := post(body); rec.Code != want {
			t.Errorf("POST %s = %d, want %d", body, rec.Code, want)
		}
	}
}

func TestDirectRequiresAuthorize(t *testing.T) {
	t.Parallel()

	if _, err := Direct(DirectConfig{Presigner: &fakePresigner{}, Rules: Images(1)}); err == nil {
		t.Fatal("Direct(no Authorize) = nil error, want a boot error")
	}

	h, err := Direct(DirectConfig{
		Presigner: &fakePresigner{}, Rules: Images(1),
		Authorize: func(*http.Request) error { return errors.New("store down") },
	})
	if err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader("{}"))
	r.Header.Set("Accept", "application/json")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST with a failing Authorize = %d, want 403", rec.Code)
	}
}

func TestVerify(t *testing.T) {
	t.Parallel()

	disk := storage.NewMemory("")
	rules := Images(1 << 10)
	rules.Dir = "a"

	good, spoofed, big := rules.storedPath(".png"), rules.storedPath(".png"), rules.storedPath(".png")
	_ = storage.PutBytes(t.Context(), disk, good, []byte(pngHead+"data"))
	_ = storage.PutBytes(t.Context(), disk, spoofed, []byte("<html></html>"))
	_ = storage.PutBytes(t.Context(), disk, big, []byte(pngMagic+strings.Repeat("x", 2<<10)))

	if f, err := Verify(
		t.Context(),
		disk,
		good,
		rules,
	); err != nil || f.Type != "image/png" ||
		f.Size != int64(len(pngHead+"data")) {
		t.Fatalf("Verify(good) = %+v, %v", f, err)
	}

	for key, want := range map[string]int{spoofed: 422, big: 413} {
		if _, err := Verify(t.Context(), disk, key, rules); errs.Status(err) != want {
			t.Errorf("Verify(%s) = %v, want %d", key, err, want)
		}

		if ok, _ := disk.Exists(t.Context(), key); ok {
			t.Errorf("Verify(%s) kept the rejected object", key)
		}
	}
}

// Verify takes a client-reported key: only keys Presign could have issued
// (a random name under Rules.Dir) may be opened or deleted, so a client
// cannot make the server delete or adopt another object.
func TestSecurity_A01_DirectVerifyOnlyIssuedKeys(t *testing.T) {
	t.Parallel()

	disk := storage.NewMemory("")
	rules := Images(1 << 10)
	rules.Dir = "uploads"

	others := []string{
		"invoices/other-user.pdf", "uploads/avatar.png", "uploads/nested/" + strings.Repeat("a", 26) + ".png",
		"uploads/" + strings.Repeat("a", 26), "uploads/" + strings.Repeat("A", 26) + ".png", "uploads/../x.png",
		strings.Repeat("a", 26) + ".png",
	}

	for _, key := range others {
		_ = storage.PutBytes(t.Context(), disk, key, []byte("<html>someone else's</html>"))

		if _, err := Verify(t.Context(), disk, key, rules); errs.Status(err) != http.StatusBadRequest {
			t.Errorf("Verify(%q) = %v, want 400", key, err)
		}

		if ok, _ := disk.Exists(t.Context(), key); !ok && !strings.Contains(key, "..") {
			t.Errorf("Verify(%q) deleted an object it did not issue", key)
		}
	}
}

// failingWriter accepts headers but fails every body write (client gone).
type failingWriter struct{ *httptest.ResponseRecorder }

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestDirectLogsWriteErrors(t *testing.T) {
	t.Parallel()

	h, err := Direct(DirectConfig{
		Presigner: &fakePresigner{}, Rules: Images(1 << 20), Authorize: func(*http.Request) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer

	svc := &web.Services{Log: slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))}
	r := httptest.NewRequestWithContext(web.WithServices(t.Context(), svc), http.MethodPost, "/presign",
		strings.NewReader(`{"filename":"a.png","content_type":"image/png","size":10}`))
	r.Header.Set("Content-Type", "application/json")

	h.ServeHTTP(failingWriter{httptest.NewRecorder()}, r)

	if !strings.Contains(buf.String(), "broken pipe") {
		t.Fatalf("log = %q, want the response write error", buf.String())
	}
}
