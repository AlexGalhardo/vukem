package jobs

import (
	"context"

	vukem "github.com/AlexGalhardo/vukem"
)

// SendInvoice is a queued job: q.Dispatch(ctx, jobs.SendInvoice{...}). Its
// fields are stored as JSON, so keep them serializable.
type SendInvoice struct{}

// Kind names the job's handler; keep it stable across deploys.
func (SendInvoice) Kind() string { return "send_invoice" }

// HandleSendInvoice builds the job's handler from the app (app.DB, app.Now,
// app.Mailer...). Returning an error retries the job with backoff. Name the
// parameters (app, ctx, j) with the first one the body uses.
func HandleSendInvoice(_ *vukem.App) func(ctx context.Context, j SendInvoice) error {
	return func(_ context.Context, _ SendInvoice) error {
		return nil
	}
}
