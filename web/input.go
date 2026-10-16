package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/AlexGalhardo/golang-framework/errs"
)

// DefaultMaxBody is the request body limit applied by Input and BindJSON
// when no smaller limit was set by middleware (A06: resource exhaustion).
const DefaultMaxBody = 10 << 20

// maxMultipartMemory is kept in memory before spilling uploads to disk.
const maxMultipartMemory = 8 << 20

// maxInputDepth bounds bracket nesting in form keys (a[b][c]...).
const maxInputDepth = 16

// Input returns the request body (JSON object, urlencoded or multipart
// form) merged over the query string as a generic map ready for schema
// parsing: JSON keeps its types (numbers as json.Number); form values are
// strings, repeated or "[]" keys become lists and "a[b]" keys nest.
func (c *Ctx) Input() (map[string]any, error) {
	out := FormToMap(c.r.URL.Query())

	switch {
	case c.r.Body == nil || c.r.Body == http.NoBody:
		return out, nil
	case c.IsJSON():
		var body map[string]any
		if err := c.decodeJSON(&body); err != nil {
			return nil, err
		}

		maps.Copy(out, body)
	case isForm(c.r):
		if err := c.parseForm(); err != nil {
			return nil, err
		}

		maps.Copy(out, FormToMap(c.r.PostForm))
	}

	return out, nil
}

// QueryInput returns the query string as a generic map (see Input).
func (c *Ctx) QueryInput() map[string]any { return FormToMap(c.r.URL.Query()) }

// BindJSON decodes the JSON body into v, rejecting unknown fields, trailing
// data and bodies over the size limit.
func (c *Ctx) BindJSON(v any) error {
	if !c.IsJSON() {
		return errs.ErrUnsupportedMediaType
	}

	return c.decodeJSON(v)
}

// FormFile returns an uploaded file.
func (c *Ctx) FormFile(name string) (multipart.File, *multipart.FileHeader, error) {
	if err := c.parseForm(); err != nil {
		return nil, nil, err
	}

	f, h, err := c.r.FormFile(name)
	if errors.Is(err, http.ErrMissingFile) {
		return nil, nil, errs.NewValidation().Add(name, "The file is required.")
	}

	return f, h, err
}

// FormValue returns a form or query value.
func (c *Ctx) FormValue(name string) string {
	_ = c.parseForm() // FormValue reports absence as "" by design
	return c.r.FormValue(name)
}

type bodyLimitKey struct{}

// LimitBody caps the request body at n bytes and marks the request so
// handlers do not apply DefaultMaxBody on top. Body-limit middleware uses it.
func LimitBody(w http.ResponseWriter, r *http.Request, n int64) *http.Request {
	r.Body = http.MaxBytesReader(w, r.Body, n)
	return r.WithContext(context.WithValue(r.Context(), bodyLimitKey{}, n))
}

func (c *Ctx) limitBody() {
	if _, limited := c.r.Context().Value(bodyLimitKey{}).(int64); !limited {
		c.r = LimitBody(c.w, c.r, DefaultMaxBody)
	}
}

func (c *Ctx) decodeJSON(v any) error {
	c.limitBody()

	dec := json.NewDecoder(c.r.Body)
	dec.UseNumber()

	if err := dec.Decode(v); err != nil {
		return bodyError(err)
	}

	if dec.More() {
		return errs.New(http.StatusBadRequest, "Request body must contain a single JSON value.")
	}

	return nil
}

func (c *Ctx) parseForm() error {
	if c.r.PostForm != nil {
		return nil
	}

	c.limitBody()

	var err error

	ct, _, _ := mime.ParseMediaType(c.r.Header.Get("Content-Type"))
	if ct == "multipart/form-data" {
		err = c.r.ParseMultipartForm(maxMultipartMemory)
	} else {
		err = c.r.ParseForm()
	}

	if err != nil {
		return bodyError(err)
	}

	return nil
}

func isForm(r *http.Request) bool {
	ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	return ct == "application/x-www-form-urlencoded" || ct == "multipart/form-data"
}

func bodyError(err error) error {
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return errs.ErrRequestTooLarge
	}

	if errors.Is(err, io.EOF) {
		return errs.New(http.StatusBadRequest, "Request body is empty.")
	}

	var (
		syntax *json.SyntaxError
		typ    *json.UnmarshalTypeError
	)

	switch {
	case errors.As(err, &syntax):
		msg := fmt.Sprintf("Malformed JSON at position %d.", syntax.Offset)
		return &errs.HTTPError{Status: http.StatusBadRequest, Message: msg, Err: err}
	case errors.As(err, &typ):
		return &errs.HTTPError{
			Status:  http.StatusBadRequest,
			Message: "Request body has the wrong JSON type.",
			Err:     err,
		}
	}

	return errs.Wrap(http.StatusBadRequest, err)
}

// FormToMap converts url.Values into nested maps and lists:
// "a=1&a=2" → {"a": ["1","2"]}, "tags[]=x" → {"tags": ["x"]},
// "author[email]=e" → {"author": {"email": "e"}},
// "items[0][name]=n" → {"items": [{"name": "n"}]}.
func FormToMap(values url.Values) map[string]any {
	root := map[string]any{}

	for key, vals := range values {
		parts := splitKey(key)
		if len(parts) == 0 || len(parts) > maxInputDepth {
			continue
		}

		var v any = vals[0]
		if len(vals) > 1 || (len(parts) > 1 && parts[len(parts)-1] == "") {
			list := make([]any, len(vals))
			for i, s := range vals {
				list[i] = s
			}

			v = list
		}

		if parts[len(parts)-1] == "" {
			parts = parts[:len(parts)-1]
		}

		setPath(root, parts, v)
	}

	for k, v := range root {
		root[k] = normalize(v)
	}

	return root
}

// splitKey splits "a[b][0][]" into ["a","b","0",""].
func splitKey(key string) []string {
	first, rest, found := strings.Cut(key, "[")
	if first == "" {
		return nil
	}

	parts := []string{first}

	for found {
		var seg string

		seg, rest, found = strings.Cut(rest, "]")
		if !found {
			return nil // unbalanced brackets
		}

		parts = append(parts, seg)

		if rest == "" {
			break
		}

		if !strings.HasPrefix(rest, "[") {
			return nil
		}

		rest = rest[1:]
		found = true
	}

	return parts
}

func setPath(m map[string]any, parts []string, v any) {
	for i, p := range parts {
		if i == len(parts)-1 {
			m[p] = v
			return
		}

		next, ok := m[p].(map[string]any)
		if !ok {
			next = map[string]any{}
			m[p] = next
		}

		m = next
	}
}

// normalize turns maps whose keys are exactly 0..n-1 into lists.
func normalize(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}

	for k, child := range m {
		m[k] = normalize(child)
	}

	if len(m) == 0 {
		return m
	}

	list := make([]any, len(m))
	for k, child := range m {
		i, err := strconv.Atoi(k)
		if err != nil || i < 0 || i >= len(m) || strconv.Itoa(i) != k {
			return m
		}

		list[i] = child
	}

	return list
}
