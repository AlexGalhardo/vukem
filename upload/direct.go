package upload

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/storage"
	"github.com/AlexGalhardo/vukem/web"
)

// Presigner issues URLs that let a client upload one object straight to
// storage. The signature must bind the key, the content type and the
// maximum size, and expire after ttl. drivers/s3's PresignPut signs the
// key and expiry only, so an adapter over it must add the size and type
// conditions (or rely on Verify, which runs after the upload).
type Presigner interface {
	PresignPut(
		ctx context.Context,
		key, contentType string,
		maxSize int64,
		ttl time.Duration,
	) (url string, headers http.Header, err error)
}

// Presigned is a direct upload the client performs itself.
type Presigned struct {
	// Key is the object's path on the disk (random name under Rules.Dir).
	Key string `json:"key"`
	// URL, Method and Headers describe the request to send.
	URL     string            `json:"url"`
	Method  string            `json:"method"`
	Headers map[string]string `json:"headers,omitempty"`
	// ExpiresAt is when the URL stops working.
	ExpiresAt time.Time `json:"expires_at"`
}

// Defaults for presigned uploads.
const (
	DefaultPresignTTL = 15 * time.Minute
	maxPresignTTL     = time.Hour
	maxPresignBody    = 4 << 10
)

// Presign checks a planned upload against rules (declared type allowed,
// extension matching it, 0 < size <= MaxFileSize) and returns a presigned
// PUT for a fresh random key. The client declares the type, so call
// Verify once the client reports the upload done: it sniffs the stored
// content and deletes objects that break the rules.
func Presign(
	ctx context.Context,
	p Presigner,
	rules Rules,
	filename, contentType string,
	size int64,
	ttl time.Duration,
) (Presigned, error) {
	r, err := rules.normalized()
	if err != nil {
		return Presigned{}, err
	}

	if ttl <= 0 {
		ttl = DefaultPresignTTL
	}

	ttl = min(ttl, maxPresignTTL)

	if size <= 0 {
		return Presigned{}, errs.NewValidation().Add("size", "The file size is required.")
	}

	if size > r.MaxFileSize {
		return Presigned{}, ErrFileTooLarge
	}

	typ, ext, err := r.matchType("file", mediaType(contentType), strings.ToLower(path.Ext(SanitizeName(filename))))
	if err != nil {
		return Presigned{}, err
	}

	key := r.storedPath(ext)

	url, headers, err := p.PresignPut(ctx, key, typ, size, ttl)
	if err != nil {
		return Presigned{}, fmt.Errorf("upload: presign: %w", err)
	}

	out := Presigned{Key: key, URL: url, Method: http.MethodPut, ExpiresAt: time.Now().Add(ttl)}
	if len(headers) > 0 {
		out.Headers = make(map[string]string, len(headers))
		for k := range headers {
			out.Headers[k] = headers.Get(k)
		}
	}

	return out, nil
}

// DirectConfig configures Direct.
type DirectConfig struct {
	Presigner Presigner
	Rules     Rules
	// TTL of the presigned URLs (default 15 minutes, at most 1 hour).
	TTL time.Duration
	// Authorize runs first; an error denies the request (required).
	Authorize func(r *http.Request) error
}

type presignRequest struct {
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
}

// Direct returns a handler for POST {"filename", "content_type", "size"}
// that answers with a Presigned upload as JSON. Missing Presigner or
// Authorize, or unusable Rules, are boot errors.
func Direct(cfg DirectConfig) (http.Handler, error) {
	if cfg.Presigner == nil || cfg.Authorize == nil {
		return nil, errors.New("upload: DirectConfig needs Presigner and Authorize")
	}

	if _, err := cfg.Rules.normalized(); err != nil {
		return nil, err
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			web.Fail(w, r, errs.ErrMethodNotAllowed.WithHeader("Allow", http.MethodPost))
			return
		}

		if err := cfg.Authorize(r); err != nil {
			if errs.Status(err) >= 500 {
				err = errs.ErrForbidden
			}

			web.Fail(w, r, err)

			return
		}

		var in presignRequest

		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxPresignBody))
		dec.DisallowUnknownFields()

		if err := dec.Decode(&in); err != nil {
			web.Fail(w, r, errs.New(http.StatusBadRequest, "Send filename, content_type and size as JSON."))
			return
		}

		out, err := Presign(r.Context(), cfg.Presigner, cfg.Rules, in.Filename, in.ContentType, in.Size, cfg.TTL)
		if err != nil {
			web.Fail(w, r, err)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if err := json.NewEncoder(w).Encode(out); err != nil {
			if log := web.ServicesFrom(r.Context()).Log; log != nil {
				log.DebugContext(r.Context(), "upload: write presign response", "err", err) // client gone
			}
		}
	}), nil
}

// Verify checks an object already on d (a finished direct upload) against
// rules: detected type, extension of key, size and, for images, the
// declared dimensions (Rules.MaxPixels). Only keys Presign could
// have issued for rules are accepted (400 otherwise, without touching the
// disk). It deletes the object when it fails and returns its File
// description otherwise.
func Verify(ctx context.Context, d storage.Disk, key string, rules Rules) (File, error) {
	r, err := rules.normalized()
	if err != nil {
		return File{}, err
	}

	if !r.issued(key) {
		return File{}, errInvalidKey
	}

	f, err := d.Open(ctx, key)
	if err != nil {
		return File{}, err
	}

	head := make([]byte, sniffLen)
	n, _ := io.ReadFull(f, head)
	head = head[:n]

	sum := sha256.New()
	body := &capReader{r: io.MultiReader(bytes.NewReader(head), f), left: r.MaxFileSize}
	_, copyErr := io.Copy(sum, body)
	_ = f.Close()

	typ, _, matchErr := r.match("file", key, head)

	switch {
	case body.tooLarge:
		err = ErrFileTooLarge
	case copyErr != nil:
		return File{}, fmt.Errorf("upload: verify %s: %w", key, copyErr)
	default:
		err = matchErr
	}

	if err == nil {
		err = checkImage(ctx, d, key, typ, "file", r.MaxPixels)
	}

	if err != nil {
		_ = d.Delete(context.WithoutCancel(ctx), key)
		return File{}, err
	}

	return File{
		Field:  "file",
		Name:   path.Base(key),
		Path:   key,
		Type:   typ,
		Size:   r.MaxFileSize - body.left,
		SHA256: hex.EncodeToString(sum.Sum(nil)),
	}, nil
}

// errInvalidKey rejects a Verify key that Presign could not have issued.
var errInvalidKey = errs.New(http.StatusBadRequest, "The upload key is invalid.")

// issued reports whether key has the shape of a key Presign issues for r
// (storedPath: a random lower-case name and a known extension under
// r.Dir), so a client-reported key never opens or deletes other objects.
// Keys are random, not bound to a caller: apps that share Rules.Dir between
// users must also check that the key was presigned for this caller.
func (r Rules) issued(key string) bool {
	dir, name := path.Split(key)
	if dir != "" {
		dir = strings.TrimSuffix(dir, "/")
	}

	if dir != strings.TrimSuffix(r.Dir, "/") {
		return false
	}

	id, ext, ok := strings.Cut(name, ".")

	return ok && validID(id) && slices.ContainsFunc(slices.Collect(maps.Values(extensions)), func(exts []string) bool {
		return slices.Contains(exts, "."+ext)
	})
}

func sortedKeys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }
