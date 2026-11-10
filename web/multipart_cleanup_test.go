package web

import (
	"bytes"
	"errors"
	"io/fs"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurity_A06_MultipartTempFilesRemoved(t *testing.T) {
	t.Parallel()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("upload", "big.bin")
	if err != nil {
		t.Fatal(err)
	}
	// Larger than maxMultipartMemory so the part spills to a temp file,
	// still under DefaultMaxBody.
	if _, err := part.Write(bytes.Repeat([]byte("x"), maxMultipartMemory+1<<20)); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodPost, "/", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())

	var fh *multipart.FileHeader
	NewCtx(httptest.NewRecorder(), r, nil).Serve(func(c *Ctx) error {
		f, h, err := c.FormFile("upload")
		if err != nil {
			return err
		}
		fh = h
		return f.Close()
	})

	if fh == nil {
		t.Fatal("handler did not receive the upload")
	}
	f, err := fh.Open()
	if err == nil {
		_ = f.Close()
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("FileHeader.Open() after Serve = %v, want fs.ErrNotExist", err)
	}
}
