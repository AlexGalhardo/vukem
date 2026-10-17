// Package s3 is a storage.Disk on Amazon S3 or any S3-compatible service
// (Cloudflare R2, Backblaze B2, Wasabi, RustFS...). It speaks the REST API
// directly with a standard-library AWS Signature Version 4 client: no AWS
// SDK.
//
//	cfg, err := config.Load[s3.Config](app.Source) // S3_BUCKET, S3_REGION, ...
//	disk, err := s3.New(cfg)
//	err = storage.PutBytes(ctx, disk, "avatars/1.png", png)
//	link, err := disk.PresignGet("invoices/42.pdf", 15*time.Minute)
//
// Keys are storage paths: relative, slash-separated, validated by
// storage.Clean (no empty key, no leading "/", no "..", no backslash or
// ':'), so "a/b.txt" and "/a/b.txt" can never name different objects.
//
// Endpoints must use https; a plain http endpoint (a local server) is refused
// unless Config.InsecureHTTP is set. Credentials are config.Secret values
// and never appear in errors, logs or fmt output.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/subsystems.md.
package s3

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"mime"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/storage"
)

// Limits from the S3 API.
const (
	MinPartSize     = 5 << 20 // smallest multipart part (except the last)
	MaxPartSize     = 5 << 30
	DefaultPartSize = 8 << 20
	maxParts        = 10_000
	// MaxPresignExpiry is the longest validity SigV4 allows for a
	// presigned URL.
	MaxPresignExpiry = 7 * 24 * time.Hour
	maxErrorBody     = 64 << 10
	maxListBody      = 16 << 20
)

// Errors returned (wrapped) by New and the presign methods.
var (
	ErrConfig        = errors.New("s3: invalid configuration")
	ErrInvalidExpiry = errors.New("s3: presign expiry must be between 1s and 7 days")
	// ErrInvalidPresign reports a PresignPutOptions value S3 cannot sign.
	ErrInvalidPresign = errors.New("s3: invalid presign options")
)

// Config is the S3 connection; load it with config.Load[s3.Config].
type Config struct {
	Bucket string `env:"S3_BUCKET"`
	Region string `env:"S3_REGION" default:"us-east-1"`
	// Endpoint is the service origin ("" = https://s3.<region>.amazonaws.com),
	// e.g. https://<account>.r2.cloudflarestorage.com or http://localhost:9000.
	Endpoint        string        `env:"S3_ENDPOINT"`
	AccessKeyID     string        `env:"S3_ACCESS_KEY_ID"`
	SecretAccessKey config.Secret `env:"S3_SECRET_ACCESS_KEY"`
	// SessionToken is set for temporary (STS) credentials.
	SessionToken config.Secret `env:"S3_SESSION_TOKEN"`
	// UsePathStyle addresses endpoint/bucket/key instead of
	// bucket.endpoint/key (most self-hosted services).
	UsePathStyle bool `env:"S3_USE_PATH_STYLE"`
	// InsecureHTTP allows an http:// endpoint; only for local development.
	InsecureHTTP bool `env:"S3_INSECURE_HTTP"`
	// PartSize is the multipart part size in bytes (0 = DefaultPartSize);
	// bodies larger than one part are uploaded in parts.
	PartSize int64 `env:"S3_PART_SIZE"`
	// PublicURL prefixes Disk.URL (a CDN or public bucket origin); "" keeps
	// the disk private.
	PublicURL string `env:"S3_PUBLIC_URL"`
}

// Option customizes a Disk.
type Option func(*Disk)

// WithHTTPClient sets the HTTP client (proxies, custom TLS roots, tests).
func WithHTTPClient(c *http.Client) Option { return func(d *Disk) { d.client = c } }

// WithClock sets the clock used for signatures (tests).
func WithClock(now func() time.Time) Option { return func(d *Disk) { d.now = now } }

// Disk is an S3 bucket implementing storage.Disk. It is safe for
// concurrent use.
type Disk struct {
	bucket    string
	scheme    string
	host      string
	pathStyle bool
	partSize  int64
	publicURL string
	maxKeys   int
	// A pointer: fmt prints nested pointers as addresses, so %+v of a
	// Disk cannot reach the secret through unexported fields.
	signer *signer
	client *http.Client
	now    func() time.Time
}

var _ storage.Disk = (*Disk)(nil)

// New validates cfg and returns the disk. It does no I/O.
func New(cfg Config, opts ...Option) (*Disk, error) {
	if err := validBucket(cfg.Bucket); err != nil {
		return nil, err
	}

	if cfg.Region == "" {
		return nil, fmt.Errorf("%w: S3_REGION is empty", ErrConfig)
	}

	if cfg.AccessKeyID == "" || cfg.SecretAccessKey.IsZero() {
		return nil, fmt.Errorf("%w: S3_ACCESS_KEY_ID and S3_SECRET_ACCESS_KEY are required", ErrConfig)
	}

	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = "https://s3." + cfg.Region + ".amazonaws.com"
	}

	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
		strings.Trim(u.Path, "/") != "" {
		return nil, fmt.Errorf("%w: S3_ENDPOINT must be a bare origin such as https://s3.example.com", ErrConfig)
	}

	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && cfg.InsecureHTTP:
	case u.Scheme == "http":
		return nil, fmt.Errorf("%w: http endpoint refused; set S3_INSECURE_HTTP=true for local development", ErrConfig)
	default:
		return nil, fmt.Errorf("%w: S3_ENDPOINT scheme must be https", ErrConfig)
	}

	partSize := cfg.PartSize
	if partSize == 0 {
		partSize = DefaultPartSize
	}

	if partSize < MinPartSize || partSize > MaxPartSize {
		return nil, fmt.Errorf("%w: S3_PART_SIZE must be between 5 MiB and 5 GiB", ErrConfig)
	}

	d := &Disk{
		bucket:    cfg.Bucket,
		scheme:    u.Scheme,
		host:      u.Host,
		pathStyle: cfg.UsePathStyle,
		partSize:  partSize,
		publicURL: strings.TrimSuffix(cfg.PublicURL, "/"),
		maxKeys:   1000,
		signer: &signer{
			accessKey: cfg.AccessKeyID,
			secret:    cfg.SecretAccessKey,
			token:     cfg.SessionToken,
			region:    cfg.Region,
			service:   "s3",
		},
		client: &http.Client{Transport: defaultTransport()},
		now:    time.Now,
	}

	for _, o := range opts {
		o(d)
	}

	return d, nil
}

// String describes the disk without credentials.
func (d *Disk) String() string { return "s3://" + d.bucket }

func defaultTransport() *http.Transport {
	t, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		t = &http.Transport{Proxy: http.ProxyFromEnvironment, ForceAttemptHTTP2: true}
	}

	t = t.Clone()
	// No total timeout: downloads stream for as long as the context allows.
	t.ResponseHeaderTimeout = time.Minute

	return t
}

// validBucket accepts DNS-compatible bucket names, so a bucket can never
// inject a host or path.
func validBucket(b string) error {
	if len(b) < 3 || len(b) > 63 {
		return fmt.Errorf("%w: S3_BUCKET must be 3-63 characters", ErrConfig)
	}

	for i := range len(b) {
		c := b[i]
		if !isAlnum(c) && c != '-' && c != '.' {
			return fmt.Errorf("%w: S3_BUCKET may only contain a-z, 0-9, '-' and '.'", ErrConfig)
		}
	}

	if !isAlnum(b[0]) || !isAlnum(b[len(b)-1]) || strings.Contains(b, "..") {
		return fmt.Errorf("%w: S3_BUCKET must start and end with a letter or digit", ErrConfig)
	}

	return nil
}

func isAlnum(c byte) bool { return 'a' <= c && c <= 'z' || '0' <= c && c <= '9' }

// objectURL is the URL of key ("" = the bucket itself) with query q.
func (d *Disk) objectURL(key string, q url.Values) *url.URL {
	u := &url.URL{Scheme: d.scheme, Host: d.host, Path: "/" + key}
	if d.pathStyle {
		u.Path = "/" + d.bucket + "/" + key
	} else {
		u.Host = d.bucket + "." + d.host
	}

	if key == "" && d.pathStyle {
		u.Path = "/" + d.bucket
	}

	u.RawPath = encodePath(u.Path)

	if len(q) > 0 {
		u.RawQuery = canonicalQuery(q)
	}

	return u
}

// do signs and sends one request. body is nil or fully buffered so its
// SHA-256 is signed.
func (d *Disk) do(
	ctx context.Context,
	method, key string,
	q url.Values,
	h http.Header,
	body []byte,
) (*http.Response, error) {
	u := d.objectURL(key, q)

	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	if body == nil {
		req.Body = http.NoBody
	}

	maps.Copy(req.Header, h)

	hash := emptySHA256
	if len(body) > 0 {
		hash = sha256Hex(body)
	}

	req.Header.Set("X-Amz-Content-Sha256", hash)
	d.signer.sign(req, hash, d.now())

	resp, err := d.client.Do(req)
	if err != nil {
		// *url.Error carries the URL only: signatures travel in headers.
		return nil, err
	}

	return resp, nil
}

// Error is an S3 error response.
type Error struct {
	Op         string
	Key        string
	StatusCode int
	Code       string
	Message    string
	RequestID  string
}

// Error describes the failure without the request URL or credentials.
func (e *Error) Error() string {
	msg := fmt.Sprintf("s3: %s %s: %d", e.Op, e.Key, e.StatusCode)
	if e.Code != "" {
		msg += " " + e.Code
	}

	if e.Message != "" {
		msg += ": " + e.Message
	}

	return msg
}

// Unwrap makes missing objects match fs.ErrNotExist (storage.ErrNotFound).
func (e *Error) Unwrap() error {
	if e.StatusCode == http.StatusNotFound && (e.Code == "" || e.Code == "NoSuchKey") {
		return fs.ErrNotExist
	}

	return nil
}

type errorBody struct {
	Code      string `xml:"Code"`
	Message   string `xml:"Message"`
	RequestID string `xml:"RequestId"`
}

// readError consumes resp into an *Error.
func readError(op, key string, resp *http.Response) error {
	defer resp.Body.Close()

	e := &Error{Op: op, Key: key, StatusCode: resp.StatusCode}

	b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))

	var eb errorBody
	if xml.Unmarshal(b, &eb) == nil {
		e.Code, e.Message, e.RequestID = eb.Code, eb.Message, eb.RequestID
	}

	if e.RequestID == "" {
		e.RequestID = resp.Header.Get("X-Amz-Request-Id")
	}

	return e
}

// Put implements storage.Disk. Bodies up to PartSize are sent in one
// request; larger ones become a multipart upload (aborted on failure), so
// memory stays bounded by one part.
func (d *Disk) Put(ctx context.Context, p string, r io.Reader) error {
	key, err := storage.Clean(p)
	if err != nil {
		return err
	}

	var buf bytes.Buffer

	n, err := io.CopyN(&buf, r, d.partSize)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("s3: put %s: read: %w", key, err)
	}

	ctype := contentType(key, buf.Bytes())

	if n < d.partSize {
		return d.putObject(ctx, key, ctype, buf.Bytes())
	}

	var one [1]byte

	m, err := io.ReadFull(r, one[:])
	if m == 0 {
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("s3: put %s: read: %w", key, err)
		}

		return d.putObject(ctx, key, ctype, buf.Bytes())
	}

	return d.multipart(ctx, key, ctype, &buf, io.MultiReader(bytes.NewReader(one[:]), r))
}

func (d *Disk) putObject(ctx context.Context, key, ctype string, body []byte) error {
	resp, err := d.do(ctx, http.MethodPut, key, nil, http.Header{"Content-Type": {ctype}}, body)
	if err != nil {
		return fmt.Errorf("s3: put %s: %w", key, err)
	}

	if resp.StatusCode != http.StatusOK {
		return readError("put", key, resp)
	}

	drain(resp)

	return nil
}

// contentType uses the extension, then sniffs the first bytes.
func contentType(key string, head []byte) string {
	if t := mime.TypeByExtension(path.Ext(key)); t != "" {
		return t
	}

	return http.DetectContentType(head)
}

// Open implements storage.Disk; the body streams from S3.
func (d *Disk) Open(ctx context.Context, p string) (io.ReadCloser, error) {
	key, err := storage.Clean(p)
	if err != nil {
		return nil, err
	}

	resp, err := d.do(ctx, http.MethodGet, key, nil, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("s3: get %s: %w", key, err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, readError("get", key, resp)
	}

	return resp.Body, nil
}

// ObjectInfo is the metadata returned by Stat.
type ObjectInfo struct {
	Key          string
	Size         int64
	ETag         string
	ContentType  string
	LastModified time.Time
}

// Stat returns the metadata of p (HEAD); a missing object matches
// fs.ErrNotExist.
func (d *Disk) Stat(ctx context.Context, p string) (ObjectInfo, error) {
	key, err := storage.Clean(p)
	if err != nil {
		return ObjectInfo{}, err
	}

	resp, err := d.do(ctx, http.MethodHead, key, nil, nil, nil)
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("s3: stat %s: %w", key, err)
	}

	if resp.StatusCode != http.StatusOK {
		return ObjectInfo{}, readError("stat", key, resp)
	}

	drain(resp)

	info := ObjectInfo{
		Key:         key,
		Size:        resp.ContentLength,
		ETag:        strings.Trim(resp.Header.Get("ETag"), `"`),
		ContentType: resp.Header.Get("Content-Type"),
	}

	if lm, err := http.ParseTime(resp.Header.Get("Last-Modified")); err == nil {
		info.LastModified = lm
	}

	return info, nil
}

// Exists implements storage.Disk.
func (d *Disk) Exists(ctx context.Context, p string) (bool, error) {
	_, err := d.Stat(ctx, p)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}

	return err == nil, err
}

// Delete implements storage.Disk (missing objects are not an error).
func (d *Disk) Delete(ctx context.Context, p string) error {
	key, err := storage.Clean(p)
	if err != nil {
		return err
	}

	resp, err := d.do(ctx, http.MethodDelete, key, nil, nil, nil)
	if err != nil {
		return fmt.Errorf("s3: delete %s: %w", key, err)
	}

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return readError("delete", key, resp)
	}

	drain(resp)

	return nil
}

type listResult struct {
	IsTruncated           bool   `xml:"IsTruncated"`
	NextContinuationToken string `xml:"NextContinuationToken"`
	Contents              []struct {
		Key string `xml:"Key"`
	} `xml:"Contents"`
}

// List implements storage.Disk with ListObjectsV2, following continuation
// tokens. Folder placeholder objects (keys ending in "/") are skipped.
func (d *Disk) List(ctx context.Context, dir string) ([]string, error) {
	prefix := ""

	if dir != "" && dir != "." {
		c, err := storage.Clean(dir)
		if err != nil {
			return nil, err
		}

		prefix = c + "/"
	}

	var (
		out   []string
		token string
	)

	for {
		q := url.Values{"list-type": {"2"}, "max-keys": {strconv.Itoa(d.maxKeys)}}
		if prefix != "" {
			q.Set("prefix", prefix)
		}

		if token != "" {
			q.Set("continuation-token", token)
		}

		page, err := d.listPage(ctx, prefix, q)
		if err != nil {
			return nil, err
		}

		for _, c := range page.Contents {
			if !strings.HasSuffix(c.Key, "/") {
				out = append(out, c.Key)
			}
		}

		if !page.IsTruncated {
			break
		}

		if page.NextContinuationToken == "" || page.NextContinuationToken == token {
			return nil, fmt.Errorf("s3: list %s: truncated page without a new continuation token", prefix)
		}

		token = page.NextContinuationToken
	}

	slices.Sort(out)

	return out, nil
}

func (d *Disk) listPage(ctx context.Context, prefix string, q url.Values) (listResult, error) {
	var page listResult

	resp, err := d.do(ctx, http.MethodGet, "", q, nil, nil)
	if err != nil {
		return page, fmt.Errorf("s3: list %s: %w", prefix, err)
	}

	if resp.StatusCode != http.StatusOK {
		return page, readError("list", prefix, resp)
	}
	defer resp.Body.Close()

	if err := xml.NewDecoder(io.LimitReader(resp.Body, maxListBody)).Decode(&page); err != nil {
		return page, fmt.Errorf("s3: list %s: decode: %w", prefix, err)
	}

	return page, nil
}

// URL implements storage.Disk: PublicURL + "/" + key, or "" for a private
// disk (use PresignGet for temporary links).
func (d *Disk) URL(p string) string {
	if d.publicURL == "" {
		return ""
	}

	return d.publicURL + "/" + encodePath(strings.TrimPrefix(p, "/"))
}

// PresignGet returns a URL anyone can use to download p until expires
// elapses (1s to 7 days).
func (d *Disk) PresignGet(p string, expires time.Duration) (string, error) {
	return d.presign(http.MethodGet, p, expires, nil)
}

// PresignPut returns a URL anyone can use to upload p (a single PUT, up to
// 5 GiB) until expires elapses (1s to 7 days). Any content type and size
// are accepted; use PresignPutWith to bind them.
func (d *Disk) PresignPut(p string, expires time.Duration) (string, error) {
	return d.presign(http.MethodPut, p, expires, nil)
}

// PresignPutOptions binds a presigned PUT to the upload a server checked.
// Each set field becomes a signed header: the uploader must send exactly
// that value or S3 answers 403 SignatureDoesNotMatch. Browsers set
// Content-Length from the body; Content-Type must be set explicitly
// (fetch(url, {method: "PUT", headers: {"Content-Type": type}, body})).
type PresignPutOptions struct {
	// ContentType is the exact Content-Type the upload must send.
	ContentType string
	// ContentLength is the exact body size in bytes (0 leaves it free). It
	// is not a ceiling: sign the real size the client declared, after
	// checking it against your maximum, or smaller files get 403.
	ContentLength int64
}

// PresignPutWith is PresignPut bound by o: the URL only accepts an upload
// with the given Content-Type and Content-Length.
func (d *Disk) PresignPutWith(p string, expires time.Duration, o PresignPutOptions) (string, error) {
	headers := map[string]string{}

	if o.ContentType != "" {
		if _, _, err := mime.ParseMediaType(o.ContentType); err != nil ||
			strings.ContainsAny(o.ContentType, "\r\n") {
			return "", fmt.Errorf("%w: content type %q", ErrInvalidPresign, o.ContentType)
		}

		headers["content-type"] = o.ContentType
	}

	if o.ContentLength < 0 || o.ContentLength > MaxPartSize {
		return "", fmt.Errorf("%w: content length %d (0 to 5 GiB)", ErrInvalidPresign, o.ContentLength)
	}

	if o.ContentLength > 0 {
		headers["content-length"] = strconv.FormatInt(o.ContentLength, 10)
	}

	return d.presign(http.MethodPut, p, expires, headers)
}

func (d *Disk) presign(method, p string, expires time.Duration, headers map[string]string) (string, error) {
	key, err := storage.Clean(p)
	if err != nil {
		return "", err
	}

	if expires < time.Second || expires > MaxPresignExpiry {
		return "", ErrInvalidExpiry
	}

	return d.signer.presign(method, d.objectURL(key, nil), d.now(), expires, headers), nil
}

// drain lets the connection be reused.
func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody))
	_ = resp.Body.Close()
}
