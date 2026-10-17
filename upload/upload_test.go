package upload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"runtime"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/storage"
	"github.com/AlexGalhardo/vukem/web"
)

const (
	pngMagic = "\x89PNG\r\n\x1a\n"
	jpgMagic = "\xff\xd8\xff\xe0"
)

type part struct {
	field, filename, contentType, content string
}

func multipartBody(t *testing.T, parts ...part) (*bytes.Buffer, string) {
	t.Helper()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	for _, p := range parts {
		h := textproto.MIMEHeader{}
		if p.filename != "" {
			h.Set("Content-Disposition", `form-data; name="`+p.field+`"; filename="`+p.filename+`"`)
			h.Set("Content-Type", p.contentType)
		} else {
			h.Set("Content-Disposition", `form-data; name="`+p.field+`"`)
		}

		w, err := mw.CreatePart(h)
		if err != nil {
			t.Fatal(err)
		}

		_, _ = io.WriteString(w, p.content)
	}

	_ = mw.Close()

	return &buf, mw.FormDataContentType()
}

func receive(t *testing.T, d storage.Disk, rules Rules, parts ...part) (*Form, error) {
	t.Helper()

	body, ct := multipartBody(t, parts...)
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/upload", body)
	r.Header.Set("Content-Type", ct)

	return Receive(web.NewCtx(httptest.NewRecorder(), r, nil), d, rules)
}

func listAll(t *testing.T, d storage.Disk) []string {
	t.Helper()

	files, err := d.List(t.Context(), "")
	if err != nil {
		t.Fatal(err)
	}

	return files
}

func TestReceive(t *testing.T) {
	t.Parallel()

	disk := storage.NewMemory("/storage")
	rules := Images(1 << 20)
	rules.Dir = "avatars"
	content := pngMagic + strings.Repeat("x", 1000)

	form, err := receive(t, disk, rules,
		part{field: "title", content: "Me"},
		part{field: "avatar", filename: "Me.PNG", contentType: "image/png", content: content},
	)
	if err != nil {
		t.Fatalf("Receive() = %v", err)
	}

	if got := form.Values.Get("title"); got != "Me" {
		t.Errorf("Values[title] = %q, want Me", got)
	}

	if len(form.Files) != 1 {
		t.Fatalf("Files = %v, want one", form.Files)
	}

	f := form.Files[0]
	sum := sha256.Sum256([]byte(content))
	want := File{
		Field: "avatar", Name: "Me.PNG", Path: f.Path, Type: "image/png", Size: int64(len(content)),
		SHA256: hex.EncodeToString(sum[:]),
	}

	if diff := cmp.Diff(want, f); diff != "" {
		t.Errorf("File (-want +got):\n%s", diff)
	}

	if !strings.HasPrefix(f.Path, "avatars/") || !strings.HasSuffix(f.Path, ".png") || strings.Contains(f.Path, "Me") {
		t.Errorf("Path = %q, want avatars/<random>.png", f.Path)
	}

	if got, _ := storage.Get(t.Context(), disk, f.Path); string(got) != content {
		t.Errorf("stored %d bytes, want %d", len(got), len(content))
	}
}

func TestReceiveRequiresRules(t *testing.T) {
	t.Parallel()

	if _, err := receive(t, storage.NewMemory(""), Rules{}, part{field: "a", content: "b"}); !errors.Is(err, errRules) {
		t.Fatalf("Receive(zero Rules) = %v, want errRules", err)
	}

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", strings.NewReader("{}"))
	r.Header.Set("Content-Type", "application/json")

	_, err := Receive(web.NewCtx(httptest.NewRecorder(), r, nil), storage.NewMemory(""), Images(10))
	if errs.Status(err) != http.StatusUnsupportedMediaType {
		t.Fatalf("Receive(JSON) = %v, want 415", err)
	}
}

// discardDisk hashes what it is given and keeps nothing, so memory use
// during a large upload reflects Receive alone.
type discardDisk struct {
	storage.Memory

	n   int64
	sum string
}

func (d *discardDisk) Put(_ context.Context, _ string, r io.Reader) error {
	h := sha256.New()
	n, err := io.Copy(h, r)
	d.n, d.sum = n, hex.EncodeToString(h.Sum(nil))

	return err
}

// TestReceiveLargeFileBoundedMemory streams a 64 MiB upload generated on
// the fly and checks that Receive allocates a tiny fraction of it.
func TestReceiveLargeFileBoundedMemory(t *testing.T) { //nolint:paralleltest // measures process-wide allocations
	const size = 64 << 20

	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	want := sha256.New()

	go func() {
		w, err := mw.CreateFormFile("video", "big.png")
		if err != nil {
			_ = pw.CloseWithError(err)
			return
		}

		chunk := bytes.Repeat([]byte("vukem!!!"), 4096)
		copy(chunk, pngMagic)

		for written := 0; written < size; written += len(chunk) {
			_, _ = want.Write(chunk)
			if _, err := w.Write(chunk); err != nil {
				_ = pw.CloseWithError(err)
				return
			}
		}

		_ = pw.CloseWithError(mw.Close())
	}()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", pr)
	r.Header.Set("Content-Type", mw.FormDataContentType())

	disk := &discardDisk{}

	var before, after runtime.MemStats

	runtime.GC()
	runtime.ReadMemStats(&before)

	form, err := Receive(web.NewCtx(httptest.NewRecorder(), r, nil), disk, Images(size))

	runtime.ReadMemStats(&after)

	if err != nil {
		t.Fatalf("Receive() = %v", err)
	}

	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("uploaded %d MiB, allocated %d KiB", size>>20, allocated>>10)

	if allocated > 1<<20 {
		t.Errorf("Receive allocated %d bytes for a %d byte upload, want < 1 MiB", allocated, size)
	}

	if disk.n != size || form.Files[0].Size != size || form.Files[0].SHA256 != hex.EncodeToString(want.Sum(nil)) {
		t.Errorf("stored %d bytes (file %+v), want %d with the generated digest", disk.n, form.Files[0], size)
	}
}

func TestSecurity_A05_UploadValidation(t *testing.T) {
	t.Parallel()

	png := pngMagic + strings.Repeat("p", 600)

	tests := []struct {
		name       string
		rules      func(r *Rules)
		parts      []part
		wantStatus int
	}{
		{
			name: "spoofed content type",
			parts: []part{
				{field: "f", filename: "x.png", contentType: "image/png", content: "<html><script>alert(1)</script>"},
			},
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "extension does not match content",
			parts:      []part{{field: "f", filename: "x.jpg", contentType: "image/jpeg", content: png}},
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "extension not allowed",
			rules:      func(r *Rules) { r.Extensions = []string{"jpg"} },
			parts:      []part{{field: "f", filename: "x.png", contentType: "image/png", content: png}},
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "file over its limit",
			rules:      func(r *Rules) { r.MaxFileSize = 100 },
			parts:      []part{{field: "f", filename: "x.png", contentType: "image/png", content: png}},
			wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name:  "request over its limit",
			rules: func(r *Rules) { r.MaxFiles, r.MaxSize = 3, 1500 },
			parts: []part{
				{field: "f", filename: "a.png", contentType: "image/png", content: png},
				{field: "f", filename: "b.png", contentType: "image/png", content: png},
				{field: "f", filename: "c.png", contentType: "image/png", content: png},
			},
			wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name: "too many files",
			parts: []part{
				{field: "f", filename: "a.png", contentType: "image/png", content: png},
				{field: "f", filename: "b.png", contentType: "image/png", content: png},
			},
			wantStatus: http.StatusUnprocessableEntity,
		},
		{
			name:       "too many fields",
			rules:      func(r *Rules) { r.MaxFields = 1 },
			parts:      []part{{field: "a", content: "1"}, {field: "b", content: "2"}},
			wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name:       "field too large",
			rules:      func(r *Rules) { r.MaxFieldSize = 4 },
			parts:      []part{{field: "a", content: "12345"}},
			wantStatus: http.StatusRequestEntityTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			disk := storage.NewMemory("")
			rules := Images(10 << 10)
			rules.Dir = "up"

			if tt.rules != nil {
				tt.rules(&rules)
			}

			_, err := receive(t, disk, rules, tt.parts...)
			if got := errs.Status(err); got != tt.wantStatus {
				t.Fatalf("Receive() = %v (status %d), want %d", err, got, tt.wantStatus)
			}

			if files := listAll(t, disk); len(files) != 0 {
				t.Errorf("rejected upload left %v on the disk", files)
			}
		})
	}

	t.Run("path traversal in the file name", func(t *testing.T) {
		t.Parallel()

		disk := storage.NewMemory("")
		rules := Images(10 << 10)
		rules.Dir = "up"

		for _, name := range []string{"../../etc/passwd.png", `..\..\windows\x.png`, "/abs/x.png", "..png", "%2e%2e%2fx.png"} {
			form, err := receive(
				t,
				disk,
				rules,
				part{field: "f", filename: name, contentType: "image/png", content: png},
			)
			if err != nil {
				t.Fatalf("Receive(%q) = %v", name, err)
			}

			f := form.Files[0]
			if !strings.HasPrefix(f.Path, "up/") || strings.Count(f.Path, "/") != 1 || strings.Contains(f.Path, "..") {
				t.Errorf("Receive(%q) stored at %q, want up/<random>.png", name, f.Path)
			}

			if strings.ContainsAny(f.Name, "/\\\x00") || strings.HasPrefix(f.Name, ".") {
				t.Errorf("Receive(%q) Name = %q, want a sanitized base name", name, f.Name)
			}
		}
	})
}

func TestSanitizeName(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"photo.jpg":                       "photo.jpg",
		"../../etc/passwd":                "passwd",
		`C:\Users\me\cv.pdf`:              "cv.pdf",
		".htaccess":                       "htaccess",
		"..":                              "file",
		"":                                "file",
		"a\"b\r\n.txt":                    "ab.txt",
		strings.Repeat("é", 300) + ".png": strings.Repeat("é", 196) + ".png",
		// Bidi overrides and zero-width characters spoof the extension.
		"invoice\u202efdp.exe": "invoicefdp.exe",
		"a\u200bb\ufeff.png":   "ab.png",
	}

	for in, want := range tests {
		if got := SanitizeName(in); got != want {
			t.Errorf("SanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSaveDetectsTypeFromContent(t *testing.T) {
	t.Parallel()

	disk := storage.NewMemory("")
	rules := Rules{MaxFileSize: 1 << 10, Types: []string{"image/jpeg", "text/plain"}}

	f, err := Save(t.Context(), disk, rules, "doc", "notes.txt", strings.NewReader("hello"))
	if err != nil || f.Type != "text/plain" || !strings.HasSuffix(f.Path, ".txt") {
		t.Fatalf("Save(text) = %+v, %v", f, err)
	}

	f, err = Save(t.Context(), disk, rules, "img", "p.jpeg", strings.NewReader(jpgMagic+"data"))
	if err != nil || f.Type != "image/jpeg" || !strings.HasSuffix(f.Path, ".jpg") {
		t.Fatalf("Save(jpeg) = %+v, %v", f, err)
	}
}
