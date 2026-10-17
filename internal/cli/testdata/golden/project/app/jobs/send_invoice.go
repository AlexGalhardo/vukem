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

// HandleSendInvoice builds the job's handler from app (app.DB, app.Now,
// app.Mailer...). Returning an error retries the job with backoff.
func HandleSendInvoice(app *vukem.App) func(ctx context.Context, j SendInvoice) error {
	return func(ctx context.Context, j SendInvoice) error {
		return nil
	}
}
