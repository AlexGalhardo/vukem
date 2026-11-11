package web

import (
	jsonv1 "encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"errors"
	"io"
	"strconv"
	"strings"
)

// errTrailingJSON marks a body with another value after the first one.
// Callers turn it into the client message they already return.
var errTrailingJSON = errors.New("trailing JSON value")

// jsonNumberUnmarshaler stores a JSON number inside an any as json.Number,
// which is what Decoder.UseNumber did. Other kinds keep the default decode.
var jsonNumberUnmarshaler = jsonv2.UnmarshalFromFunc(func(dec *jsontext.Decoder, v *any) error {
	if dec.PeekKind() != '0' {
		return errors.ErrUnsupported
	}

	raw, err := dec.ReadValue()
	if err != nil {
		return err
	}

	// ReadValue aliases the decoder buffer; string copies it.
	*v = jsonv1.Number(string(raw))

	return nil
})

// jsonDecodeOptions is encoding/json/v2 with v1 compatibility (HTML
// escaping and legacy struct tags stay on the marshal side), plus the two
// stricter v2 decode defaults: duplicate names and invalid UTF-8 fail.
func jsonDecodeOptions(strict bool) jsonv2.Options {
	opts := []jsonv2.Options{
		jsonv1.DefaultOptionsV1(),
		jsontext.AllowDuplicateNames(false),
		jsontext.AllowInvalidUTF8(false),
		jsonv2.WithUnmarshalers(jsonNumberUnmarshaler),
	}
	if strict {
		opts = append(opts, jsonv2.RejectUnknownMembers(true))
	}

	return jsonv2.JoinOptions(opts...)
}

// marshalJSON encodes v with encoding/json/v2 using v1 defaults, so nil
// slices stay null, omitempty stays legacy, and <, > and & stay escaped.
func marshalJSON(v any) ([]byte, error) {
	return jsonv2.Marshal(v, jsonv1.DefaultOptionsV1())
}

// unmarshalJSONBytes decodes one JSON value from b. It is the in-memory
// half of problem+json encoding, not a request body.
func unmarshalJSONBytes(b []byte, v any) error {
	return jsonv2.Unmarshal(b, v, jsonv1.DefaultOptionsV1())
}

// decodeJSONReader decodes one JSON value from r. strict rejects unknown
// struct fields. A second value is errTrailingJSON.
func decodeJSONReader(r io.Reader, v any, strict bool) error {
	opts := jsonDecodeOptions(strict)
	dec := jsontext.NewDecoder(r, opts)

	if err := jsonv2.UnmarshalDecode(dec, v, opts); err != nil {
		return err
	}

	switch _, err := dec.ReadToken(); {
	case errors.Is(err, io.EOF):
		return nil
	case err == nil:
		return errTrailingJSON
	default:
		// A syntax error here is a second, malformed value; read errors (a
		// body over the limit, a dropped connection) keep their type.
		if _, ok := errors.AsType[*jsontext.SyntacticError](err); ok {
			return errTrailingJSON
		}

		return err
	}
}

// unknownField reports the object member RejectUnknownMembers refused.
// v2 wraps ErrUnknownName; the v1 default-options transform still emits
// the historical "json: unknown field" string, so both shapes are read.
func unknownField(err error) (string, bool) {
	var sem *jsonv2.SemanticError
	if errors.As(err, &sem) && errors.Is(sem, jsonv2.ErrUnknownName) {
		if name := sem.JSONPointer.LastToken(); name != "" {
			return name, true
		}
	}

	rest, ok := strings.CutPrefix(err.Error(), "json: unknown field ")
	if !ok {
		return "", false
	}

	field, uerr := strconv.Unquote(rest)

	return field, uerr == nil && field != ""
}
