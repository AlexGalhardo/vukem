package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/AlexGalhardo/vukem/errs"
)

// Session is the request session as seen by handlers. The session package
// implements it; web only depends on this interface.
type Session interface {
	// Get returns a value, or nil.
	Get(key string) any
	// Put stores a value (JSON-compatible) for this and later requests.
	Put(key string, value any)
	// Forget removes keys.
	Forget(keys ...string)
	// Pull returns and removes a value.
	Pull(key string) any
	// Flash stores a value available only on the next request.
	Flash(key string, value any)
	// Regenerate issues a new session id keeping the data (call on login
	// and privilege changes to prevent session fixation).
	Regenerate()
	// Invalidate clears all data and issues a new id (call on logout).
	Invalidate()
	// ID returns the session id (meaningful for server-side stores).
	ID() string
}

// Reserved session keys used by flash, old input and validation errors.
const (
	OldInputKey = "_old"
	ErrorsKey   = "_errors"
	TokenKey    = "_token"
)

type sessionKey struct{}

// WithSession stores s in ctx (session middleware does this).
func WithSession(ctx context.Context, s Session) context.Context {
	return context.WithValue(ctx, sessionKey{}, s)
}

// SessionFrom returns the session stored in ctx, or nil.
func SessionFrom(ctx context.Context) Session {
	s, _ := ctx.Value(sessionKey{}).(Session)
	return s
}

// errNoSession is returned when session features are used without the
// session middleware.
var errNoSession = errs.Wrap(
	http.StatusInternalServerError,
	errors.New("web: no session (add session middleware to the router stack)"),
)

// Session returns the request session or nil when sessions are not enabled.
func (c *Ctx) Session() Session { return SessionFrom(c.Context()) }

// Flash stores a message for the next request (e.g. after a redirect).
func (c *Ctx) Flash(key string, value any) error {
	s := c.Session()
	if s == nil {
		return errNoSession
	}

	s.Flash(key, value)

	return nil
}

// FlashValue returns a flashed value from the previous request, or nil.
func (c *Ctx) FlashValue(key string) any {
	if s := c.Session(); s != nil {
		return s.Get(key)
	}

	return nil
}

// Old returns the input submitted by the previous (failed) form post for
// field, so forms can be re-filled. Nested fields use dots ("author.email").
func (c *Ctx) Old(field string) string {
	s := c.Session()
	if s == nil {
		return ""
	}

	old, _ := s.Get(OldInputKey).(map[string]any)

	var cur any = old

	for part := range strings.SplitSeq(field, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}

		cur = m[part]
	}

	switch v := cur.(type) {
	case string:
		return v
	case []any:
		if len(v) > 0 {
			return fmt.Sprint(v[0])
		}

		return ""
	case nil:
		return ""
	default:
		return fmt.Sprint(v)
	}
}

// Errors returns validation errors flashed by the previous request.
func (c *Ctx) Errors() map[string][]string {
	s := c.Session()
	if s == nil {
		return nil
	}

	return toFieldErrors(s.Get(ErrorsKey))
}

func toFieldErrors(v any) map[string][]string {
	switch e := v.(type) {
	case map[string][]string:
		return e
	case map[string]any:
		out := make(map[string][]string, len(e))

		for k, msgs := range e {
			list, _ := msgs.([]any)
			for _, m := range list {
				out[k] = append(out[k], fmt.Sprint(m))
			}
		}

		return out
	default:
		return nil
	}
}
