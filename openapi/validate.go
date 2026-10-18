package openapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrNoOperation is returned when the document has no operation for the
// request.
var ErrNoOperation = errors.New("openapi: no operation matches the request")

// ValidateResponse checks a response body against the document: the
// operation for method and path (a concrete URL path), then the schema
// declared for status, or the "default" response. It supports the JSON
// Schema subset Generate emits.
func (d *Document) ValidateResponse(method, path string, status int, body []byte) error {
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return err
	}

	if method == "HEAD" {
		method = "GET"
	}

	op, tmpl := findOperation(doc, strings.ToLower(method), path)
	if op == nil {
		return fmt.Errorf("%w: %s %s", ErrNoOperation, method, path)
	}

	responses, _ := op["responses"].(map[string]any)

	resp, ok := responses[strconv.Itoa(status)].(map[string]any)
	if !ok {
		if resp, ok = responses["default"].(map[string]any); !ok {
			return fmt.Errorf("openapi: %s %s documents no %d response", method, tmpl, status)
		}
	}

	v := validator{doc: doc}
	resp = v.deref(resp)

	content, _ := resp["content"].(map[string]any)
	if len(content) == 0 {
		if len(body) > 0 && status != 204 {
			return fmt.Errorf("openapi: %s %s %d documents no body", method, tmpl, status)
		}

		return nil
	}

	var schema map[string]any
	for _, mt := range content {
		schema, _ = asMap(mt)["schema"].(map[string]any)
	}

	// An empty schema (a plain route's "*/*" response) accepts any body,
	// JSON or not.
	if len(schema) == 0 {
		return nil
	}

	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return fmt.Errorf("openapi: response is not JSON: %w", err)
	}

	return v.check(schema, value, "$", 0)
}

func findOperation(doc map[string]any, method, path string) (map[string]any, string) {
	paths, _ := doc["paths"].(map[string]any)

	var (
		best      map[string]any
		bestTmpl  string
		bestScore = -1
	)

	for tmpl, item := range paths {
		re := "^" + templateRe.ReplaceAllString(regexp.QuoteMeta(tmpl), `[^/]+`) + "$"
		if !regexp.MustCompile(re).MatchString(path) {
			continue
		}

		op, ok := asMap(item)[method].(map[string]any)
		if !ok {
			continue
		}

		// Prefer literal segments: fewer parameters wins.
		if score := 100 - strings.Count(tmpl, "{"); score > bestScore {
			best, bestTmpl, bestScore = op, tmpl, score
		}
	}

	return best, bestTmpl
}

var templateRe = regexp.MustCompile(`\\\{[A-Za-z_][A-Za-z0-9_]*\\\}`)

type validator struct {
	doc map[string]any
}

func (v validator) deref(m map[string]any) map[string]any {
	for range 16 {
		ref, ok := m["$ref"].(string)
		if !ok {
			return m
		}

		var cur any = v.doc
		for part := range strings.SplitSeq(strings.TrimPrefix(ref, "#/"), "/") {
			cur = asMap(cur)[part]
		}

		next, ok := cur.(map[string]any)
		if !ok {
			return map[string]any{}
		}

		m = next
	}

	return m
}

func (v validator) check(s map[string]any, val any, at string, depth int) error {
	if depth > 64 || s == nil {
		return nil
	}

	s = v.deref(s)

	if all, ok := s["allOf"].([]any); ok {
		for _, sub := range all {
			if err := v.check(asMap(sub), val, at, depth+1); err != nil {
				return err
			}
		}
	}

	if anyOf, ok := s["anyOf"].([]any); ok {
		var first error

		for _, sub := range anyOf {
			err := v.check(asMap(sub), val, at, depth+1)
			if err == nil {
				first = nil
				break
			}

			if first == nil {
				first = err
			}
		}

		if first != nil {
			return first
		}
	}

	if t, ok := s["type"]; ok && !typeMatches(t, val) {
		return fmt.Errorf("%s: got %s, want %v", at, jsonType(val), t)
	}

	if enum, ok := s["enum"].([]any); ok && !containsJSON(enum, val) {
		return fmt.Errorf("%s: %v is not one of %v", at, val, enum)
	}

	switch x := val.(type) {
	case string:
		return checkString(s, x, at)
	case float64:
		return checkNumber(s, x, at)
	case []any:
		return v.checkArray(s, x, at, depth)
	case map[string]any:
		return v.checkObject(s, x, at, depth)
	}

	return nil
}

func (v validator) checkObject(s, obj map[string]any, at string, depth int) error {
	if req, ok := s["required"].([]any); ok {
		for _, k := range req {
			if name, _ := k.(string); !hasKey(obj, name) {
				return fmt.Errorf("%s: missing required %q", at, k)
			}
		}
	}

	props, _ := s["properties"].(map[string]any)
	extra, _ := s["additionalProperties"].(map[string]any)

	for k, val := range obj {
		if ps, ok := props[k].(map[string]any); ok {
			if err := v.check(ps, val, at+"."+k, depth+1); err != nil {
				return err
			}
		} else if extra != nil {
			if err := v.check(extra, val, at+"."+k, depth+1); err != nil {
				return err
			}
		}
	}

	return nil
}

func (v validator) checkArray(s map[string]any, arr []any, at string, depth int) error {
	if n, ok := s["minItems"].(float64); ok && float64(len(arr)) < n {
		return fmt.Errorf("%s: %d items, want at least %v", at, len(arr), n)
	}

	if n, ok := s["maxItems"].(float64); ok && float64(len(arr)) > n {
		return fmt.Errorf("%s: %d items, want at most %v", at, len(arr), n)
	}

	items, _ := s["items"].(map[string]any)
	for i, item := range arr {
		if err := v.check(items, item, at+"["+strconv.Itoa(i)+"]", depth+1); err != nil {
			return err
		}
	}

	if u, _ := s["uniqueItems"].(bool); u {
		for i := range arr {
			for j := i + 1; j < len(arr); j++ {
				if reflect.DeepEqual(arr[i], arr[j]) {
					return fmt.Errorf("%s: items %d and %d are equal", at, i, j)
				}
			}
		}
	}

	return nil
}

func checkString(s map[string]any, x, at string) error {
	n := float64(utf8.RuneCountInString(x))

	if m, ok := s["minLength"].(float64); ok && n < m {
		return fmt.Errorf("%s: length %v, want at least %v", at, n, m)
	}

	if m, ok := s["maxLength"].(float64); ok && n > m {
		return fmt.Errorf("%s: length %v, want at most %v", at, n, m)
	}

	if p, ok := s["pattern"].(string); ok {
		if re, err := regexp.Compile(p); err == nil && !re.MatchString(x) {
			return fmt.Errorf("%s: %q does not match %s", at, x, p)
		}
	}

	if f, _ := s["format"].(string); f == "date-time" {
		if _, err := time.Parse(time.RFC3339Nano, x); err != nil {
			return fmt.Errorf("%s: %q is not a date-time", at, x)
		}
	}

	return nil
}

func checkNumber(s map[string]any, x float64, at string) error {
	if m, ok := s["minimum"].(float64); ok && x < m {
		return fmt.Errorf("%s: %v is below the minimum %v", at, x, m)
	}

	if m, ok := s["maximum"].(float64); ok && x > m {
		return fmt.Errorf("%s: %v is above the maximum %v", at, x, m)
	}

	if m, ok := s["exclusiveMinimum"].(float64); ok && x <= m {
		return fmt.Errorf("%s: %v must be greater than %v", at, x, m)
	}

	return nil
}

func typeMatches(t, val any) bool {
	got := jsonType(val)

	matches := func(want string) bool {
		return want == got || (want == "number" && got == "integer")
	}

	switch t := t.(type) {
	case string:
		return matches(t)
	case []any:
		for _, w := range t {
			if s, ok := w.(string); ok && matches(s) {
				return true
			}
		}
	}

	return false
}

func jsonType(val any) string {
	switch x := val.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64:
		if x == math.Trunc(x) {
			return "integer"
		}

		return "number"
	case []any:
		return "array"
	default:
		return "object"
	}
}

func containsJSON(list []any, val any) bool {
	for _, e := range list {
		if reflect.DeepEqual(e, val) {
			return true
		}
	}

	return false
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func hasKey(m map[string]any, k string) bool {
	_, ok := m[k]
	return ok
}
