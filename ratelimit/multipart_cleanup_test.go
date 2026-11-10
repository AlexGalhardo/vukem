package ratelimit

import (
	"bytes"
	"errors"
	"io/fs"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/web"
)

func TestSecurity_A06_RateLimitRemovesMultipartFiles(t *testing.T) {
	t.Parallel()

	// A key function that reads the form, as auth.ThrottleKeys does.
	byField := func(r *http.Request) string { return "email:" + r.PostFormValue("email") }
	h := Middleware(NewMemory(1, time.Minute), byField)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	ctx := web.WithServices(t.Context(), &web.Services{Log: slog.New(slog.DiscardHandler)})

	for i, want := range []int{http.StatusOK, http.StatusTooManyRequests} {
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		_ = mw.WriteField("email", "a@example.com")
		part, err := mw.CreateFormFile("upload", "big.bin")
		if err != nil {
			t.Fatal(err)
		}
		// Over the 32 MiB that PostFormValue keeps in memory.
		if _, err := part.Write(bytes.Repeat([]byte("x"), 33<<20)); err != nil {
			t.Fatal(err)
		}
		if err := mw.Close(); err != nil {
			t.Fatal(err)
		}

		r := httptest.NewRequestWithContext(ctx, http.MethodPost, "http://example.com/login", &body)
		r.Header.Set("Content-Type", mw.FormDataContentType())

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)

		if rec.Code != want {
			t.Fatalf("request %d: status = %d, want %d", i, rec.Code, want)
		}

		f, err := r.MultipartForm.File["upload"][0].Open()
		if err == nil {
			_ = f.Close()
		}
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("request %d: FileHeader.Open() after the limiter = %v, want fs.ErrNotExist", i, err)
		}
	}
}
