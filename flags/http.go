package flags

import (
	"context"
	"net/http"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/inertia"
	"github.com/AlexGalhardo/vukem/web"
)

// Require serves next only while flag is on for the scope in the request
// context; otherwise it responds 404, as if the route did not exist. A
// flag that was not registered is logged when Require is called (at boot)
// and always responds 404.
func (f *Flags) Require(flag Flag[bool]) func(http.Handler) http.Handler {
	f.check(context.Background(), flag)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !f.on(r.Context(), flag) {
				web.Fail(w, r, errs.New(http.StatusNotFound, http.StatusText(http.StatusNotFound)))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// on is Enabled, except that an unregistered flag is off.
func (f *Flags) on(ctx context.Context, flag Flag[bool]) bool {
	d, ok := f.lookup(ctx, flag.name, KindBool)
	return ok && decode[bool](f.resolve(ctx, d, scopeFrom(ctx)))
}

// Share adds the allowlisted flags to every Inertia page as the "flags"
// prop (see Expose); put it after inertia's middleware. Flags that were not
// registered are logged when Share is called (at boot) and left out.
func (f *Flags) Share(allow ...AnyFlag) func(http.Handler) http.Handler {
	f.check(context.Background(), allow...)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			inertia.Share(r.Context(), "flags", f.Expose(r.Context(), allow...))
			next.ServeHTTP(w, r)
		})
	}
}

// check logs every flag in list that is not registered.
func (f *Flags) check(ctx context.Context, list ...AnyFlag) {
	for _, af := range list {
		f.lookup(ctx, af.Name(), af.Kind())
	}
}
