package storage_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/signed"
	"github.com/AlexGalhardo/vukem/storage"
	"github.com/AlexGalhardo/vukem/storage/storagetest"
	"github.com/AlexGalhardo/vukem/web"
)

func local(t *testing.T, base string) (*storage.Local, string) {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "disk")

	d, err := storage.NewLocal(dir, base)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = d.Close() })

	return d, dir
}

func TestConformance(t *testing.T) {
	t.Parallel()

	d, _ := local(t, "")
	storagetest.Run(t, d)
	storagetest.Run(t, storage.NewMemory(""))
}

func TestSecurity_A01_StorageTraversal(t *testing.T) {
	t.Parallel()

	d, dir := local(t, "")
	storagetest.RunTraversal(t, d)
	storagetest.RunTraversal(t, storage.NewMemory(""))

	// A symlink inside the disk cannot lead outside it.
	outside := filepath.Join(filepath.Dir(dir), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(outside, filepath.Join(dir, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err) // Windows without developer mode
	}

	if _, err := storage.Get(t.Context(), d, "link.txt"); err == nil {
		t.Fatal("followed a symlink out of the disk")
	}
}

// Dot directories (.git, .secrets) are as private as dotfiles, and served
// user content runs sandboxed so an uploaded HTML or XML file cannot
// script the app's origin.
func TestSecurity_A01_StorageHandlerHidesDotPaths(t *testing.T) {
	t.Parallel()

	d, _ := local(t, "/storage/")
	ctx := t.Context()

	for _, p := range []string{".git/config", "x/.secrets/key.txt", "page.html"} {
		_ = storage.PutBytes(ctx, d, p, []byte("<script>secret</script>"))
	}

	h := http.StripPrefix("/storage", storage.Handler(d, nil))
	get := func(target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
		h.ServeHTTP(
			rec,
			r.WithContext(web.WithServices(r.Context(), &web.Services{Log: slog.New(slog.DiscardHandler)})),
		)

		return rec
	}

	for _, target := range []string{"/storage/.git/config", "/storage/x/.secrets/key.txt"} {
		if rec := get(target); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", target, rec.Code)
		}
	}

	if rec := get("/storage/page.html"); rec.Code != 200 ||
		!strings.HasPrefix(rec.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Errorf(
			"GET page.html = %d CSP %q, want 200 with a sandbox policy",
			rec.Code,
			rec.Header().Get("Content-Security-Policy"),
		)
	}
}

func TestURLsAndHandler(t *testing.T) {
	t.Parallel()

	d, _ := local(t, "/storage/")
	ctx := t.Context()

	_ = storage.PutBytes(ctx, d, "avatars/a.png", []byte("\x89PNG\r\n\x1a\nfake"))
	_ = storage.PutBytes(ctx, d, ".env", []byte("SECRET=1"))

	if d.URL("avatars/a.png") != "/storage/avatars/a.png" || storage.NewMemory("").URL("x") != "" {
		t.Fatal("URL")
	}

	public := http.StripPrefix("/storage", storage.Handler(d, nil))

	get := func(h http.Handler, target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r := httptest.NewRequestWithContext(ctx, http.MethodGet, target, http.NoBody)
		h.ServeHTTP(
			rec,
			r.WithContext(web.WithServices(r.Context(), &web.Services{Log: slog.New(slog.DiscardHandler)})),
		)

		return rec
	}

	if rec := get(
		public,
		"/storage/avatars/a.png",
	); rec.Code != 200 ||
		rec.Header().Get("Content-Type") != "image/png" ||
		rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("public file = %d %v", rec.Code, rec.Header())
	}

	for _, target := range []string{"/storage/.env", "/storage/missing.png", "/storage/../go.mod", "/storage/avatars"} {
		if rec := get(public, target); rec.Code != 404 {
			t.Errorf("%s = %d, want 404", target, rec.Code)
		}
	}

	// Private files need a signed, unexpired URL.
	signer := signed.New([]byte("0123456789abcdef0123456789abcdef"))
	private := http.StripPrefix("/files", storage.Handler(d, signer))

	link, err := storage.TemporaryURL(signer, "/files", "avatars/a.png", time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	if rec := get(private, link); rec.Code != 200 {
		t.Fatalf("signed = %d", rec.Code)
	}

	if rec := get(private, "/files/avatars/a.png"); rec.Code != 403 {
		t.Fatalf("unsigned = %d", rec.Code)
	}

	if _, err := storage.TemporaryURL(signer, "/files", "../x", time.Minute); err == nil {
		t.Fatal("traversal signed")
	}

	// Memory disks stream without seeking.
	mem := storage.NewMemory("")
	_ = storage.PutBytes(ctx, mem, "notes.txt", []byte("hello"))

	if rec := get(
		storage.Handler(mem, nil),
		"/notes.txt",
	); rec.Body.String() != "hello" ||
		!strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("memory handler = %q %v", rec.Body.String(), rec.Header())
	}
}

func upload(t *testing.T, name string, data []byte) *multipart.FileHeader {
	t.Helper()

	var b bytes.Buffer

	mw := multipart.NewWriter(&b)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(data)
	_ = mw.Close()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", &b)
	r.Header.Set("Content-Type", mw.FormDataContentType())

	if err := r.ParseMultipartForm(1 << 20); err != nil {
		t.Fatal(err)
	}

	return r.MultipartForm.File["file"][0]
}

func TestPutUploaded(t *testing.T) {
	t.Parallel()

	d := storage.NewMemory("")
	ctx := t.Context()
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 100)...)

	p, err := storage.PutUploaded(ctx, d, upload(t, "../../evil.php", png), "avatars", storage.Images(1024))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(p, "avatars/") || !strings.HasSuffix(p, ".png") || strings.Contains(p, "evil") {
		t.Fatalf("stored as %q", p)
	}

	if got, _ := storage.Get(ctx, d, p); !bytes.Equal(got, png) {
		t.Fatal("content changed")
	}

	// A script renamed to .png is rejected by content sniffing.
	if _, err := storage.PutUploaded(
		ctx,
		d,
		upload(t, "photo.png", []byte("<?php echo 1; ?>")),
		"",
		storage.Images(1024),
	); errs.Status(
		err,
	) != 422 {
		t.Fatalf("fake image = %v", err)
	}

	if _, err := storage.PutUploaded(
		ctx,
		d,
		upload(t, "big.png", append(png, make([]byte, 2048)...)),
		"",
		storage.Images(1024),
	); errs.Status(
		err,
	) != 422 {
		t.Fatalf("too large = %v", err)
	}

	if _, err := storage.PutUploaded(ctx, d, upload(t, "a.png", png), "", storage.UploadRules{}); err == nil {
		t.Fatal("missing MaxSize accepted")
	}

	// Allowed types outside the extension table get .bin.
	if p, err := storage.PutUploaded(
		ctx,
		d,
		upload(t, "x.dat", []byte{0x00, 0x01, 0x02}),
		"",
		storage.UploadRules{MaxSize: 10, Types: []string{"application/octet-stream"}},
	); err != nil ||
		!strings.HasSuffix(p, ".bin") {
		t.Fatalf("binary = %q %v", p, err)
	}

	// A file larger than its declared size is caught while copying.
	fh := upload(t, "lie.png", png)
	fh.Size = 10

	if _, err := storage.PutUploaded(ctx, d, fh, "", storage.Images(64)); errs.Status(err) != 422 {
		t.Fatalf("under-reported size = %v", err)
	}
}

// PutUploaded without a type allowlist is a configuration error, not
// "accept anything": an empty Types must never store an HTML or script
// upload (fail closed).
func TestSecurity_A05_PutUploadedRequiresTypes(t *testing.T) {
	t.Parallel()

	d := storage.NewMemory("")

	_, err := storage.PutUploaded(
		t.Context(), d, upload(t, "x.html", []byte("<html><script>1</script>")), "", storage.UploadRules{MaxSize: 1024},
	)
	if err == nil {
		t.Fatal("PutUploaded(empty Types) = nil error, want a configuration error")
	}

	if all, _ := d.List(t.Context(), ""); len(all) != 0 {
		t.Errorf("PutUploaded(empty Types) stored %v", all)
	}
}

func TestHandlerLastModified(t *testing.T) {
	t.Parallel()

	d, dir := local(t, "")
	ctx := t.Context()
	modified := time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)

	_ = storage.PutBytes(ctx, d, "a.txt", []byte("hello"))
	if err := os.Chtimes(filepath.Join(dir, "a.txt"), modified, modified); err != nil {
		t.Fatal(err)
	}

	get := func(h http.Handler, ims string) *httptest.ResponseRecorder {
		r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/a.txt", http.NoBody)
		if ims != "" {
			r.Header.Set("If-Modified-Since", ims)
		}

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)

		return rec
	}

	h := storage.Handler(d, nil)
	if got, want := get(h, "").Header().Get("Last-Modified"), modified.Format(http.TimeFormat); got != want {
		t.Errorf("Last-Modified = %q, want %q", got, want)
	}

	if rec := get(h, modified.Format(http.TimeFormat)); rec.Code != http.StatusNotModified {
		t.Errorf("GET with If-Modified-Since = %d, want 304", rec.Code)
	}

	if rec := get(h, modified.Add(-time.Hour).Format(http.TimeFormat)); rec.Code != http.StatusOK {
		t.Errorf("GET with an older If-Modified-Since = %d, want 200", rec.Code)
	}

	mem := storage.NewMemory("")
	_ = storage.PutBytes(ctx, mem, "a.txt", []byte("hello"))

	if got := get(storage.Handler(mem, nil), "").Header().Get("Last-Modified"); got != "" {
		t.Errorf("memory Last-Modified = %q, want none (time unknown)", got)
	}
}

// observingReader runs check once, in the middle of a Put, while the
// temporary file exists.
type observingReader struct {
	check func()
	done  bool
}

func (o *observingReader) Read(p []byte) (int, error) {
	if o.done {
		return 0, io.EOF
	}

	o.check()
	o.done = true

	return copy(p, "partial"), nil
}

func TestLocalPutHidesTemporaryFile(t *testing.T) {
	t.Parallel()

	d, dir := local(t, "")
	ctx := t.Context()

	var (
		names  []string
		listed []string
		code   int
	)

	r := &observingReader{check: func() {
		entries, _ := os.ReadDir(filepath.Join(dir, "docs"))
		for _, e := range entries {
			names = append(names, e.Name())
		}

		listed, _ = d.List(ctx, "")

		for _, n := range names {
			rec := httptest.NewRecorder()
			storage.Handler(d, nil).
				ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/docs/"+n, http.NoBody))
			code = rec.Code
		}
	}}

	if err := d.Put(ctx, "docs/a.txt", r); err != nil {
		t.Fatal(err)
	}

	if len(names) != 1 || !strings.HasPrefix(names[0], ".") {
		t.Errorf("temporary files = %v, want one dot-prefixed name", names)
	}

	if len(listed) != 0 {
		t.Errorf("List during Put = %v, want none", listed)
	}

	if code != http.StatusNotFound {
		t.Errorf("GET temporary file = %d, want 404", code)
	}

	if all, _ := d.List(ctx, ""); !slices.Equal(all, []string{"docs/a.txt"}) {
		t.Errorf("List after Put = %v, want [docs/a.txt]", all)
	}
}

// A102: a disk had no health check, so an app wrote its own, and the
// natural one — does a known key exist? — is wrong: with the wrong
// credentials several S3 implementations answer "object does not exist"
// rather than failing, so the check reports healthy while nothing can be
// stored. Only a real write distinguishes the two.
func TestCheck(t *testing.T) {
	t.Parallel()

	disk := storage.NewMemory("https://cdn.example.com")

	if err := storage.Check(t.Context(), disk); err != nil {
		t.Fatalf("Check() on a working disk = %v", err)
	}

	// Nothing is left behind: a probe that leaks one object per run fills
	// the bucket it is meant to watch.
	files, err := disk.List(t.Context(), ".")
	if err != nil {
		t.Fatal(err)
	}

	if len(files) != 0 {
		t.Errorf("Check() left %v behind", files)
	}
}

func TestCheckFailsWhenTheDiskDoesNot(t *testing.T) {
	t.Parallel()

	for name, disk := range map[string]storage.Disk{
		// The case the existence check misses: writes are refused while a
		// lookup answers "not found" rather than "forbidden".
		"write refused":  &brokenDisk{put: errors.New("AccessDenied: key AKIAnotreal secret s3cr3t")},
		"read refused":   &brokenDisk{open: errors.New("AccessDenied")},
		"delete refused": &brokenDisk{del: errors.New("AccessDenied")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := storage.Check(t.Context(), disk)
			if err == nil {
				t.Fatal("Check() on a broken disk = nil")
			}

			// The driver error is classified, not forwarded: it may carry a
			// key or a secret, and a naive handler puts the error in the
			// response body.
			if !errors.Is(err, storage.ErrUnhealthy) {
				t.Errorf("Check() = %v, want ErrUnhealthy", err)
			}

			for _, secret := range []string{"AKIAnotreal", "s3cr3t"} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("Check() error leaks %q: %v", secret, err)
				}
			}
		})
	}
}

// A round trip that writes and reads back different bytes is a disk that
// is up and wrong, which a Put-only probe reports as healthy.
func TestCheckDetectsCorruptedRoundTrip(t *testing.T) {
	t.Parallel()

	err := storage.Check(t.Context(), &brokenDisk{body: []byte("not what was written")})
	if err == nil || !errors.Is(err, storage.ErrUnhealthy) {
		t.Fatalf("Check() on a disk returning other bytes = %v, want ErrUnhealthy", err)
	}
}

// brokenDisk fails the operation its field names, and otherwise behaves.
type brokenDisk struct {
	put, open, del error
	body           []byte
}

func (d *brokenDisk) Put(_ context.Context, _ string, r io.Reader) error {
	if d.put != nil {
		return d.put
	}

	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}

	if d.body == nil {
		d.body = b
	}

	return nil
}

func (d *brokenDisk) Open(_ context.Context, _ string) (io.ReadCloser, error) {
	if d.open != nil {
		return nil, d.open
	}

	return io.NopCloser(bytes.NewReader(d.body)), nil
}

func (d *brokenDisk) Exists(context.Context, string) (bool, error) { return d.body != nil, nil }

func (d *brokenDisk) Delete(context.Context, string) error {
	if d.del != nil {
		return d.del
	}

	d.body = nil

	return nil
}

func (*brokenDisk) List(context.Context, string) ([]string, error) { return nil, nil }

func (*brokenDisk) URL(string) string { return "" }
