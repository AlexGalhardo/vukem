package web

import (
	"context"

	"github.com/AlexGalhardo/vukem/z"
)

type issueLocalizerKey struct{}

// WithIssueLocalizer stores fn in ctx; Parse, Bind, ParseQuery and
// ParseParams pass validation issues through it before building the
// *errs.ValidationError, so field messages follow the request locale.
// The i18n middleware installs one bound to the resolved locale.
func WithIssueLocalizer(ctx context.Context, fn func(z.Issues) z.Issues) context.Context {
	return context.WithValue(ctx, issueLocalizerKey{}, fn)
}

// IssueLocalizer returns the function stored by WithIssueLocalizer, or nil.
func IssueLocalizer(ctx context.Context) func(z.Issues) z.Issues {
	fn, _ := ctx.Value(issueLocalizerKey{}).(func(z.Issues) z.Issues)
	return fn
}
