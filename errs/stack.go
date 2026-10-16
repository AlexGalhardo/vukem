package errs

import (
	"errors"
	"runtime"
	"strings"
)

// Frame is one call of a stack trace.
type Frame struct {
	Function string
	File     string
	Line     int
}

// StackTracer is implemented by errors that carry a stack trace; the debug
// error page shows it. *HTTPError and WithStack errors implement it.
type StackTracer interface {
	StackFrames() []Frame
}

const maxDepth = 32

// callers records the stack above its caller's caller (skip 0 = the
// function calling callers).
func callers(skip int) []uintptr {
	pcs := make([]uintptr, maxDepth)
	n := runtime.Callers(skip+2, pcs)

	return pcs[:n]
}

func frames(pcs []uintptr) []Frame {
	if len(pcs) == 0 {
		return nil
	}

	var out []Frame

	it := runtime.CallersFrames(pcs)

	for {
		f, more := it.Next()

		// Errors built in package variables (sentinels) carry the stack of
		// the program's initialisation, which says nothing about the request.
		if strings.HasPrefix(f.Function, "runtime.doInit") {
			return nil
		}

		if f.Function != "" {
			out = append(out, Frame{Function: f.Function, File: f.File, Line: f.Line})
		}

		if !more {
			return out
		}
	}
}

// StackFrames implements StackTracer.
func (e *HTTPError) StackFrames() []Frame { return frames(e.stack) }

type stackError struct {
	err   error
	stack []uintptr
}

func (w *stackError) Error() string        { return w.err.Error() }
func (w *stackError) Unwrap() error        { return w.err }
func (w *stackError) StackFrames() []Frame { return frames(w.stack) }

// WithStack records the caller's stack on err (nil stays nil), for errors
// from other packages that the debug page should trace.
func WithStack(err error) error { return WithStackSkip(err, 1) }

// WithStackSkip is WithStack skipping skip more frames (for helpers and
// panic recovery).
func WithStackSkip(err error, skip int) error {
	if err == nil {
		return nil
	}

	return &stackError{err: err, stack: callers(skip + 1)}
}

// Stack returns the stack recorded closest to the origin of err: the
// deepest error in the Unwrap chain that carries one, or nil.
func Stack(err error) []Frame {
	var found []Frame

	for e := err; e != nil; e = errors.Unwrap(e) {
		if st, ok := e.(StackTracer); ok {
			if f := st.StackFrames(); len(f) > 0 {
				found = f
			}
		}
	}

	return found
}
