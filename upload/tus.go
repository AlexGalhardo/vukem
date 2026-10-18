package upload

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/web"
	"github.com/AlexGalhardo/vukem/z"
)

// tus protocol constants (https://tus.io/protocols/resumable-upload).
const (
	TusVersion       = "1.0.0"
	tusExtensions    = "creation,expiration,termination"
	tusContentType   = "application/offset+octet-stream"
	maxMetadataSize  = 4 << 10
	maxMetadataPairs = 32
	idLen            = 26 // rand.Text()
)

// TusInfo describes a resumable upload.
type TusInfo struct {
	ID       string            `json:"id"`
	Length   int64             `json:"length"`
	Offset   int64             `json:"-"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Created  time.Time         `json:"created"`
	Expires  time.Time         `json:"expires"`
}

// Filename returns the "filename" metadata value sent by the client
// (untrusted: pass it through Save, which sanitizes it).
func (i TusInfo) Filename() string { return i.Metadata["filename"] }

// TusConfig configures Tus.
type TusConfig struct {
	// Dir holds partial uploads (opened with os.Root; created if needed).
	// Keep it outside any publicly served directory.
	Dir string
	// BasePath is the URL path the handler is mounted at ("/uploads/tus/");
	// upload URLs are BasePath + id.
	BasePath string
	// MaxSize is the largest Upload-Length accepted (required).
	MaxSize int64
	// Authorize runs before every request except OPTIONS; an error denies
	// it (its status when it has one, 403 otherwise). Required: upload
	// endpoints are never open by accident.
	Authorize func(r *http.Request) error
	// OnComplete receives a finished upload. open reads its bytes (close
	// it before returning). It typically calls Save to validate the
	// content and move it to a disk. The partial data is deleted after it
	// returns; an error is sent to the client (422 for validation errors).
	OnComplete func(ctx context.Context, info TusInfo, open func() (io.ReadCloser, error)) error
	// Expiry is how long an unfinished upload is kept (default 24h).
	// Call Cleanup periodically (scheduler) to delete expired uploads.
	Expiry time.Duration
	// Now is the clock (default time.Now).
	Now func() time.Time
}

// TusHandler serves the tus 1.0 core protocol with the creation,
// expiration and termination extensions.
type TusHandler struct {
	cfg  TusConfig
	root *os.Root

	mu   sync.Mutex
	busy map[string]bool
}

// Tus returns a tus 1.0 handler; mount it at cfg.BasePath (a subtree
// pattern, "/uploads/tus/"). It is a boot error to omit MaxSize,
// Authorize, OnComplete, Dir or BasePath. Close it on shutdown.
func Tus(cfg TusConfig) (*TusHandler, error) {
	switch {
	case cfg.Dir == "" || cfg.BasePath == "":
		return nil, errors.New("upload: TusConfig needs Dir and BasePath")
	case cfg.MaxSize <= 0:
		return nil, errors.New("upload: TusConfig needs MaxSize > 0")
	case cfg.Authorize == nil:
		return nil, errors.New("upload: TusConfig needs Authorize (return nil to allow everyone)")
	case cfg.OnComplete == nil:
		return nil, errors.New("upload: TusConfig needs OnComplete")
	}

	if !strings.HasSuffix(cfg.BasePath, "/") {
		cfg.BasePath += "/"
	}

	if cfg.Expiry <= 0 {
		cfg.Expiry = 24 * time.Hour
	}

	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	if err := os.MkdirAll(cfg.Dir, 0o750); err != nil {
		return nil, fmt.Errorf("upload: %w", err)
	}

	root, err := os.OpenRoot(cfg.Dir)
	if err != nil {
		return nil, fmt.Errorf("upload: %w", err)
	}

	return &TusHandler{cfg: cfg, root: root, busy: map[string]bool{}}, nil
}

// Close releases the upload directory.
func (h *TusHandler) Close() error { return h.root.Close() }

// ServeHTTP implements http.Handler.
func (h *TusHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodOptions {
		w.Header().Set("Tus-Version", TusVersion)
		w.Header().Set("Tus-Max-Size", strconv.FormatInt(h.cfg.MaxSize, 10))
		w.Header().Set("Tus-Extension", tusExtensions)
		w.WriteHeader(http.StatusNoContent)

		return
	}

	w.Header().Set("Tus-Resumable", TusVersion)

	if r.Header.Get("Tus-Resumable") != TusVersion {
		w.Header().Set("Tus-Version", TusVersion)
		tusError(w, http.StatusPreconditionFailed, "")

		return
	}

	if err := h.cfg.Authorize(r); err != nil {
		status := errs.Status(err)
		if status < 400 || status >= 500 {
			status = http.StatusForbidden
		}

		tusError(w, status, "")

		return
	}

	id, ok := strings.CutPrefix(r.URL.Path, h.cfg.BasePath)
	if !ok || (id != "" && !validID(id)) {
		tusError(w, http.StatusNotFound, "")
		return
	}

	switch {
	case id == "" && r.Method == http.MethodPost:
		h.create(w, r)
	case id == "":
		w.Header().Set("Allow", "OPTIONS, POST")
		tusError(w, http.StatusMethodNotAllowed, "")
	case r.Method == http.MethodHead:
		h.head(w, r, id)
	case r.Method == http.MethodPatch:
		h.patch(w, r, id)
	case r.Method == http.MethodDelete:
		h.terminate(w, id)
	default:
		w.Header().Set("Allow", "OPTIONS, HEAD, PATCH, DELETE")
		tusError(w, http.StatusMethodNotAllowed, "")
	}
}

func (h *TusHandler) create(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Upload-Defer-Length") != "" {
		tusError(w, http.StatusBadRequest, "Upload-Defer-Length is not supported.")
		return
	}

	length, err := strconv.ParseInt(r.Header.Get("Upload-Length"), 10, 64)
	if err != nil || length < 0 {
		tusError(w, http.StatusBadRequest, "Upload-Length is required.")
		return
	}

	if length > h.cfg.MaxSize {
		tusError(w, http.StatusRequestEntityTooLarge, "")
		return
	}

	meta, err := parseMetadata(r.Header.Get("Upload-Metadata"))
	if err != nil {
		tusError(w, http.StatusBadRequest, "Upload-Metadata is invalid.")
		return
	}

	now := h.cfg.Now()
	info := TusInfo{
		ID:       strings.ToLower(rand.Text()),
		Length:   length,
		Metadata: meta,
		Created:  now,
		Expires:  now.Add(h.cfg.Expiry),
	}

	if err := h.write(info); err != nil {
		tusError(w, http.StatusInternalServerError, "")
		return
	}

	w.Header().Set("Location", h.cfg.BasePath+info.ID)
	w.Header().Set("Upload-Expires", info.Expires.UTC().Format(http.TimeFormat))
	w.WriteHeader(http.StatusCreated)
}

func (h *TusHandler) write(info TusInfo) error {
	b, err := json.Marshal(info)
	if err != nil {
		return err
	}

	f, err := h.root.OpenFile(info.ID+".bin", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	if err := h.root.WriteFile(info.ID+".json", b, 0o640); err != nil {
		_ = h.root.Remove(info.ID + ".bin")
		return err
	}

	return nil
}

// load returns the upload's info with its current offset; expired
// uploads are deleted and reported missing.
func (h *TusHandler) load(id string) (TusInfo, error) {
	b, err := h.root.ReadFile(id + ".json")
	if err != nil {
		return TusInfo{}, err
	}

	var info TusInfo
	if err := json.Unmarshal(b, &info); err != nil {
		return TusInfo{}, err
	}

	if !h.cfg.Now().Before(info.Expires) {
		h.remove(id)
		return TusInfo{}, fs.ErrNotExist
	}

	st, err := h.root.Stat(id + ".bin")
	if err != nil {
		return TusInfo{}, err
	}

	info.Offset = st.Size()

	return info, nil
}

// remove deletes the data first and the info last, so a failed removal
// (an open file on Windows) is retried by Cleanup.
func (h *TusHandler) remove(id string) {
	if err := h.root.Remove(id + ".bin"); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return
	}

	_ = h.root.Remove(id + ".json")
}

func (h *TusHandler) head(w http.ResponseWriter, _ *http.Request, id string) {
	info, err := h.load(id)
	if err != nil {
		tusError(w, http.StatusNotFound, "")
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Upload-Offset", strconv.FormatInt(info.Offset, 10))
	w.Header().Set("Upload-Length", strconv.FormatInt(info.Length, 10))
	w.Header().Set("Upload-Expires", info.Expires.UTC().Format(http.TimeFormat))

	if m := encodeMetadata(info.Metadata); m != "" {
		w.Header().Set("Upload-Metadata", m)
	}

	w.WriteHeader(http.StatusOK)
}

// lock marks id busy; concurrent PATCH requests on one upload get 423.
func (h *TusHandler) lock(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.busy[id] {
		return false
	}

	h.busy[id] = true

	return true
}

func (h *TusHandler) unlock(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.busy, id)
}

func (h *TusHandler) patch(w http.ResponseWriter, r *http.Request, id string) {
	if mediaType(r.Header.Get("Content-Type")) != tusContentType {
		tusError(w, http.StatusUnsupportedMediaType, "Content-Type must be "+tusContentType+".")
		return
	}

	offset, err := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
	if err != nil || offset < 0 {
		tusError(w, http.StatusBadRequest, "Upload-Offset is required.")
		return
	}

	if !h.lock(id) {
		tusError(w, http.StatusLocked, "")
		return
	}
	defer h.unlock(id)

	info, err := h.load(id)
	if err != nil {
		tusError(w, http.StatusNotFound, "")
		return
	}

	if offset != info.Offset {
		w.Header().Set("Upload-Offset", strconv.FormatInt(info.Offset, 10))
		tusError(w, http.StatusConflict, "Upload-Offset does not match the upload.")

		return
	}

	remaining := info.Length - info.Offset
	if r.ContentLength > remaining {
		tusError(w, http.StatusRequestEntityTooLarge, "")
		return
	}

	r = web.LimitBody(w, r, remaining)

	f, err := h.root.OpenFile(id+".bin", os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		tusError(w, http.StatusInternalServerError, "")
		return
	}

	// An interrupted body keeps what arrived: the client resumes from the
	// offset HEAD reports.
	n, copyErr := io.Copy(f, r.Body)
	closeErr := f.Close()
	info.Offset += n

	w.Header().Set("Upload-Offset", strconv.FormatInt(info.Offset, 10))

	switch {
	case closeErr != nil:
		tusError(w, http.StatusInternalServerError, "")
		return
	case copyErr != nil:
		if _, tooLarge := errors.AsType[*http.MaxBytesError](copyErr); tooLarge {
			tusError(w, http.StatusRequestEntityTooLarge, "")
			return
		}

		tusError(w, http.StatusBadRequest, "The request body was interrupted.")

		return
	}

	if info.Offset == info.Length {
		if err := h.complete(r.Context(), info); err != nil {
			status := errs.Status(err)
			if status >= http.StatusInternalServerError {
				logger(r.Context()).ErrorContext(r.Context(), "upload: tus completion failed",
					"id", info.ID, "status", status, "err", err)
			}

			tusError(w, status, clientMessage(err, status))

			return
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// logger is the request's logger (web.Services) or slog.Default.
func logger(ctx context.Context) *slog.Logger {
	if log := web.ServicesFrom(ctx).Log; log != nil {
		return log
	}

	return slog.Default()
}

func (h *TusHandler) complete(ctx context.Context, info TusInfo) error {
	defer h.remove(info.ID)

	return h.cfg.OnComplete(ctx, info, func() (io.ReadCloser, error) { return h.root.Open(info.ID + ".bin") })
}

func (h *TusHandler) terminate(w http.ResponseWriter, id string) {
	if !h.lock(id) {
		tusError(w, http.StatusLocked, "")
		return
	}
	defer h.unlock(id)

	if _, err := h.load(id); err != nil {
		tusError(w, http.StatusNotFound, "")
		return
	}

	h.remove(id)
	w.WriteHeader(http.StatusNoContent)
}

// Cleanup deletes expired uploads and orphaned data older than Expiry,
// returning how many uploads it removed. Schedule it (hourly is plenty).
func (h *TusHandler) Cleanup(ctx context.Context) (int, error) {
	entries, err := fs.ReadDir(h.root.FS(), ".")
	if err != nil {
		return 0, err
	}

	now := h.cfg.Now()
	removed := 0

	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return removed, err
		}

		id, ok := strings.CutSuffix(e.Name(), ".bin")
		if !ok || !validID(id) || !h.lock(id) {
			continue
		}

		expired := false

		if b, err := h.root.ReadFile(id + ".json"); err == nil {
			var info TusInfo
			expired = json.Unmarshal(b, &info) != nil || !now.Before(info.Expires)
		} else if st, err := e.Info(); err == nil {
			expired = now.Sub(st.ModTime()) > h.cfg.Expiry
		}

		if expired {
			h.remove(id)
			removed++
		}

		h.unlock(id)
	}

	return removed, nil
}

func validID(id string) bool {
	if len(id) != idLen {
		return false
	}

	for _, c := range id {
		if (c < 'a' || c > 'z') && (c < '2' || c > '7') {
			return false
		}
	}

	return true
}

// parseMetadata reads "key base64,key2 base64" (values optional).
func parseMetadata(s string) (map[string]string, error) {
	if s == "" {
		return nil, nil //nolint:nilnil // no metadata is not an error
	}

	if len(s) > maxMetadataSize {
		return nil, errors.New("metadata too large")
	}

	pairs := strings.Split(s, ",")
	if len(pairs) > maxMetadataPairs {
		return nil, errors.New("too many metadata pairs")
	}

	out := make(map[string]string, len(pairs))

	for _, p := range pairs {
		key, val, _ := strings.Cut(strings.TrimSpace(p), " ")
		if key == "" || strings.ContainsFunc(key, func(r rune) bool { return r <= ' ' || r > '~' }) {
			return nil, errors.New("invalid metadata key")
		}

		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(val))
		if err != nil || !utf8.Valid(b) {
			return nil, errors.New("invalid metadata value")
		}

		out[key] = string(b)
	}

	return out, nil
}

func encodeMetadata(m map[string]string) string {
	parts := make([]string, 0, len(m))
	for _, k := range sortedKeys(m) {
		parts = append(parts, k+" "+base64.StdEncoding.EncodeToString([]byte(m[k])))
	}

	return strings.Join(parts, ",")
}

func tusError(w http.ResponseWriter, status int, msg string) {
	if msg == "" {
		msg = http.StatusText(status)
	}

	http.Error(w, msg, status)
}

// clientMessage returns the client-safe text of err.
func clientMessage(err error, status int) string {
	if he, ok := errors.AsType[*errs.HTTPError](err); ok {
		return he.Text()
	}

	if ve, ok := errors.AsType[*errs.ValidationError](err); ok {
		for _, k := range sortedKeys(ve.Fields) {
			return ve.First(k)
		}
	}

	if is, ok := z.AsIssues(err); ok && status == http.StatusUnprocessableEntity && len(is) > 0 {
		return is[0].Message
	}

	return http.StatusText(status)
}
