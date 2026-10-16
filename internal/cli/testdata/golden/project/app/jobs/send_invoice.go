package jobs

import "context"

// SendInvoice is a queued job: q.Dispatch(ctx, jobs.SendInvoice{...}). Its
// fields are stored as JSON, so keep them serializable.
type SendInvoice struct{}

// Kind names the job's handler; keep it stable across deploys.
func (SendInvoice) Kind() string { return "send_invoice" }

// HandleSendInvoice runs the job. Returning an error retries it with backoff.
func HandleSendInvoice(ctx context.Context, j SendInvoice) error {
	return nil
}
