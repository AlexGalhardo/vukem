//go:build integration

package s3

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/storage"
	"github.com/AlexGalhardo/vukem/storage/storagetest"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}

	return fallback
}

// openS3 connects to S3_ENDPOINT (task services:up starts RustFS on
// :59000) and creates a bucket unique to the test.
func openS3(t *testing.T) *Disk {
	t.Helper()

	d, err := New(Config{
		Bucket:          "vukem-" + strings.ToLower(rand.Text()[:12]),
		Region:          "us-east-1",
		Endpoint:        env("S3_ENDPOINT", "http://localhost:59000"),
		AccessKeyID:     env("S3_ACCESS_KEY_ID", "vukem"),
		SecretAccessKey: config.NewSecret(env("S3_SECRET_ACCESS_KEY", "vukem-secret")),
		UsePathStyle:    true,
		InsecureHTTP:    true,
		PartSize:        MinPartSize,
	})
	if err != nil {
		t.Fatal(err)
	}

	resp, err := d.do(t.Context(), http.MethodPut, "", nil, nil, nil)
	if err != nil {
		t.Fatalf("s3: %v (start it with task services:up)", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create bucket: %v", readError("create bucket", d.bucket, resp))
	}

	drain(resp)

	t.Cleanup(func() {
		ctx := t.Context()
		keys, _ := d.List(ctx, "")

		for _, k := range keys {
			_ = d.Delete(ctx, k)
		}

		if resp, err := d.do(ctx, http.MethodDelete, "", nil, nil, nil); err == nil {
			drain(resp)
		}
	})

	return d
}

func TestS3Conformance(t *testing.T) {
	d := openS3(t)
	d.maxKeys = 2

	storagetest.Run(t, d)
	storagetest.RunTraversal(t, d)
}

func TestS3MultipartAndStat(t *testing.T) {
	ctx := t.Context()
	d := openS3(t)

	data := make([]byte, 2*MinPartSize+12345)
	_, _ = rand.Read(data)

	if err := d.Put(ctx, "videos/clip.mp4", struct{ io.Reader }{bytes.NewReader(data)}); err != nil {
		t.Fatal(err)
	}

	got, err := storage.Get(ctx, d, "videos/clip.mp4")
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("Get = %d bytes, %v", len(got), err)
	}

	info, err := d.Stat(ctx, "videos/clip.mp4")
	if err != nil || info.Size != int64(len(data)) ||
		!strings.HasSuffix(info.ETag, "-3") {
		t.Errorf("Stat = %+v %v, want a 3-part ETag", info, err)
	}

	if _, err := d.Stat(ctx, "videos/missing.mp4"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(missing) = %v", err)
	}
}

func TestS3Presign(t *testing.T) {
	ctx := t.Context()
	d := openS3(t)

	putURL, err := d.PresignPut("uploads/report final.pdf", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	req, _ := http.NewRequestWithContext(ctx, http.MethodPut, putURL, strings.NewReader("%PDF-1.7"))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	drain(resp)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("presigned PUT = %d", resp.StatusCode)
	}

	getURL, err := d.PresignGet("uploads/report final.pdf", time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, getURL, http.NoBody)

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK || string(body) != "%PDF-1.7" {
		t.Fatalf("presigned GET = %d %q", resp.StatusCode, body)
	}

	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, strings.Replace(getURL, "report", "other", 1), http.NoBody)

	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	drain(resp)

	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("tampered presigned GET = %d, want 403", resp.StatusCode)
	}
}

func TestS3PresignPutWith(t *testing.T) {
	d := openS3(t)
	data := []byte("\x89PNG fake")

	u, err := d.PresignPutWith("avatars/1.png", time.Minute, PresignPutOptions{
		ContentType:   "image/png",
		ContentLength: int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}

	put := func(ctype string, body []byte) int {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodPut, u, bytes.NewReader(body))
		req.Header.Set("Content-Type", ctype)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}

		drain(resp)

		return resp.StatusCode
	}

	if got := put("text/html", data); got != http.StatusForbidden {
		t.Errorf("PUT with another content type = %d, want 403", got)
	}

	if got := put("image/png", append(slices.Clone(data), 'x')); got != http.StatusForbidden {
		t.Errorf("PUT with another size = %d, want 403", got)
	}

	if got := put("image/png", data); got != http.StatusOK {
		t.Fatalf("matching PUT = %d, want 200", got)
	}

	info, err := d.Stat(t.Context(), "avatars/1.png")
	if err != nil || info.Size != int64(len(data)) || info.ContentType != "image/png" {
		t.Errorf("Stat = %+v %v", info, err)
	}
}
