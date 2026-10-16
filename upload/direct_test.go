package upload

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/storage"
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
	_ = storage.PutBytes(t.Context(), disk, "a/good.png", []byte(pngMagic+"data"))
	_ = storage.PutBytes(t.Context(), disk, "a/spoofed.png", []byte("<html></html>"))
	_ = storage.PutBytes(t.Context(), disk, "a/big.png", []byte(pngMagic+strings.Repeat("x", 2<<10)))

	if f, err := Verify(t.Context(), disk, "a/good.png", rules); err != nil || f.Type != "image/png" || f.Size != 12 {
		t.Fatalf("Verify(good) = %+v, %v", f, err)
	}

	for key, want := range map[string]int{"a/spoofed.png": 422, "a/big.png": 413} {
		if _, err := Verify(t.Context(), disk, key, rules); errs.Status(err) != want {
			t.Errorf("Verify(%s) = %v, want %d", key, err, want)
		}

		if ok, _ := disk.Exists(t.Context(), key); ok {
			t.Errorf("Verify(%s) kept the rejected object", key)
		}
	}
}
