package storage

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/signed"
	"github.com/AlexGalhardo/vukem/web"
)

// Handler serves a disk's files under a URL prefix (mount it with
// http.StripPrefix). Directory listings, dotfiles and files under dot
// directories are never served; responses carry a sandbox CSP.
// Pass a Signer to require signed URLs (private files, TemporaryURL).
func Handler(d Disk, signer *signed.Signer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if signer != nil {
			if err := signer.Verify(original(r)); err != nil {
				web.Fail(w, r, err)
				return
			}
		}

		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "." || strings.HasPrefix(p, ".") || strings.Contains(p, "/.") {
			// Dot directories (.git, quarantine) are as private as dotfiles.
			http.NotFound(w, r)
			return
		}

		f, err := d.Open(r.Context(), p)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) || errors.Is(err, ErrInvalidPath) {
				http.NotFound(w, r)
				return
			}

			web.Fail(w, r, err)

			return
		}
		defer f.Close()

		w.Header().Set("X-Content-Type-Options", "nosniff")
		// Stored files are often user content: an HTML or XML upload must
		// not run script on the app's origin. Images and downloads are
		// unaffected.
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self'; style-src 'unsafe-inline'")

		if rs, ok := f.(io.ReadSeeker); ok {
			http.ServeContent(w, r, path.Base(p), modTimeNow(), rs)
			return
		}

		if ct := mime.TypeByExtension(path.Ext(p)); ct != "" {
			w.Header().Set("Content-Type", ct)
		}

		_, _ = io.Copy(w, f)
	})
}

// original undoes http.StripPrefix for signature checks: links are signed
// with their full path.
func original(r *http.Request) *http.Request {
	if r.RequestURI == "" {
		return r
	}

	u, err := url.ParseRequestURI(r.RequestURI)
	if err != nil {
		return r
	}

	c := *r
	c.URL = u

	return &c
}

// TemporaryURL signs a link to p, served by Handler(disk, signer) mounted
// at prefix, valid for ttl.
func TemporaryURL(signer *signed.Signer, prefix, p string, ttl time.Duration) (string, error) {
	c, err := Clean(p)
	if err != nil {
		return "", err
	}

	return signer.Sign(strings.TrimSuffix(prefix, "/")+"/"+c, ttl)
}

// UploadRules restrict PutUploaded.
type UploadRules struct {
	// MaxSize in bytes (required).
	MaxSize int64
	// Types are allowed MIME types, detected from the content (not the
	// client's Content-Type or file name).
	Types []string
}

// Images allows JPEG, PNG, GIF and WebP up to maxSize bytes.
func Images(maxSize int64) UploadRules {
	return UploadRules{MaxSize: maxSize, Types: []string{"image/jpeg", "image/png", "image/gif", "image/webp"}}
}

var extensions = map[string]string{ //nolint:gochecknoglobals // read-only lookup table
	"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif", "image/webp": ".webp",
	"application/pdf": ".pdf", "text/plain; charset=utf-8": ".txt",
}

// PutUploaded stores an uploaded file under dir with a random name and an
// extension derived from the detected type, after checking its size and
// type. It returns the stored path. The client's file name is never used.
func PutUploaded(ctx context.Context, d Disk, fh *multipart.FileHeader, dir string, rules UploadRules) (string, error) {
	if rules.MaxSize <= 0 {
		return "", errors.New("storage: UploadRules.MaxSize is required")
	}

	if fh.Size > rules.MaxSize {
		return "", errs.NewValidation().
			Add("file", fmt.Sprintf("The file may not be larger than %d KB.", rules.MaxSize/1024))
	}

	f, err := fh.Open()
	if err != nil {
		return "", err
	}
	defer f.Close()

	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	head = head[:n]

	ct := http.DetectContentType(head)
	if len(rules.Types) > 0 && !slices.Contains(rules.Types, ct) {
		return "", errs.NewValidation().Add("file", "This file type is not allowed.")
	}

	ext := extensions[ct]
	if ext == "" {
		ext = ".bin"
	}

	name := strings.ToLower(rand.Text()) + ext
	if dir != "" {
		name = strings.TrimSuffix(dir, "/") + "/" + name
	}

	body := &capReader{r: io.MultiReader(strings.NewReader(string(head)), f), left: rules.MaxSize}

	if err := d.Put(ctx, name, body); err != nil {
		return "", err
	}

	return name, nil
}

// errTooLarge stops a write whose content is larger than its declared size.
var errTooLarge = errs.NewValidation().Add("file", "The file is too large.")

// capReader fails once more than left bytes were read, so a client that
// under-reports the size cannot store a bigger file.
type capReader struct {
	r    io.Reader
	left int64
}

func (c *capReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.left -= int64(n)

	if c.left < 0 {
		return n, errTooLarge
	}

	return n, err
}
