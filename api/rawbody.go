package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/web"
)

// RawBody is the request body of a typed route, unread and not decoded: the
// route hands the bytes of a file or any non-JSON payload to the handler
// instead of binding them. Declare it as one exported field of In, tagged
// `json:"-"` so the validator skips it:
//
//	type ImportImage struct {
//		Format string      `query:"format" validate:"required,oneof=png jpeg"`
//		Body   api.RawBody `json:"-"`
//	}
//	api.Register(g, "POST /images", api.Op{MaxBody: 64 << 20}, func(c *web.Ctx, in ImportImage) (ImageJSON, error) {
//		data, err := io.ReadAll(in.Body) // an over-limit body is a 413 error here
//		...
//	})
//
// The reader is limited by the route's body limit ([Op.MaxBody], else the
// router-wide one, else [web.DefaultMaxBody]). A declared Content-Length over
// the limit answers 413 before the handler runs; an undeclared body that
// crosses it makes Read return the same 413 error, which the handler returns.
// The other fields of In come from the query string and the path (`query:"name"`
// tags), never from the body. RawBody needs a method with a body: Register
// panics for GET, HEAD and DELETE, for a field that is not exported or lacks
// `json:"-"`, and for a second RawBody field. The OpenAPI document describes
// the body as application/octet-stream (binary).
type RawBody io.Reader

// rawBodyField returns the index of In's RawBody field, if it has one.
func rawBodyField(who, pattern string, t reflect.Type) ([]int, bool) {
	if t.Kind() != reflect.Struct {
		return nil, false
	}

	var index []int

	for _, f := range reflect.VisibleFields(t) {
		if f.Type != reflect.TypeFor[RawBody]() {
			continue
		}

		switch name, _, _ := strings.Cut(f.Tag.Get("json"), ","); {
		case !f.IsExported():
			panic(fmt.Sprintf("api: %s %q: RawBody field %s must be exported", who, pattern, f.Name))
		case name != "-":
			panic(fmt.Sprintf("api: %s %q: RawBody field %s needs the tag `json:\"-\"`", who, pattern, f.Name))
		case index != nil:
			panic(fmt.Sprintf("api: %s %q: %s has more than one RawBody field", who, pattern, t))
		}

		index = f.Index
	}

	return index, index != nil
}

// openRawBody returns the request body as a reader that reports an
// over-limit read as the 413 error, applying [web.DefaultMaxBody] when no
// limit was set.
func openRawBody(c *web.Ctx) (io.Reader, error) {
	r := c.Request()

	limit, ok := web.BodyLimitOf(r)
	if !ok {
		limit = web.DefaultMaxBody
		c.SetRequest(web.LimitBody(c.Writer(), r, limit))
		r = c.Request()
	}

	if r.ContentLength > limit {
		return nil, errs.ErrRequestTooLarge
	}

	if r.Body == nil {
		return http.NoBody, nil
	}

	return tooLargeReader{r.Body}, nil
}

type tooLargeReader struct{ r io.Reader }

func (t tooLargeReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if _, tooBig := errors.AsType[*http.MaxBytesError](err); tooBig {
		return n, errs.ErrRequestTooLarge
	}

	return n, err
}
