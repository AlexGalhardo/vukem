package s3

import (
	"crypto/hmac"
	"crypto/rand"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AlexGalhardo/vukem/config"
)

const (
	testAccessKey = "AKIAFAKEACCESSKEY01"
	testSecret    = "fake/Secret+Key/0123456789abcdefghijklmn"
	testRegion    = "eu-west-3"
	testBucket    = "vukem-test"
)

// verifySigV4 checks r the way S3 does: header or query signature,
// credential scope, signed payload hash and presign expiry.
func verifySigV4(r *http.Request, body []byte, now time.Time) error {
	if r.URL.Query().Has("X-Amz-Signature") {
		return verifyPresigned(r, now)
	}

	auth, ok := strings.CutPrefix(r.Header.Get("Authorization"), algorithm+" ")
	if !ok {
		return errors.New("missing AWS4-HMAC-SHA256 authorization")
	}

	fields := map[string]string{}

	for f := range strings.SplitSeq(auth, ", ") {
		k, v, _ := strings.Cut(f, "=")
		fields[k] = v
	}

	t, err := time.Parse(amzDateFormat, r.Header.Get("X-Amz-Date"))
	if err != nil {
		return fmt.Errorf("bad x-amz-date: %w", err)
	}

	s := signer{accessKey: testAccessKey, secret: config.NewSecret(testSecret), region: testRegion, service: "s3"}
	if fields["Credential"] != testAccessKey+"/"+s.scope(t) {
		return fmt.Errorf("bad credential scope %q", fields["Credential"])
	}

	hash := r.Header.Get("X-Amz-Content-Sha256")
	if hash != unsignedPayload && hash != sha256Hex(body) {
		return errors.New("payload hash mismatch")
	}

	signedNames := strings.Split(fields["SignedHeaders"], ";")
	for _, required := range []string{"host", "x-amz-content-sha256", "x-amz-date"} {
		if !slices.Contains(signedNames, required) {
			return fmt.Errorf("%s not signed", required)
		}
	}

	h := http.Header{}

	for _, n := range signedNames {
		if n != "host" {
			h[http.CanonicalHeaderKey(n)] = r.Header.Values(n)
		}
	}

	canon := &http.Request{
		Method: r.Method,
		URL:    &url.URL{Path: r.URL.Path, RawQuery: r.URL.RawQuery},
		Host:   r.Host,
		Header: h,
	}

	creq, signed := canonicalRequest(canon, hash)
	if signed != fields["SignedHeaders"] {
		return fmt.Errorf("signed headers %q, recomputed %q", fields["SignedHeaders"], signed)
	}

	want := s.signature(t, stringToSign(t, s.scope(t), creq))
	if !hmac.Equal([]byte(want), []byte(fields["Signature"])) {
		return errors.New("signature mismatch")
	}

	return nil
}

func verifyPresigned(r *http.Request, now time.Time) error {
	q := r.URL.Query()
	sig := q.Get("X-Amz-Signature")
	q.Del("X-Amz-Signature")

	t, err := time.Parse(amzDateFormat, q.Get("X-Amz-Date"))
	if err != nil {
		return fmt.Errorf("bad X-Amz-Date: %w", err)
	}

	exp, err := strconv.Atoi(q.Get("X-Amz-Expires"))
	if err != nil || exp < 1 || exp > 604800 {
		return errors.New("bad X-Amz-Expires")
	}

	if now.After(t.Add(time.Duration(exp) * time.Second)) {
		return errors.New("request has expired")
	}

	s := signer{accessKey: testAccessKey, secret: config.NewSecret(testSecret), region: testRegion, service: "s3"}
	if q.Get("X-Amz-Credential") != testAccessKey+"/"+s.scope(t) {
		return errors.New("bad credential")
	}

	// Like S3, every signed header must be present with the signed value;
	// Content-Length is checked against the body actually received.
	headers := map[string]string{}

	for n := range strings.SplitSeq(q.Get("X-Amz-SignedHeaders"), ";") {
		switch n {
		case "host":
			headers[n] = r.Host
		case "content-length":
			headers[n] = strconv.FormatInt(r.ContentLength, 10)
		default:
			if r.Header.Get(n) == "" {
				return fmt.Errorf("signed header %s missing", n)
			}

			headers[n] = trimHeader(r.Header.Get(n))
		}
	}

	if _, ok := headers["host"]; !ok {
		return errors.New("host not signed")
	}

	u := &url.URL{Path: r.URL.Path, RawQuery: canonicalQuery(q)}
	creq, _ := canonicalRequestHeaders(r.Method, u, headers, unsignedPayload)

	want := s.signature(t, stringToSign(t, s.scope(t), creq))
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return errors.New("signature mismatch")
	}

	return nil
}

type fakeObject struct {
	data  []byte
	ctype string
	etag  string
	mod   time.Time
}

// fakeS3 is an in-memory S3 that verifies every signature. It serves one
// bucket in path style (/bucket/key) and virtual-hosted style
// (bucket.<host>/key).
type fakeS3 struct {
	mu       sync.Mutex
	objects  map[string]fakeObject
	uploads  map[string]map[int][]byte
	aborted  int
	failPart int  // answer 500 to this part number
	badDone  bool // CompleteMultipartUpload answers 200 with an <Error>
	calls    []string
}

func newFakeS3() *fakeS3 {
	return &fakeS3{objects: map[string]fakeObject{}, uploads: map[string]map[int][]byte{}}
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := verifySigV4(r, body, time.Now()); err != nil {
		writeS3Error(w, http.StatusForbidden, "SignatureDoesNotMatch", err.Error())
		return
	}

	key, ok := f.route(r)
	if !ok {
		writeS3Error(w, http.StatusNotFound, "NoSuchBucket", "The specified bucket does not exist")
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	q := r.URL.Query()
	f.calls = append(f.calls, r.Method+" "+callName(q))

	switch {
	case key == "" && r.Method == http.MethodGet && q.Get("list-type") == "2":
		f.list(w, q)
	case key == "":
		writeS3Error(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "")
	case r.Method == http.MethodPost && q.Has("uploads"):
		id := rand.Text()
		f.uploads[id] = map[int][]byte{}
		writeXML(w, struct {
			XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
			UploadID string   `xml:"UploadId"`
		}{UploadID: id})
	case r.Method == http.MethodPut && q.Has("uploadId"):
		f.uploadPart(w, q, body)
	case r.Method == http.MethodPost && q.Has("uploadId"):
		f.complete(w, key, q.Get("uploadId"), body, r.Header.Get("Content-Type"))
	case r.Method == http.MethodDelete && q.Has("uploadId"):
		delete(f.uploads, q.Get("uploadId"))
		f.aborted++
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodPut:
		f.objects[key] = fakeObject{
			data:  body,
			ctype: r.Header.Get("Content-Type"),
			etag:  sha256Hex(body)[:32],
			mod:   time.Now(),
		}
		w.Header().Set("ETag", `"`+f.objects[key].etag+`"`)
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		o, ok := f.objects[key]
		if !ok {
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusNotFound)
				return
			}

			writeS3Error(w, http.StatusNotFound, "NoSuchKey", "The specified key does not exist.")

			return
		}

		w.Header().Set("Content-Type", o.ctype)
		w.Header().Set("ETag", `"`+o.etag+`"`)
		w.Header().Set("Last-Modified", o.mod.UTC().Format(http.TimeFormat))
		w.Header().Set("Content-Length", strconv.Itoa(len(o.data)))

		if r.Method == http.MethodGet {
			_, _ = w.Write(o.data)
		}
	case r.Method == http.MethodDelete:
		delete(f.objects, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		writeS3Error(w, http.StatusMethodNotAllowed, "MethodNotAllowed", "")
	}
}

func callName(q url.Values) string {
	for _, k := range []string{"uploads", "partNumber", "uploadId", "list-type"} {
		if q.Has(k) {
			return k
		}
	}

	return "object"
}

// route returns the object key ("" = bucket) and whether the bucket matched.
func (f *fakeS3) route(r *http.Request) (string, bool) {
	if strings.HasPrefix(r.Host, testBucket+".") {
		return strings.TrimPrefix(r.URL.Path, "/"), true
	}

	rest, ok := strings.CutPrefix(r.URL.Path, "/"+testBucket)
	if !ok || (rest != "" && !strings.HasPrefix(rest, "/")) {
		return "", false
	}

	return strings.TrimPrefix(rest, "/"), true
}

func (f *fakeS3) list(w http.ResponseWriter, q url.Values) {
	prefix := q.Get("prefix")
	maxKeys, _ := strconv.Atoi(q.Get("max-keys"))
	token := q.Get("continuation-token")

	var keys []string

	for k := range f.objects {
		if strings.HasPrefix(k, prefix) && k > token {
			keys = append(keys, k)
		}
	}

	slices.Sort(keys)

	type content struct {
		Key  string `xml:"Key"`
		Size int    `xml:"Size"`
	}

	res := struct {
		XMLName     xml.Name  `xml:"ListBucketResult"`
		IsTruncated bool      `xml:"IsTruncated"`
		Next        string    `xml:"NextContinuationToken,omitempty"`
		Contents    []content `xml:"Contents"`
	}{}

	if maxKeys > 0 && len(keys) > maxKeys {
		keys = keys[:maxKeys]
		res.IsTruncated = true
		res.Next = keys[len(keys)-1]
	}

	for _, k := range keys {
		res.Contents = append(res.Contents, content{Key: k, Size: len(f.objects[k].data)})
	}

	writeXML(w, res)
}

func (f *fakeS3) uploadPart(w http.ResponseWriter, q url.Values, body []byte) {
	parts, ok := f.uploads[q.Get("uploadId")]
	if !ok {
		writeS3Error(w, http.StatusNotFound, "NoSuchUpload", "")
		return
	}

	n, _ := strconv.Atoi(q.Get("partNumber"))
	if n == f.failPart {
		writeS3Error(w, http.StatusInternalServerError, "InternalError", "injected failure")
		return
	}

	parts[n] = body
	w.Header().Set("ETag", `"`+sha256Hex(body)[:32]+`"`)
}

func (f *fakeS3) complete(w http.ResponseWriter, key, id string, body []byte, ctype string) {
	parts, ok := f.uploads[id]
	if !ok {
		writeS3Error(w, http.StatusNotFound, "NoSuchUpload", "")
		return
	}

	if f.badDone {
		writeXML(w, struct {
			XMLName xml.Name `xml:"Error"`
			Code    string   `xml:"Code"`
		}{Code: "InternalError"})

		return
	}

	var req completeUpload
	if err := xml.Unmarshal(body, &req); err != nil {
		writeS3Error(w, http.StatusBadRequest, "MalformedXML", "")
		return
	}

	var data []byte

	for i, p := range req.Parts {
		b, ok := parts[p.PartNumber]
		if !ok || p.PartNumber != i+1 || p.ETag != `"`+sha256Hex(b)[:32]+`"` {
			writeS3Error(w, http.StatusBadRequest, "InvalidPart", "")
			return
		}

		if i < len(req.Parts)-1 && len(b) < MinPartSize {
			writeS3Error(w, http.StatusBadRequest, "EntityTooSmall", "")
			return
		}

		data = append(data, b...)
	}

	delete(f.uploads, id)
	f.objects[key] = fakeObject{data: data, ctype: ctype, etag: sha256Hex(data)[:32], mod: time.Now()}
	writeXML(w, struct {
		XMLName xml.Name `xml:"CompleteMultipartUploadResult"`
		Key     string   `xml:"Key"`
	}{Key: key})
}

func writeXML(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/xml")
	_ = xml.NewEncoder(w).Encode(v)
}

func writeS3Error(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_ = xml.NewEncoder(w).Encode(struct {
		XMLName   xml.Name `xml:"Error"`
		Code      string   `xml:"Code"`
		Message   string   `xml:"Message"`
		RequestID string   `xml:"RequestId"`
	}{Code: code, Message: msg, RequestID: "REQ123"})
}

func (f *fakeS3) snapshot() (calls []string, aborted, pending int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.calls), f.aborted, len(f.uploads)
}
