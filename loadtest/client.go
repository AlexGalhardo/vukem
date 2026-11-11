package loadtest

import (
	"bytes"
	"cmp"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	defaultTimeout   = 30 * time.Second
	defaultUserAgent = "vukem-bench"
)

// errWinsockRefused is WSAECONNREFUSED: Go on Windows reports it under that number, not as syscall.ECONNREFUSED.
const errWinsockRefused = syscall.Errno(10061)

type client struct {
	http    *http.Client
	allow   []string
	timeout time.Duration
	agent   string
	// own is true when the run built the HTTP client, so the run closes its idle connections at the end.
	own bool
}

// newClient builds the client of a run. A Config.Client wins, for tests, and then none of the protections below
// apply. The transport has no proxy, never follows a redirect (a 302 to another host would turn the generator on a
// target nobody approved) and dials loopback addresses only, unless the host is in AllowRemote.
func newClient(cfg *Config, agent string) *client {
	c := &client{allow: cfg.AllowRemote, timeout: cmp.Or(cfg.Timeout, defaultTimeout), agent: agent, http: cfg.Client}
	if c.http != nil {
		return c
	}

	c.own = true

	tr := &http.Transport{
		MaxIdleConns:        cmp.Or(cfg.Connections, defaultConnections),
		MaxIdleConnsPerHost: cmp.Or(cfg.Connections, defaultConnections),
		IdleConnTimeout:     90 * time.Second,
		// The generator discards the body, so decompressing it would only spend the CPU the target shares.
		DisableCompression: true,
		DisableKeepAlives:  cfg.NoKeepAlive,
		ForceAttemptHTTP2:  cfg.HTTP2,
		DialContext:        newDialContext(cfg.AllowRemote, c.timeout),
		TLSClientConfig:    &tls.Config{MinVersion: tls.VersionTLS12},
	}

	if !cfg.HTTP2 {
		tr.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
	}

	c.http = &http.Client{
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	return c
}

// trace records the connection timings the httptrace hooks report, possibly from another goroutine.
type trace struct {
	mu                                            sync.Mutex
	connStart, connDone, tlsStart, tlsDone, first time.Time
}

func (tr *trace) hooks(now func() time.Time) *httptrace.ClientTrace {
	set := func(p *time.Time) {
		tr.mu.Lock()
		*p = now()
		tr.mu.Unlock()
	}

	return &httptrace.ClientTrace{
		ConnectStart:         func(string, string) { set(&tr.connStart) },
		ConnectDone:          func(string, string, error) { set(&tr.connDone) },
		TLSHandshakeStart:    func() { set(&tr.tlsStart) },
		TLSHandshakeDone:     func(tls.ConnectionState, error) { set(&tr.tlsDone) },
		GotFirstResponseByte: func() { set(&tr.first) },
	}
}

// newDialContext dials like net.Dialer, but pins every connection to a loopback address unless the host is in
// allow. The host name was checked before the request, yet a name such as x.localhost can resolve to an internal
// address, and an answer can change between the check and the dial (DNS rebinding): the address that is actually
// connected to is what gets checked.
func newDialContext(
	allow []string,
	timeout time.Duration,
) func(ctx context.Context, network, addr string) (net.Conn, error) {
	open := &net.Dialer{Timeout: timeout}
	pinned := &net.Dialer{Timeout: timeout, Control: loopbackOnly}

	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if hostAllowed(allow, addr) {
			return open.DialContext(ctx, network, addr)
		}

		return pinned.DialContext(ctx, network, addr)
	}
}

// loopbackOnly is a net.Dialer.Control hook: it runs with the resolved address, right before the connection.
func loopbackOnly(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil || !ap.Addr().Unmap().IsLoopback() {
		return errors.New(
			"loadtest: refusing to connect to a non-loopback address; pass --allow-remote for hosts you own",
		)
	}

	return nil
}

// hostAllowed reports whether the host of hostport is in allow, exactly, ignoring case and port.
func hostAllowed(allow []string, hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}

	return slices.ContainsFunc(allow, func(a string) bool { return strings.EqualFold(a, host) })
}

func span(from, to time.Time) time.Duration {
	if from.IsZero() || to.IsZero() {
		return 0
	}

	return to.Sub(from)
}

// do sends one request and measures it. A zero scheduled means the closed model: the request is due when it is sent.
// The target is checked here, for every request, because a Targeter may change the host.
func (c *client) do(ctx context.Context, req Request, seq uint64, scheduled time.Time, now func() time.Time) Result {
	res, _ := c.doBody(ctx, req, seq, scheduled, now, 0)

	return res
}

// doBody is do that also keeps up to keep bytes of the response body (the rest is read and discarded). The Response
// is nil when keep is 0 or the request failed before a response.
func (c *client) doBody(
	ctx context.Context, req Request, seq uint64, scheduled time.Time, now func() time.Time, keep int,
) (Result, *Response) {
	res := Result{Seq: seq, Name: req.Name, Sent: now(), BytesOut: int64(len(req.Body))}
	if scheduled.IsZero() {
		scheduled = res.Sent
	}

	res.Scheduled = scheduled

	var out *Response

	finish := func() (Result, *Response) {
		end := now()
		res.Duration, res.Service = end.Sub(scheduled), end.Sub(res.Sent)

		return res, out
	}

	u, err := parseTarget(req.URL, c.allow)
	if err != nil {
		res.Err = "other"

		return finish()
	}

	method := cmp.Or(req.Method, http.MethodGet)
	if res.Name == "" {
		res.Name = method + " " + u.EscapedPath()
	}

	rctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	var tr trace

	httpReq, err := http.NewRequestWithContext(
		httptrace.WithClientTrace(rctx, tr.hooks(now)),
		method,
		u.String(),
		bytes.NewReader(req.Body),
	)
	if err != nil {
		res.Err = "other"

		return finish()
	}

	httpReq.Header = req.Header.Clone()
	if httpReq.Header == nil {
		httpReq.Header = http.Header{}
	}

	if httpReq.Header.Get("User-Agent") == "" {
		httpReq.Header.Set("User-Agent", c.agent)
	}

	resp, err := c.http.Do(httpReq)
	if err == nil {
		res.Status = resp.StatusCode

		var kept []byte

		kept, res.BytesIn, err = readBody(resp.Body, keep)

		_ = resp.Body.Close()

		if keep > 0 {
			out = &Response{Status: resp.StatusCode, Header: resp.Header.Clone(), Body: kept}
		}
	}

	if err != nil {
		res.Err = classify(ctx, err)
	}

	tr.mu.Lock()
	res.Waiting, res.Connecting, res.TLS = span(
		res.Sent,
		tr.first,
	), span(
		tr.connStart,
		tr.connDone,
	), span(
		tr.tlsStart,
		tr.tlsDone,
	)
	tr.mu.Unlock()

	return finish()
}

// readBody reads the whole body, keeping its first keep bytes. It returns the bytes kept and the total read.
func readBody(r io.Reader, keep int) ([]byte, int64, error) {
	if keep <= 0 {
		n, err := io.Copy(io.Discard, r)

		return nil, n, err
	}

	var buf bytes.Buffer

	n, err := io.CopyN(&buf, r, int64(keep))
	if errors.Is(err, io.EOF) {
		return buf.Bytes(), n, nil
	}

	if err != nil {
		return buf.Bytes(), n, err
	}

	rest, err := io.Copy(io.Discard, r)

	return buf.Bytes(), n + rest, err
}

// classify reduces a transport error to a short class. The message itself never reaches a report: it can hold the URL.
func classify(runCtx context.Context, err error) string {
	var (
		netErr net.Error
		errno  syscall.Errno
	)

	switch {
	case runCtx.Err() != nil && errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return "timeout"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.As(err, &errno) && (errno == syscall.ECONNREFUSED || errno == errWinsockRefused):
		return "connection refused"
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "eof"
	}

	return "other"
}
