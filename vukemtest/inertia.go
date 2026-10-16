package vukemtest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Inertia marks following requests as Inertia visits (X-Inertia) with the
// client's asset version ("" when the app has no Vite build).
func (c *Client) Inertia(version string) *Client {
	return c.WithHeader("X-Inertia", "true").
		WithHeader("X-Requested-With", "XMLHttpRequest").
		WithHeader("X-Inertia-Version", version)
}

// InertiaPage is the page object of an Inertia response, with chainable
// prop assertions. Prop paths are dotted ("posts.0.title").
type InertiaPage struct {
	t    testing.TB
	page map[string]any
}

var pageScript = regexp.MustCompile(`<script data-page="[^"]*" type="application/json">(.*?)</script>`)

// AssertInertia checks that the response renders component (as JSON for
// Inertia visits, or embedded in the HTML of a first visit) and returns the
// page for prop assertions.
func (r *Response) AssertInertia(component string) *InertiaPage {
	r.t.Helper()

	body := r.rec.Body.Bytes()
	if r.rec.Header().Get("X-Inertia") != "true" {
		m := pageScript.FindSubmatch(body)
		if m == nil {
			r.fail("not an Inertia response")
		}

		body = m[1]
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()

	var page map[string]any
	if err := dec.Decode(&page); err != nil {
		r.fail("not an Inertia response: %v", err)
	}

	if got := page["component"]; got != component {
		r.fail("component = %q, want %q", got, component)
	}

	return &InertiaPage{t: r.t, page: page}
}

// Page returns the decoded page object.
func (p *InertiaPage) Page() map[string]any { return p.page }

// Prop returns the prop at path and whether it exists.
func (p *InertiaPage) Prop(path string) (any, bool) {
	cur := p.page["props"]

	for part := range strings.SplitSeq(path, ".") {
		switch v := cur.(type) {
		case map[string]any:
			next, ok := v[part]
			if !ok {
				return nil, false
			}

			cur = next
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(v) {
				return nil, false
			}

			cur = v[i]
		default:
			return nil, false
		}
	}

	return cur, true
}

// HasProp asserts that path exists; with count, that it is a list or
// object of that size.
func (p *InertiaPage) HasProp(path string, count ...int) *InertiaPage {
	p.t.Helper()

	v, ok := p.Prop(path)
	if !ok {
		p.t.Fatalf("prop %q missing", path)
	}

	if len(count) > 0 {
		n := -1

		switch x := v.(type) {
		case []any:
			n = len(x)
		case map[string]any:
			n = len(x)
		}

		if n != count[0] {
			p.t.Fatalf("prop %q has %d items, want %d", path, n, count[0])
		}
	}

	return p
}

// MissingProp asserts that path does not exist.
func (p *InertiaPage) MissingProp(path string) *InertiaPage {
	p.t.Helper()

	if _, ok := p.Prop(path); ok {
		p.t.Fatalf("prop %q present", path)
	}

	return p
}

// PropEquals asserts that path equals want, compared after a JSON round
// trip (so Go structs, ints and maps compare like the decoded page).
func (p *InertiaPage) PropEquals(path string, want any) *InertiaPage {
	p.t.Helper()

	got, ok := p.Prop(path)
	if !ok {
		p.t.Fatalf("prop %q missing", path)
	}

	b, err := json.Marshal(want)
	if err != nil {
		p.t.Fatalf("vukemtest: encode %v: %v", want, err)
	}

	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()

	var norm any
	if err := dec.Decode(&norm); err != nil {
		p.t.Fatalf("vukemtest: %v", err)
	}

	if !reflect.DeepEqual(got, norm) {
		p.t.Fatalf("prop %q = %s, want %s", path, show(got), show(norm))
	}

	return p
}

func show(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}

	return string(b)
}
