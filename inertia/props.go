package inertia

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/orm"
)

// HeaderScrollMergeIntent tells a scroll prop whether the client appends or
// prepends the next page.
const HeaderScrollMergeIntent = "X-Inertia-Infinite-Scroll-Merge-Intent"

type propKind uint8

const (
	kindValue propKind = iota
	kindOptional
	kindAlways
	kindDefer
)

// Prop is a page prop with protocol behaviour (optional, always, deferred,
// merged, once, scroll). Build one with Optional, Always, Defer, Merge,
// DeepMerge, Once or Scroll and refine it with its methods. The wrapped
// value may be a func(context.Context) (any, error), resolved only when the
// prop is sent.
type Prop struct {
	value any
	kind  propKind

	group  string
	rescue bool

	merge, deepMerge bool
	prependRoot      bool
	appendPaths      []string
	prependPaths     []string
	matchOn          []string

	once      bool
	onceKey   string
	fresh     bool
	expiresAt *int64

	scroll  *ScrollMeta
	wrapper string
}

// ScrollMeta describes the paginator cursor of a scroll prop.
type ScrollMeta struct {
	PageName     string `json:"pageName"`
	PreviousPage any    `json:"previousPage"`
	NextPage     any    `json:"nextPage"`
	CurrentPage  any    `json:"currentPage"`
}

// Optional is never sent on full visits and resolved only when a partial
// reload asks for it.
func Optional(value any) *Prop { return &Prop{value: value, kind: kindOptional} }

// Always is sent on every response, even when a partial reload excludes it.
func Always(value any) *Prop { return &Prop{value: value, kind: kindAlways} }

// Defer skips the prop on the first visit and lets the client fetch it in a
// follow-up request (one per Group, "default" unless set).
func Defer(value any) *Prop { return &Prop{value: value, kind: kindDefer, group: "default"} }

// Merge marks the prop to be appended to the client's existing data on
// partial reloads.
func Merge(value any) *Prop { return &Prop{value: value, merge: true} }

// DeepMerge marks the prop to be deep merged with the client's data.
func DeepMerge(value any) *Prop { return &Prop{value: value, merge: true, deepMerge: true} }

// Once resolves the prop once and lets the client reuse it on later pages.
func Once(value any) *Prop { return &Prop{value: value, once: true} }

// Scroll is an infinite-scroll prop: its wrapper array ("data") is merged
// and meta describes the pages around it.
func Scroll(value any, meta ScrollMeta) *Prop {
	return &Prop{value: value, merge: true, scroll: &meta, wrapper: "data"}
}

// ScrollPage is Scroll for an orm.Page (query parameter "page").
func ScrollPage[T any](p orm.Page[T]) *Prop {
	meta := ScrollMeta{PageName: "page", CurrentPage: p.Page}
	if p.Page > 1 {
		meta.PreviousPage = p.Page - 1
	}

	if p.Page < p.LastPage {
		meta.NextPage = p.Page + 1
	}

	return Scroll(p, meta)
}

// ScrollCursor is Scroll for an orm.CursorPage; cursorName is the query
// parameter carrying the cursor.
func ScrollCursor[T any](p orm.CursorPage[T], cursorName string) *Prop {
	meta := ScrollMeta{PageName: cursorName}
	if p.NextCursor != "" {
		meta.NextPage = p.NextCursor
	}

	return Scroll(p, meta)
}

// Group sets the deferred request group.
func (p *Prop) Group(name string) *Prop { p.group = name; return p }

// Defer makes the prop load in a follow-up request of group.
func (p *Prop) Defer(group string) *Prop {
	if p.kind == kindValue {
		p.kind = kindDefer
	}

	p.group = group

	return p
}

// Rescue keeps the response successful when resolving the prop fails: the
// prop is omitted, listed in rescuedProps and the error is logged.
func (p *Prop) Rescue() *Prop { p.rescue = true; return p }

// Merge marks the prop as mergeable (append at the root).
func (p *Prop) Merge() *Prop { p.merge = true; return p }

// DeepMerge marks the prop for deep merging.
func (p *Prop) DeepMerge() *Prop { p.merge, p.deepMerge = true, true; return p }

// Append merges by appending, at the root or at the given nested paths.
func (p *Prop) Append(paths ...string) *Prop {
	p.merge = true
	if len(paths) == 0 {
		p.prependRoot = false
	}

	p.appendPaths = append(p.appendPaths, paths...)

	return p
}

// Prepend merges by prepending, at the root or at the given nested paths.
func (p *Prop) Prepend(paths ...string) *Prop {
	p.merge = true
	if len(paths) == 0 {
		p.prependRoot = true
	}

	p.prependPaths = append(p.prependPaths, paths...)

	return p
}

// MatchOn names the fields ("id", "data.id") identifying merged items, so
// the client updates them in place.
func (p *Prop) MatchOn(fields ...string) *Prop { p.matchOn = append(p.matchOn, fields...); return p }

// Once makes the prop resolve once and be reused by the client.
func (p *Prop) Once() *Prop { p.once = true; return p }

// As sets the key the client remembers a once prop under (to share it
// between pages with different prop names).
func (p *Prop) As(key string) *Prop { p.once, p.onceKey = true, key; return p }

// Fresh sends a once prop even when the client already has it.
func (p *Prop) Fresh() *Prop { p.fresh = true; return p }

// Until expires a once prop at t; the client refreshes it afterwards.
func (p *Prop) Until(t time.Time) *Prop {
	ms := t.UnixMilli()
	p.once, p.expiresAt = true, &ms

	return p
}

// Wrapper sets the key of a scroll prop's item array (default "data").
func (p *Prop) Wrapper(key string) *Prop { p.wrapper = key; return p }

type onceMeta struct {
	Prop      string `json:"prop"`
	ExpiresAt *int64 `json:"expiresAt"`
}

type scrollEntry struct {
	ScrollMeta

	Reset bool `json:"reset"`
}

// resolver turns props into the page's props and metadata, following the
// protocol's prop evaluation model.
type resolver struct {
	ctx     context.Context
	log     *slog.Logger
	partial bool
	inertia bool
	only    []string // nil: header absent
	except  []string
	reset   []string
	loaded  []string
	prepend bool

	deferred  map[string][]string
	rescued   []string
	merge     []string
	prepended []string
	deep      []string
	matchOn   []string
	scroll    map[string]scrollEntry
	onceProps map[string]onceMeta
}

func splitHeader(v string) []string {
	var out []string

	for part := range strings.SplitSeq(v, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}

	return out
}

func (r *resolver) resolveAll(props map[string]any) (map[string]any, error) {
	unpacked, err := r.unpackDots(props)
	if err != nil {
		return nil, err
	}

	return r.resolveMap(unpacked, "", false)
}

// unpackDots turns top-level "a.b" keys into nested maps.
func (r *resolver) unpackDots(props map[string]any) (map[string]any, error) {
	out := maps.Clone(props)

	for _, key := range slices.Sorted(maps.Keys(props)) {
		if !strings.Contains(key, ".") {
			continue
		}

		segs := strings.Split(key, ".")
		cur := out

		for _, seg := range segs[:len(segs)-1] {
			next, err := r.asMap(cur[seg])
			if err != nil {
				return nil, fmt.Errorf("inertia: prop %q: %w", key, err)
			}

			if next == nil {
				next = map[string]any{}
			}

			cur[seg] = next
			cur = next
		}

		cur[segs[len(segs)-1]] = props[key]
		delete(out, key)
	}

	return out, nil
}

// asMap resolves a closure along a dotted path and returns it as a map, or
// nil when it is not one.
func (r *resolver) asMap(v any) (map[string]any, error) {
	if fn, ok := v.(func(context.Context) (any, error)); ok {
		var err error
		if v, err = fn(r.ctx); err != nil {
			return nil, err
		}
	}

	switch m := v.(type) {
	case map[string]any:
		return m, nil
	case Props:
		return m, nil
	}

	return nil, nil
}

//nolint:gocognit // prop resolution walk with lazy/deferred/once cases kept together
func (r *resolver) resolveMap(props map[string]any, prefix string, parentResolved bool) (map[string]any, error) {
	out := make(map[string]any, len(props))

	for _, key := range slices.Sorted(maps.Keys(props)) {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}

		prop := props[key]
		p, _ := prop.(*Prop)

		if r.partial && !parentResolved && (p == nil || p.kind != kindAlways) && !r.pathRequested(path) {
			continue
		}

		if !r.partial && r.excludeFromFirstLoad(p, path) {
			continue
		}

		val, rescued, err := r.resolveValue(prop, path)
		if err != nil {
			return nil, err
		}

		if rescued {
			continue
		}

		// A closure may return a prop type; unwrap it once so it takes part
		// in filtering and metadata.
		if inner, ok := val.(*Prop); ok && inner != p {
			p = inner
			if !r.partial && r.excludeFromFirstLoad(p, path) {
				continue
			}

			if val, rescued, err = r.resolveValue(p, path); err != nil {
				return nil, err
			}

			if rescued {
				continue
			}
		}

		r.collect(p, path)

		if m, err := r.asMap(val); err == nil && m != nil {
			_, wasMap := prop.(map[string]any)
			_, wasProps := prop.(Props)

			if val, err = r.resolveMap(m, path, parentResolved || (!wasMap && !wasProps)); err != nil {
				return nil, err
			}
		}

		out[key] = val
	}

	return out, nil
}

func (r *resolver) resolveValue(v any, path string) (val any, rescued bool, err error) {
	p, _ := v.(*Prop)
	if p != nil {
		v = p.value
	}

	if fn, ok := v.(func(context.Context) (any, error)); ok {
		v, err = fn(r.ctx)
	}

	if err != nil {
		if p != nil && p.rescue {
			r.log.ErrorContext(r.ctx, "inertia: rescued deferred prop", "prop", path, "err", err)
			r.rescued = append(r.rescued, path)

			return nil, true, nil
		}

		return nil, false, fmt.Errorf("inertia: prop %q: %w", path, err)
	}

	return v, false, nil
}

// excludeFromFirstLoad reports whether a full visit skips the prop, after
// recording the metadata the client needs to fetch or reuse it.
func (r *resolver) excludeFromFirstLoad(p *Prop, path string) bool {
	if p == nil {
		return false
	}

	switch {
	case p.kind == kindOptional || p.kind == kindDefer:
		if p.kind == kindDefer && !r.loadedByClient(p, path) {
			r.addDeferred(p.group, path)
		}

		r.collectMerge(p, path)
		r.collectOnce(p, path)

		return true
	case r.inertia && r.loadedByClient(p, path):
		r.collectOnce(p, path)
		return true
	}

	return false
}

func (r *resolver) loadedByClient(p *Prop, path string) bool {
	key := cmpOr(p.onceKey, path)
	return p.once && !p.fresh && slices.Contains(r.loaded, key)
}

func (r *resolver) addDeferred(group, path string) {
	if r.deferred == nil {
		r.deferred = map[string][]string{}
	}

	r.deferred[group] = append(r.deferred[group], path)
}

func (r *resolver) collect(p *Prop, path string) {
	if p == nil {
		return
	}

	r.collectMerge(p, path)

	if p.scroll != nil {
		if r.scroll == nil {
			r.scroll = map[string]scrollEntry{}
		}

		r.scroll[path] = scrollEntry{ScrollMeta: *p.scroll, Reset: slices.Contains(r.reset, path)}
	}

	r.collectOnce(p, path)
}

func (r *resolver) collectMerge(p *Prop, path string) {
	if !p.merge || slices.Contains(r.reset, path) || (r.partial && !r.inMetadata(path)) {
		return
	}

	appendPaths, prependPaths, prependRoot := p.appendPaths, p.prependPaths, p.prependRoot
	if p.scroll != nil {
		if r.prepend {
			prependPaths = []string{p.wrapper}
		} else {
			appendPaths = []string{p.wrapper}
		}
	}

	atRoot := len(appendPaths) == 0 && len(prependPaths) == 0

	switch {
	case p.deepMerge:
		r.deep = append(r.deep, path)
	case atRoot && !prependRoot:
		r.merge = append(r.merge, path)
	case atRoot:
		r.prepended = append(r.prepended, path)
	default:
		for _, sub := range appendPaths {
			r.merge = append(r.merge, path+"."+sub)
		}

		for _, sub := range prependPaths {
			r.prepended = append(r.prepended, path+"."+sub)
		}
	}

	for _, f := range p.matchOn {
		r.matchOn = append(r.matchOn, path+"."+f)
	}
}

func (r *resolver) collectOnce(p *Prop, path string) {
	if !p.once || (r.partial && !r.inMetadata(path)) {
		return
	}

	if r.onceProps == nil {
		r.onceProps = map[string]onceMeta{}
	}

	r.onceProps[cmpOr(p.onceKey, path)] = onceMeta{Prop: path, ExpiresAt: p.expiresAt}
}

// pathRequested is the partial reload filter: path is in (or leads to, or
// lies under) "only", and not under "except".
func (r *resolver) pathRequested(path string) bool {
	if r.only != nil && !underAny(path, r.only) && !slices.ContainsFunc(r.only, func(o string) bool {
		return strings.HasPrefix(o, path+".")
	}) {
		return false
	}

	return r.except == nil || !underAny(path, r.except)
}

// inMetadata reports whether a partial reload reports metadata for path.
func (r *resolver) inMetadata(path string) bool {
	return (r.only == nil || underAny(path, r.only)) && (r.except == nil || !underAny(path, r.except))
}

func underAny(path string, list []string) bool {
	return slices.ContainsFunc(list, func(p string) bool { return path == p || strings.HasPrefix(path, p+".") })
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}

	return b
}
