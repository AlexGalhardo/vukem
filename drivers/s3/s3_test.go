package s3

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/storage"
	"github.com/AlexGalhardo/vukem/storage/storagetest"
)

func testConfig(endpoint string) Config {
	return Config{
		Bucket:          testBucket,
		Region:          testRegion,
		Endpoint:        endpoint,
		AccessKeyID:     testAccessKey,
		SecretAccessKey: config.NewSecret(testSecret),
		UsePathStyle:    true,
		InsecureHTTP:    true,
		PartSize:        MinPartSize,
	}
}

// newTestDisk starts a fake S3 and a path-style disk on it.
func newTestDisk(t *testing.T) (*Disk, *fakeS3) {
	t.Helper()

	f := newFakeS3()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	d, err := New(testConfig(srv.URL))
	if err != nil {
		t.Fatal(err)
	}

	d.maxKeys = 2 // exercise ListObjectsV2 pagination

	return d, f
}

func TestConformance(t *testing.T) {
	t.Parallel()

	t.Run("path style", func(t *testing.T) {
		t.Parallel()

		d, _ := newTestDisk(t)
		storagetest.Run(t, d)
		storagetest.RunTraversal(t, d)
	})

	t.Run("virtual hosted", func(t *testing.T) {
		t.Parallel()

		f := newFakeS3()
		srv := httptest.NewServer(f)
		t.Cleanup(srv.Close)

		// bucket.s3.test resolves to the fake whatever the host name.
		dialer := &net.Dialer{}
		client := &http.Client{Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
				return dialer.DialContext(ctx, network, srv.Listener.Addr().String())
			},
		}}

		cfg := testConfig("http://s3.test")
		cfg.UsePathStyle = false

		d, err := New(cfg, WithHTTPClient(client))
		if err != nil {
			t.Fatal(err)
		}

		storagetest.Run(t, d)
		storagetest.RunTraversal(t, d)
	})

	t.Run("tls", func(t *testing.T) {
		t.Parallel()

		srv := httptest.NewTLSServer(newFakeS3())
		t.Cleanup(srv.Close)

		cfg := testConfig(srv.URL)
		cfg.InsecureHTTP = false

		d, err := New(cfg, WithHTTPClient(srv.Client()))
		if err != nil {
			t.Fatal(err)
		}

		storagetest.Run(t, d)
	})
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)

	return b
}

func TestPutMultipart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		size  int
		calls []string
	}{
		{"below one part", MinPartSize - 1, []string{"PUT object"}},
		{"exactly one part", MinPartSize, []string{"PUT object"}},
		{
			"three parts", 2*MinPartSize + 3,
			[]string{"POST uploads", "PUT partNumber", "PUT partNumber", "PUT partNumber", "POST uploadId"},
		},
		{
			"two full parts", 2 * MinPartSize,
			[]string{"POST uploads", "PUT partNumber", "PUT partNumber", "POST uploadId"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d, f := newTestDisk(t)
			data := randomBytes(tt.size)

			// A reader without WriterTo/Len, like a request body.
			if err := d.Put(t.Context(), "big/file.bin", struct{ io.Reader }{bytes.NewReader(data)}); err != nil {
				t.Fatal(err)
			}

			got, err := storage.Get(t.Context(), d, "big/file.bin")
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("Get = %d bytes, %v; want %d bytes", len(got), err, len(data))
			}

			calls, _, pending := f.snapshot()
			if diff := cmp.Diff(tt.calls, calls[:len(tt.calls)]); diff != "" {
				t.Errorf("calls (-want +got):\n%s", diff)
			}

			if pending != 0 {
				t.Errorf("%d multipart uploads left open", pending)
			}
		})
	}
}

type failingReader struct {
	r     io.Reader
	after int
}

func (f *failingReader) Read(p []byte) (int, error) {
	if f.after <= 0 {
		return 0, errors.New("client went away")
	}

	p = p[:min(len(p), f.after)]
	n, err := f.r.Read(p)
	f.after -= n

	return n, err
}

func TestPutMultipartAborts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(*fakeS3)
		body  func() io.Reader
	}{
		{
			name:  "part fails",
			setup: func(f *fakeS3) { f.failPart = 2 },
			body:  func() io.Reader { return bytes.NewReader(randomBytes(3 * MinPartSize)) },
		},
		{
			name:  "reader fails",
			setup: func(*fakeS3) {},
			body: func() io.Reader {
				return &failingReader{r: bytes.NewReader(randomBytes(3 * MinPartSize)), after: MinPartSize + 100}
			},
		},
		{
			name:  "complete answers 200 with an error",
			setup: func(f *fakeS3) { f.badDone = true },
			body:  func() io.Reader { return bytes.NewReader(randomBytes(MinPartSize + 1)) },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d, f := newTestDisk(t)
			tt.setup(f)

			if err := d.Put(t.Context(), "big.bin", tt.body()); err == nil {
				t.Fatal("Put succeeded")
			}

			_, aborted, pending := f.snapshot()
			if aborted != 1 || pending != 0 {
				t.Errorf("aborted = %d, pending = %d; want 1, 0", aborted, pending)
			}

			if ok, _ := d.Exists(t.Context(), "big.bin"); ok {
				t.Error("failed upload left an object")
			}
		})
	}
}

func TestPutAbortsWhenContextCanceled(t *testing.T) {
	t.Parallel()

	d, f := newTestDisk(t)
	ctx, cancel := context.WithCancel(t.Context())

	r := io.MultiReader(bytes.NewReader(randomBytes(MinPartSize+10)), readerFunc(func([]byte) (int, error) {
		cancel()
		return 0, context.Canceled
	}))

	if err := d.Put(ctx, "big.bin", r); err == nil {
		t.Fatal("Put succeeded")
	}

	if _, aborted, pending := f.snapshot(); aborted != 1 || pending != 0 {
		t.Errorf("aborted = %d, pending = %d; want 1, 0", aborted, pending)
	}
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

func TestStat(t *testing.T) {
	t.Parallel()

	d, _ := newTestDisk(t)
	ctx := t.Context()

	if err := storage.PutBytes(ctx, d, "img/logo.png", []byte("\x89PNG\r\n\x1a\nrest")); err != nil {
		t.Fatal(err)
	}

	if err := storage.PutBytes(ctx, d, "notes/readme", []byte("plain words")); err != nil {
		t.Fatal(err)
	}

	info, err := d.Stat(ctx, "img/logo.png")
	if err != nil {
		t.Fatal(err)
	}

	if info.Key != "img/logo.png" || info.Size != 12 || info.ContentType != "image/png" || info.ETag == "" ||
		info.LastModified.IsZero() || strings.Contains(info.ETag, `"`) {
		t.Errorf("Stat = %+v", info)
	}

	if info, _ := d.Stat(ctx, "notes/readme"); info.ContentType != "text/plain; charset=utf-8" {
		t.Errorf("sniffed content type = %q", info.ContentType)
	}

	if _, err := d.Stat(ctx, "missing.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(missing) = %v, want fs.ErrNotExist", err)
	}
}

func TestErrors(t *testing.T) {
	t.Parallel()

	f := newFakeS3()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	cfg := testConfig(srv.URL)
	cfg.SecretAccessKey = config.NewSecret("wrong-secret")

	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	_, err = d.Open(t.Context(), "a.txt")

	e, ok := errors.AsType[*Error](err)
	if !ok || e.StatusCode != http.StatusForbidden || e.Code != "SignatureDoesNotMatch" || e.RequestID != "REQ123" {
		t.Fatalf("Open with a bad secret = %v", err)
	}

	if errors.Is(err, fs.ErrNotExist) {
		t.Error("403 matched fs.ErrNotExist")
	}

	cfg = testConfig(srv.URL)
	cfg.Bucket = "other-bucket"

	d, _ = New(cfg)
	if _, err := d.Open(t.Context(), "a.txt"); errors.Is(err, fs.ErrNotExist) || err == nil {
		t.Errorf("missing bucket = %v, want a non-ErrNotExist error", err)
	}

	if _, err := d.List(t.Context(), ""); err == nil {
		t.Error("List on a missing bucket succeeded")
	}
}

func TestPresign(t *testing.T) {
	t.Parallel()

	d, _ := newTestDisk(t)
	ctx := t.Context()
	data := []byte("uploaded by the browser")

	putURL, err := d.PresignPut("uploads/a b+c.txt", 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(putURL, testSecret) {
		t.Fatal("presigned URL contains the secret")
	}

	req, _ := http.NewRequestWithContext(ctx, http.MethodPut, putURL, bytes.NewReader(data))
	req.Header.Set("Content-Type", "text/plain")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("presigned PUT = %d", resp.StatusCode)
	}

	getURL, err := d.PresignGet("uploads/a b+c.txt", time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	status, body := httpGet(t, getURL)
	if status != http.StatusOK || body != string(data) {
		t.Fatalf("presigned GET = %d %q", status, body)
	}

	// Tampering with the key or the expiry breaks the signature.
	for _, bad := range []string{
		strings.Replace(getURL, "a%20b", "x%20b", 1),
		strings.Replace(getURL, "X-Amz-Expires=60", "X-Amz-Expires=600", 1),
	} {
		if status, _ := httpGet(t, bad); status != http.StatusForbidden {
			t.Errorf("tampered URL = %d, want 403", status)
		}
	}

	// A URL signed an hour ago with a one-minute expiry is refused.
	past, _ := New(testConfig(strings.SplitN(getURL, "/"+testBucket, 2)[0]),
		WithClock(func() time.Time { return time.Now().Add(-time.Hour) }))

	expired, _ := past.PresignGet("uploads/a b+c.txt", time.Minute)
	if status, _ := httpGet(t, expired); status != http.StatusForbidden {
		t.Errorf("expired URL = %d, want 403", status)
	}

	if _, err := d.PresignGet("../etc/passwd", time.Minute); !errors.Is(err, storage.ErrInvalidPath) {
		t.Errorf("PresignGet(traversal) = %v", err)
	}
}

func httpGet(t *testing.T, u string) (int, string) {
	t.Helper()

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, u, http.NoBody)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	b, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, string(b)
}

func TestSecurity_A01_S3PresignExpiryBounded(t *testing.T) {
	t.Parallel()

	d, _ := newTestDisk(t)

	for _, exp := range []time.Duration{0, -time.Minute, 500 * time.Millisecond, MaxPresignExpiry + time.Second, 30 * 24 * time.Hour} {
		if _, err := d.PresignGet("a.txt", exp); !errors.Is(err, ErrInvalidExpiry) {
			t.Errorf("PresignGet(%v) = %v, want ErrInvalidExpiry", exp, err)
		}

		if _, err := d.PresignPut("a.txt", exp); !errors.Is(err, ErrInvalidExpiry) {
			t.Errorf("PresignPut(%v) = %v, want ErrInvalidExpiry", exp, err)
		}
	}

	if u, err := d.PresignGet("a.txt", MaxPresignExpiry); err != nil || !strings.Contains(u, "X-Amz-Expires=604800") {
		t.Errorf("PresignGet(7 days) = %s %v", u, err)
	}
}

func TestSecurity_A04_S3RequiresTLS(t *testing.T) {
	t.Parallel()

	cfg := testConfig("http://localhost:9000")
	cfg.InsecureHTTP = false

	if _, err := New(cfg); !errors.Is(err, ErrConfig) {
		t.Errorf("http endpoint without InsecureHTTP = %v, want ErrConfig", err)
	}

	cfg.Endpoint = ""

	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if u, _ := d.PresignGet("a.txt", time.Minute); !strings.HasPrefix(u, "https://s3."+testRegion+".amazonaws.com/") {
		t.Errorf("default endpoint URL = %s", u)
	}

	cfg.Endpoint = "http://localhost:9000"
	cfg.InsecureHTTP = true

	if _, err := New(cfg); err != nil {
		t.Errorf("explicit InsecureHTTP = %v", err)
	}
}

func TestSecurity_A09_S3NeverLeaksCredentials(t *testing.T) {
	t.Parallel()

	d, _ := newTestDisk(t)
	cfg := testConfig("http://localhost:9000")
	cfg.SessionToken = config.NewSecret("session-token-value")

	out := fmt.Sprintf("%v %+v %#v %v %+v %s", cfg, cfg, cfg, d, *d, d)
	for _, secret := range []string{testSecret, "session-token-value"} {
		if strings.Contains(out, secret) {
			t.Errorf("formatted config or disk contains %q", secret)
		}
	}

	f := newFakeS3()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	bad := testConfig(srv.URL)
	bad.SecretAccessKey = config.NewSecret("wrong-secret")
	wrong, _ := New(bad)

	for _, err := range []error{
		storage.PutBytes(t.Context(), wrong, "a.txt", []byte("x")),
		wrong.Delete(t.Context(), "a.txt"),
	} {
		msg := fmt.Sprint(err)
		if err == nil || strings.Contains(msg, "wrong-secret") || strings.Contains(msg, "Signature=") ||
			strings.Contains(msg, "Credential=") {
			t.Errorf("error = %q", msg)
		}
	}
}

func TestNewValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		edit func(*Config)
	}{
		{"empty bucket", func(c *Config) { c.Bucket = "" }},
		{"short bucket", func(c *Config) { c.Bucket = "ab" }},
		{"uppercase bucket", func(c *Config) { c.Bucket = "MyBucket" }},
		{"bucket with slash", func(c *Config) { c.Bucket = "a/b-bucket" }},
		{"bucket with host", func(c *Config) { c.Bucket = "evil.com:80@x" }},
		{"bucket edge dash", func(c *Config) { c.Bucket = "-bucket" }},
		{"bucket double dot", func(c *Config) { c.Bucket = "a..b" }},
		{"empty region", func(c *Config) { c.Region = "" }},
		{"no access key", func(c *Config) { c.AccessKeyID = "" }},
		{"no secret", func(c *Config) { c.SecretAccessKey = config.Secret{} }},
		{"endpoint with path", func(c *Config) { c.Endpoint = "https://s3.example.com/bucket" }},
		{"endpoint with user", func(c *Config) { c.Endpoint = "https://u:p@s3.example.com" }},
		{"endpoint with query", func(c *Config) { c.Endpoint = "https://s3.example.com?x=1" }},
		{"endpoint ftp", func(c *Config) { c.Endpoint = "ftp://s3.example.com" }},
		{"endpoint no host", func(c *Config) { c.Endpoint = "https://" }},
		{"part too small", func(c *Config) { c.PartSize = MinPartSize - 1 }},
		{"part too large", func(c *Config) { c.PartSize = MaxPartSize + 1 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := testConfig("https://s3.example.com")
			tt.edit(&cfg)

			if _, err := New(cfg); !errors.Is(err, ErrConfig) {
				t.Errorf("New = %v, want ErrConfig", err)
			}
		})
	}

	cfg := testConfig("https://s3.example.com/")
	cfg.PartSize = 0

	d, err := New(cfg)
	if err != nil || d.partSize != DefaultPartSize {
		t.Errorf("New(default part size) = %v, %v", d, err)
	}
}

type mapSource map[string]string

func (m mapSource) Lookup(k string) (string, bool) {
	v, ok := m[k]
	return v, ok
}

func TestConfigFromEnv(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load[Config](mapSource{
		"S3_BUCKET":            "media",
		"S3_ENDPOINT":          "http://localhost:9000",
		"S3_ACCESS_KEY_ID":     "minio",
		"S3_SECRET_ACCESS_KEY": "minio-secret",
		"S3_USE_PATH_STYLE":    "true",
		"S3_INSECURE_HTTP":     "true",
		"S3_PART_SIZE":         "10485760",
		"S3_PUBLIC_URL":        "https://cdn.example.com/",
	})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Bucket != "media" || cfg.Region != "us-east-1" || cfg.Endpoint != "http://localhost:9000" ||
		cfg.AccessKeyID != "minio" || cfg.SecretAccessKey.Reveal() != "minio-secret" || !cfg.UsePathStyle ||
		!cfg.InsecureHTTP || cfg.PartSize != 10<<20 {
		t.Errorf("Load = %+v", cfg)
	}

	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	if got := d.URL("avatars/a b.png"); got != "https://cdn.example.com/avatars/a%20b.png" {
		t.Errorf("URL = %q", got)
	}

	if d.String() != "s3://media" {
		t.Errorf("String = %q", d.String())
	}
}

func TestURLPrivate(t *testing.T) {
	t.Parallel()

	d, _ := newTestDisk(t)
	if got := d.URL("a.txt"); got != "" {
		t.Errorf("URL on a private disk = %q", got)
	}
}

func presignedPut(t *testing.T, u, ctype string, body []byte) int {
	t.Helper()

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodPut, u, bytes.NewReader(body))
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	_ = resp.Body.Close()

	return resp.StatusCode
}

func TestPresignPutWith(t *testing.T) {
	t.Parallel()

	d, f := newTestDisk(t)
	data := []byte(`{"ok":true}`)

	u, err := d.PresignPutWith("uploads/a.json", 5*time.Minute, PresignPutOptions{
		ContentType:   "application/json",
		ContentLength: int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(u, "X-Amz-SignedHeaders=content-length%3Bcontent-type%3Bhost") {
		t.Errorf("PresignPutWith URL = %s, want content-length;content-type;host signed", u)
	}

	if got := presignedPut(t, u, "application/json", data); got != http.StatusOK {
		t.Fatalf("matching PUT = %d, want 200", got)
	}

	if got := f.objects["uploads/a.json"].ctype; got != "application/json" {
		t.Errorf("stored content type = %q", got)
	}

	// Zero options sign only host, like PresignPut.
	u, err = d.PresignPutWith("uploads/b.txt", time.Minute, PresignPutOptions{})
	if err != nil || !strings.Contains(u, "X-Amz-SignedHeaders=host&") {
		t.Errorf("PresignPutWith(zero) = %s %v", u, err)
	}

	for _, o := range []PresignPutOptions{
		{ContentLength: -1},
		{ContentLength: MaxPartSize + 1},
		{ContentType: "text/plain\r\nX-Evil: 1"},
		{ContentType: "not a media type"},
	} {
		if _, err := d.PresignPutWith("a.txt", time.Minute, o); !errors.Is(err, ErrInvalidPresign) {
			t.Errorf("PresignPutWith(%+v) = %v, want ErrInvalidPresign", o, err)
		}
	}

	if _, err := d.PresignPutWith("../x", time.Minute, PresignPutOptions{}); !errors.Is(err, storage.ErrInvalidPath) {
		t.Errorf("PresignPutWith(traversal) = %v", err)
	}

	if _, err := d.PresignPutWith("a.txt", 0, PresignPutOptions{}); !errors.Is(err, ErrInvalidExpiry) {
		t.Errorf("PresignPutWith(0s) = %v", err)
	}
}

func TestSecurity_A01_S3PresignPutBindsTypeAndSize(t *testing.T) {
	t.Parallel()

	d, f := newTestDisk(t)
	data := []byte("PNG-bytes")

	u, err := d.PresignPutWith("avatars/1.png", time.Minute, PresignPutOptions{
		ContentType:   "image/png",
		ContentLength: int64(len(data)),
	})
	if err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		ctype string
		body  []byte
	}{
		"other type":   {"text/html", data},
		"missing type": {"", data},
		"larger body":  {"image/png", append(slices.Clone(data), make([]byte, 1<<20)...)},
		"smaller body": {"image/png", data[:3]},
	} {
		if got := presignedPut(t, u, tc.ctype, tc.body); got != http.StatusForbidden {
			t.Errorf("%s: PUT = %d, want 403", name, got)
		}
	}

	if _, ok := f.objects["avatars/1.png"]; ok {
		t.Fatal("a mismatching upload was stored")
	}

	if got := presignedPut(t, u, "image/png", data); got != http.StatusOK {
		t.Errorf("matching PUT = %d, want 200", got)
	}
}

func TestOpenReportsValidators(t *testing.T) {
	t.Parallel()

	d, _ := newTestDisk(t)
	ctx := t.Context()

	if err := storage.PutBytes(ctx, d, "covers/a.webp", []byte("img")); err != nil {
		t.Fatal(err)
	}

	info, err := d.Stat(ctx, "covers/a.webp")
	if err != nil {
		t.Fatal(err)
	}

	f, err := d.Open(ctx, "covers/a.webp")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	// storage.HandlerWith reads these to answer conditional requests.
	tagged, ok := f.(interface {
		ETag() string
		ModTime() time.Time
	})
	if !ok {
		t.Fatalf("Open returned %T without ETag/ModTime", f)
	}

	if got, want := tagged.ETag(), `"`+info.ETag+`"`; got != want {
		t.Errorf("ETag() = %q, want %q", got, want)
	}

	if got := tagged.ModTime(); !got.Equal(info.LastModified) || got.IsZero() {
		t.Errorf("ModTime() = %v, want %v", got, info.LastModified)
	}

	if b, _ := io.ReadAll(f); string(b) != "img" {
		t.Errorf("body = %q", b)
	}
}
