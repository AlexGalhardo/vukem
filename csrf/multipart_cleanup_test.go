package csrf

import (
	"bytes"
	"errors"
	"io/fs"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AlexGalhardo/vukem/web"
)

func TestSecurity_A06_CSRFRejectRemovesMultipartFiles(t *testing.T) {
	t.Parallel()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField(FieldName, "wrong")
	part, err := mw.CreateFormFile("upload", "big.bin")
	if err != nil {
		t.Fatal(err)
	}
	// Over the 32 MiB that PostFormValue keeps in memory, so it spills to disk.
	if _, err := part.Write(bytes.Repeat([]byte("x"), 33<<20)); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	m, err := Middleware(Config{TokenFallback: true})
	if err != nil {
		t.Fatal(err)
	}

	sess := &fakeSession{data: map[string]any{web.TokenKey: "right"}}
	ctx := web.WithSession(web.WithServices(t.Context(), &web.Services{Log: slog.New(slog.DiscardHandler)}), sess)
	r := httptest.NewRequestWithContext(ctx, http.MethodPost, "http://example.com/upload", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.AddCookie(&http.Cookie{Name: "vukem_session", Value: "x"})

	rec := httptest.NewRecorder()
	m(http.NotFoundHandler()).ServeHTTP(rec, r)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if r.MultipartForm == nil || len(r.MultipartForm.File["upload"]) == 0 {
		t.Fatal("the middleware did not parse the multipart form")
	}

	f, err := r.MultipartForm.File["upload"][0].Open()
	if err == nil {
		_ = f.Close()
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("FileHeader.Open() after a rejected request = %v, want fs.ErrNotExist", err)
	}
}
