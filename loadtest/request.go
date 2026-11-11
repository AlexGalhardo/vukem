package loadtest

import (
	"net/http"
	"time"
)

// Request is one HTTP request to send.
type Request struct {
	// Name groups results in reports; empty means "METHOD path".
	Name string
	// Method is the HTTP method; empty means GET.
	Method string
	URL    string
	Header http.Header
	Body   []byte
}

// Targeter returns the request for the seq-th arrival (0-based). It must be safe for concurrent use.
type Targeter func(seq uint64) Request

// Static returns a Targeter that always sends r.
func Static(r Request) Targeter {
	return func(uint64) Request { return r }
}

// Result is the outcome of one request.
type Result struct {
	Seq  uint64
	Name string
	// Scheduled is when the request was due in the open model; in the closed model it equals Sent.
	Scheduled time.Time
	// Sent is when the request actually started.
	Sent time.Time
	// Duration is the time until the response body was fully read, measured from Scheduled.
	Duration time.Duration
	// Service is the same, measured from Sent.
	Service time.Duration
	// Waiting is the time from Sent to the first response byte.
	Waiting time.Duration
	// Connecting is the TCP connect time, 0 on a reused connection.
	Connecting time.Duration
	// TLS is the TLS handshake time, 0 without TLS or on a reused connection.
	TLS time.Duration
	// Status is the HTTP status, 0 when Err is set before a response arrived.
	Status   int
	BytesOut int64
	BytesIn  int64
	// Err is a short class, never the transport message (which can hold the URL): "timeout", "canceled",
	// "connection refused", "eof" or "other". Empty on success.
	Err string
}

// Failed reports a transport error or a status of 400 or more.
func (r Result) Failed() bool { return r.Err != "" || r.Status >= 400 }
