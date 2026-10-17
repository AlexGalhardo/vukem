package upload

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/storage"
	"github.com/AlexGalhardo/vukem/z"
)

type tusEnv struct {
	h      *TusHandler
	srv    *httptest.Server
	done   chan string // methods of finished requests
	mu     sync.Mutex
	got    []byte
	info   TusInfo
	stored File
	now    time.Time
}

func newTus(t *testing.T, mutate func(*TusConfig)) *tusEnv {
	t.Helper()

	env := &tusEnv{done: make(chan string, 16), now: time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)}
	disk := storage.NewMemory("")

	cfg := TusConfig{
		Dir:      t.TempDir(),
		BasePath: "/files/",
		MaxSize:  1 << 20,
		Authorize: func(r *http.Request) error {
			if r.Header.Get("Authorization") != "Bearer ok" {
				return errs.ErrUnauthorized
			}

			return nil
		},
		OnComplete: func(ctx context.Context, info TusInfo, open func() (io.ReadCloser, error)) error {
			rc, err := open()
			if err != nil {
				return err
			}
			defer rc.Close()

			b, err := io.ReadAll(rc)
			if err != nil {
				return err
			}

			f, err := Save(ctx, disk, Images(1<<20), "file", info.Filename(), bytes.NewReader(b))

			env.mu.Lock()
			env.got, env.info, env.stored = b, info, f
			env.mu.Unlock()

			return err
		},
		Now: func() time.Time {
			env.mu.Lock()
			defer env.mu.Unlock()

			return env.now
		},
	}

	if mutate != nil {
		mutate(&cfg)
	}

	h, err := Tus(cfg)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = h.Close() })

	mux := http.NewServeMux()
	mux.Handle("/files/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r)
		env.done <- r.Method
	}))

	env.h = h
	env.srv = httptest.NewServer(mux)
	t.Cleanup(env.srv.Close)

	return env
}

// tusRes is the part of a response the tests check.
type tusRes struct {
	StatusCode int
	Header     http.Header
	Body       string
}

func (e *tusEnv) do(t *testing.T, method, target string, body io.Reader, headers map[string]string) tusRes {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, e.srv.URL+target, body)
	if err != nil {
		t.Fatal(err)
	}

	req.Header.Set("Tus-Resumable", TusVersion)
	req.Header.Set("Authorization", "Bearer ok")

	for k, v := range headers {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}

	res, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}

	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	<-e.done

	return tusRes{StatusCode: res.StatusCode, Header: res.Header, Body: string(b)}
}

func (e *tusEnv) create(t *testing.T, length int, filename string) string {
	t.Helper()

	res := e.do(t, http.MethodPost, "/files/", nil, map[string]string{
		"Upload-Length":   strconv.Itoa(length),
		"Upload-Metadata": "filename " + base64.StdEncoding.EncodeToString([]byte(filename)) + ",private",
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("POST = %d, want 201", res.StatusCode)
	}

	loc := res.Header.Get("Location")
	if !strings.HasPrefix(loc, "/files/") || len(loc) != len("/files/")+idLen {
		t.Fatalf("Location = %q, want /files/<26-char id>", loc)
	}

	return loc
}

// interruptedPatch sends a PATCH announcing the whole remainder but closes
// the connection after sending only part of it, like a dropped network.
func (e *tusEnv) interruptedPatch(t *testing.T, loc string, offset, length int, sent []byte) {
	t.Helper()

	conn, err := net.Dial("tcp", e.srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}

	fmt.Fprintf(conn, "PATCH %s HTTP/1.1\r\nHost: test\r\nTus-Resumable: 1.0.0\r\nAuthorization: Bearer ok\r\n"+
		"Content-Type: application/offset+octet-stream\r\nUpload-Offset: %d\r\nContent-Length: %d\r\n\r\n",
		loc, offset, length)
	_, _ = conn.Write(sent)
	_ = conn.Close()

	if m := <-e.done; m != http.MethodPatch {
		t.Fatalf("finished %s, want PATCH", m)
	}
}

func TestTusResumesAfterInterruption(t *testing.T) {
	t.Parallel()

	env := newTus(t, nil)
	content := []byte(pngHead + strings.Repeat("0123456789", 20_000))
	half := len(content) / 2

	res := env.do(t, http.MethodOptions, "/files/", nil, map[string]string{"Tus-Resumable": "", "Authorization": ""})
	if res.StatusCode != http.StatusNoContent || res.Header.Get("Tus-Version") != "1.0.0" ||
		res.Header.Get("Tus-Max-Size") != "1048576" || !strings.Contains(res.Header.Get("Tus-Extension"), "creation") {
		t.Fatalf("OPTIONS = %d %v", res.StatusCode, res.Header)
	}

	loc := env.create(t, len(content), "../holiday.png")

	env.interruptedPatch(t, loc, 0, len(content), content[:half])

	res = env.do(t, http.MethodHead, loc, nil, nil)
	if res.StatusCode != http.StatusOK || res.Header.Get("Upload-Offset") != strconv.Itoa(half) ||
		res.Header.Get("Upload-Length") != strconv.Itoa(len(content)) || res.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("HEAD after interruption = %d %v, want offset %d", res.StatusCode, res.Header, half)
	}

	patch := map[string]string{"Content-Type": "application/offset+octet-stream", "Upload-Offset": "0"}
	if res := env.do(t, http.MethodPatch, loc, bytes.NewReader(content), patch); res.StatusCode != http.StatusConflict {
		t.Fatalf("PATCH at a stale offset = %d, want 409", res.StatusCode)
	}

	patch["Upload-Offset"] = strconv.Itoa(half)

	res = env.do(t, http.MethodPatch, loc, bytes.NewReader(content[half:]), patch)
	if res.StatusCode != http.StatusNoContent || res.Header.Get("Upload-Offset") != strconv.Itoa(len(content)) {
		t.Fatalf("PATCH rest = %d %v", res.StatusCode, res.Header)
	}

	env.mu.Lock()
	got, info, stored := env.got, env.info, env.stored
	env.mu.Unlock()

	if !bytes.Equal(got, content) {
		t.Fatalf("OnComplete got %d bytes, want the %d uploaded", len(got), len(content))
	}

	if info.Filename() != "../holiday.png" || info.Metadata["private"] != "" || info.Offset != info.Length {
		t.Errorf("info = %+v", info)
	}

	if stored.Name != "holiday.png" || stored.Type != "image/png" || stored.Size != int64(len(content)) {
		t.Errorf("stored = %+v", stored)
	}

	if res := env.do(t, http.MethodHead, loc, nil, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("HEAD after completion = %d, want 404 (partial data removed)", res.StatusCode)
	}
}

func TestTusRejects(t *testing.T) {
	t.Parallel()

	env := newTus(t, nil)
	loc := env.create(t, 10, "a.png")
	patch := map[string]string{"Content-Type": "application/offset+octet-stream", "Upload-Offset": "0"}

	tests := []struct {
		name    string
		method  string
		target  string
		body    string
		headers map[string]string
		want    int
	}{
		{"no Tus-Resumable", http.MethodHead, loc, "", map[string]string{"Tus-Resumable": ""}, 412},
		{"unauthorized", http.MethodHead, loc, "", map[string]string{"Authorization": ""}, 401},
		{"too long", http.MethodPost, "/files/", "", map[string]string{"Upload-Length": "1048577"}, 413},
		{"no length", http.MethodPost, "/files/", "", nil, 400},
		{
			"bad metadata",
			http.MethodPost,
			"/files/",
			"",
			map[string]string{"Upload-Length": "1", "Upload-Metadata": "k !!"},
			400,
		},
		{"unknown id", http.MethodHead, "/files/aaaaaaaaaaaaaaaaaaaaaaaaaa", "", nil, 404},
		{"traversal id", http.MethodHead, "/files/aaaaaaaaaaaaaaaaaaaaaaaaaa.json", "", nil, 404},
		{
			"wrong content type",
			http.MethodPatch,
			loc,
			"x",
			map[string]string{"Content-Type": "text/plain", "Upload-Offset": "0"},
			415,
		},
		{"body over the length", http.MethodPatch, loc, strings.Repeat("x", 11), patch, 413},
	}

	for _, tt := range tests {
		var body io.Reader
		if tt.body != "" {
			body = strings.NewReader(tt.body)
		}

		if res := env.do(t, tt.method, tt.target, body, tt.headers); res.StatusCode != tt.want {
			t.Errorf("%s: %s %s = %d, want %d", tt.name, tt.method, tt.target, res.StatusCode, tt.want)
		}
	}
}

func TestTusCompletionValidatesContent(t *testing.T) {
	t.Parallel()

	env := newTus(t, nil)
	content := "<html>not an image</html>"
	loc := env.create(t, len(content), "x.png")

	patch := map[string]string{"Content-Type": "application/offset+octet-stream", "Upload-Offset": "0"}
	if res := env.do(
		t,
		http.MethodPatch,
		loc,
		strings.NewReader(content),
		patch,
	); res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("PATCH spoofed image = %d, want 422", res.StatusCode)
	}

	if res := env.do(t, http.MethodHead, loc, nil, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("HEAD after rejected completion = %d, want 404", res.StatusCode)
	}
}

// z validation errors from Authorize and OnComplete are 422s: a
// z.FieldError message reaches the client, a plain Refine error does not.
func TestTusZValidationErrors(t *testing.T) {
	t.Parallel()

	_, refineErr := z.String().Refine(func(string) error { return errors.New("db: 10.0.0.5") }).Parse("x")

	tests := []struct {
		name     string
		mutate   func(*TusConfig)
		wantBody string
	}{
		{"authorize field error", func(cfg *TusConfig) {
			cfg.Authorize = func(*http.Request) error { return z.FieldError("project", "Unknown project.") }
		}, "Unprocessable Entity"}, // Authorize errors never carry text
		{"complete field error", func(cfg *TusConfig) {
			cfg.OnComplete = func(context.Context, TusInfo, func() (io.ReadCloser, error)) error {
				return z.FieldError("file", "The file is not a PNG image.")
			}
		}, "The file is not a PNG image."},
		{"complete plain refine error", func(cfg *TusConfig) {
			cfg.OnComplete = func(context.Context, TusInfo, func() (io.ReadCloser, error)) error { return refineErr }
		}, "The value field is invalid."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := newTus(t, tt.mutate)

			var res tusRes
			if tt.name == "authorize field error" {
				res = env.do(t, http.MethodPost, "/files/", nil, map[string]string{"Upload-Length": "1"})
			} else {
				loc := env.create(t, 1, "a.png")
				res = env.do(t, http.MethodPatch, loc, strings.NewReader("x"),
					map[string]string{"Content-Type": "application/offset+octet-stream", "Upload-Offset": "0"})
			}

			if res.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(res.Body, tt.wantBody) ||
				strings.Contains(res.Body, "10.0.0.5") {
				t.Errorf("status = %d body %q, want 422 with %q", res.StatusCode, res.Body, tt.wantBody)
			}
		})
	}
}

func TestTusExpiryAndTermination(t *testing.T) {
	t.Parallel()

	env := newTus(t, func(c *TusConfig) { c.Expiry = time.Hour })
	expiring := env.create(t, 10, "a.png")
	terminated := env.create(t, 10, "b.png")

	if res := env.do(t, http.MethodDelete, terminated, nil, nil); res.StatusCode != http.StatusNoContent {
		t.Fatalf("DELETE = %d, want 204", res.StatusCode)
	}

	if res := env.do(t, http.MethodHead, terminated, nil, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("HEAD after DELETE = %d, want 404", res.StatusCode)
	}

	env.mu.Lock()
	env.now = env.now.Add(2 * time.Hour)
	env.mu.Unlock()

	n, err := env.h.Cleanup(t.Context())
	if err != nil || n != 1 {
		t.Fatalf("Cleanup() = %d, %v, want 1", n, err)
	}

	if res := env.do(t, http.MethodHead, expiring, nil, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("HEAD after expiry = %d, want 404", res.StatusCode)
	}
}

func TestTusConfigIsValidatedAtBoot(t *testing.T) {
	t.Parallel()

	ok := TusConfig{
		Dir: t.TempDir(), BasePath: "/f/", MaxSize: 1,
		Authorize:  func(*http.Request) error { return nil },
		OnComplete: func(context.Context, TusInfo, func() (io.ReadCloser, error)) error { return nil },
	}

	for name, mutate := range map[string]func(*TusConfig){
		"no Authorize":  func(c *TusConfig) { c.Authorize = nil },
		"no OnComplete": func(c *TusConfig) { c.OnComplete = nil },
		"no MaxSize":    func(c *TusConfig) { c.MaxSize = 0 },
		"no Dir":        func(c *TusConfig) { c.Dir = "" },
	} {
		cfg := ok
		mutate(&cfg)

		if h, err := Tus(cfg); err == nil {
			_ = h.Close()
			t.Errorf("Tus(%s) = nil error, want a boot error", name)
		}
	}

	h, err := Tus(ok)
	if err != nil {
		t.Fatalf("Tus(valid) = %v", err)
	}

	_ = h.Close()
}
