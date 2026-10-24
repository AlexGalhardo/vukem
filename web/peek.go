package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// PeekJSON decodes a JSON object body the way [Ctx.Input] does (numbers as
// json.Number, a single value, the route's body limit or [DefaultMaxBody]) and
// puts the body back, so a middleware can key on a field the handler will
// read later. A request without a JSON media type, or with an empty body,
// returns (nil, nil). A body the handler would reject (over the limit, not an
// object, trailing data) returns an error: callers must not guess a value.
func PeekJSON(r *http.Request) (map[string]any, error) {
	if !IsJSONMediaType(r.Header.Get("Content-Type")) || r.Body == nil || r.Body == http.NoBody {
		return nil, nil
	}

	limit, ok := BodyLimitOf(r)
	if !ok {
		limit = DefaultMaxBody
	}

	// The body is already wrapped by LimitBody when a limit is set; the extra
	// byte only detects an unwrapped body over the default.
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), r.Body))

	if err != nil {
		return nil, err
	}

	if int64(len(body)) > limit {
		return nil, errors.New("request body over the limit")
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()

	var out map[string]any

	if err := dec.Decode(&out); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}

		return nil, err
	}

	if dec.More() {
		return nil, errors.New("request body must contain a single JSON value")
	}

	return out, nil
}
