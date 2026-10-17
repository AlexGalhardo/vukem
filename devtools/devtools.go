// Package devtools is Vukem's development dashboard, served at /_vukem:
// recent requests with timings, SQL queries (with N+1 detection), jobs,
// events, sent mail previews, log records, per-request traces and the
// route table. Everything lives in bounded in-memory ring buffers.
//
// Apps enable it with vukem.WithDevtools() or DEVTOOLS=true; the app
// refuses to boot when it is enabled with APP_ENV=production, and the
// dashboard answers only loopback requests for a loopback Host unless
// DEVTOOLS_INSECURE_ALLOW_REMOTE=true. Collection hooks are observers set
// on the database, queue and event bus, a mail transport wrapper and a
// slog handler, so an app without devtools pays nothing.
//
// [New] returns the [Devtools] collector; [Devtools.Middleware] records
// requests, [Devtools.Register] mounts the dashboard at [Path], and the
// Observe methods, [Devtools.MailTransport] and [Devtools.LogHandler] are
// the hooks the app wires for you.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/devtools.md.
package devtools

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/events"
	"github.com/AlexGalhardo/vukem/mail"
	"github.com/AlexGalhardo/vukem/middleware"
	"github.com/AlexGalhardo/vukem/queue"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

// Path is the dashboard prefix.
const Path = "/_vukem"

// Defaults for Config.
const (
	DefaultCapacity          = 200
	DefaultNPlusOneThreshold = 3
	// maxMailBody bounds a stored mail part so previews cannot exhaust
	// memory.
	maxMailBody = 512 << 10
)

// Config tunes the collector.
type Config struct {
	// Capacity is the number of records kept per kind (default 200).
	Capacity int
	// NPlusOneThreshold flags a SELECT whose normalized statement runs at
	// least this many times within one request (default 3).
	NPlusOneThreshold int
	// InsecureAllowRemote serves the dashboard to any client. By default
	// only loopback clients with a loopback Host (no proxy headers) get it.
	InsecureAllowRemote bool
	// Level is the minimum level of collected log records (default Info).
	Level slog.Leveler
	// Routes lists the app routes for the routes page (Router.Routes).
	Routes func() []router.RouteInfo
	// Now is the clock (default time.Now).
	Now func() time.Time
}

// Request is a recorded HTTP request.
type Request struct {
	// ID is the devtools trace id ("r12").
	ID string
	// RequestID is the X-Request-ID response header, when set.
	RequestID string
	Method    string
	Path      string
	// Pattern is the matched ServeMux pattern ("GET /users/{id}").
	Pattern string
	// Route is the route name, when the route has one.
	Route     string
	Status    int
	Start     time.Time
	Duration  time.Duration
	Queries   int
	QueryTime time.Duration
	// NPlusOne lists the SELECT shapes repeated at least the threshold.
	NPlusOne []Repeat
}

// Repeat is a statement shape and how often it ran in one request.
type Repeat struct {
	SQL   string
	Count int
}

// Query is a recorded SQL statement; Args are redacted to types.
type Query struct {
	TraceID  string
	Time     time.Time
	SQL      string
	Args     []string
	Duration time.Duration
	Caller   string
	Err      string
	NPlusOne bool
}

// Job is a recorded queue step.
type Job struct {
	TraceID  string
	Time     time.Time
	ID       string
	Kind     string
	Queue    string
	Status   string
	Attempt  int
	Duration time.Duration
	Err      string
}

// Event is a recorded event dispatch.
type Event struct {
	TraceID   string
	Time      time.Time
	Type      string
	Listeners int
	Duration  time.Duration
	Err       string
}

// Mail is a recorded outgoing message.
type Mail struct {
	ID          string
	TraceID     string
	Time        time.Time
	From        string
	To          []string
	Cc          []string
	Subject     string
	HTML        string
	Text        string
	Attachments []string
	Err         string
}

// Log is a recorded log record.
type Log struct {
	TraceID string
	Time    time.Time
	Level   string
	Message string
	Attrs   string
}

// Devtools collects records and serves the dashboard. It implements
// database.QueryObserver, queue.Observer and events.Observer.
type Devtools struct {
	cfg Config
	seq atomic.Uint64

	requests *ring[Request]
	queries  *ring[Query]
	jobs     *ring[Job]
	events   *ring[Event]
	mails    *ring[Mail]
	logs     *ring[Log]

	routeOnce  sync.Once
	routeNames map[string]string
}

var (
	_ database.QueryObserver = (*Devtools)(nil)
	_ queue.Observer         = (*Devtools)(nil)
	_ events.Observer        = (*Devtools)(nil)
)

// New returns an empty collector.
func New(cfg Config) *Devtools {
	if cfg.Capacity <= 0 {
		cfg.Capacity = DefaultCapacity
	}

	if cfg.NPlusOneThreshold <= 1 {
		cfg.NPlusOneThreshold = DefaultNPlusOneThreshold
	}

	if cfg.Level == nil {
		cfg.Level = slog.LevelInfo
	}

	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	return &Devtools{
		cfg:      cfg,
		requests: newRing[Request](cfg.Capacity),
		queries:  newRing[Query](cfg.Capacity),
		jobs:     newRing[Job](cfg.Capacity),
		events:   newRing[Event](cfg.Capacity),
		mails:    newRing[Mail](cfg.Capacity),
		logs:     newRing[Log](cfg.Capacity),
	}
}

// active is the in-flight state of one request, shared through its
// context with the observers.
type active struct {
	id    string
	skip  bool // a dashboard request: record nothing
	start time.Time

	mu        sync.Mutex
	pattern   string
	queries   int
	queryTime time.Duration
	counts    map[string]int
}

type ctxKey struct{}

func activeFrom(ctx context.Context) *active {
	a, _ := ctx.Value(ctxKey{}).(*active)
	return a
}

// traceID returns the request's trace id, "" outside requests, and false
// for dashboard requests (which are not recorded).
func traceID(ctx context.Context) (string, bool) {
	a := activeFrom(ctx)
	if a == nil {
		return "", true
	}

	return a.id, !a.skip
}

func isDashboard(path string) bool { return path == Path || strings.HasPrefix(path, Path+"/") }

// Middleware records every request except the dashboard's own. Install it
// first with Router.UseServer so its timing covers the whole stack.
func (d *Devtools) Middleware() router.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isDashboard(r.URL.Path) {
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, &active{skip: true})))
				return
			}

			a := &active{id: "r" + strconv.FormatUint(d.seq.Add(1), 10), start: d.cfg.Now(), counts: map[string]int{}}

			rw, ok := w.(*web.ResponseWriter)
			if !ok {
				rw = &web.ResponseWriter{ResponseWriter: w}
			}

			defer func() {
				p := recover()
				d.finish(a, r, rw, p != nil)

				if p != nil {
					panic(p)
				}
			}()

			next.ServeHTTP(rw, r.WithContext(context.WithValue(r.Context(), ctxKey{}, a)))
		})
	}
}

// RouteMiddleware captures the matched route pattern; install it with
// Router.Use.
func (d *Devtools) RouteMiddleware() router.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if a := activeFrom(r.Context()); a != nil && !a.skip {
				a.mu.Lock()
				a.pattern = r.Pattern
				a.mu.Unlock()
			}

			next.ServeHTTP(w, r)
		})
	}
}

func (d *Devtools) finish(a *active, r *http.Request, rw *web.ResponseWriter, panicked bool) {
	status := rw.Status()

	switch {
	case panicked:
		status = http.StatusInternalServerError
	case status == 0:
		status = http.StatusOK
	}

	a.mu.Lock()
	req := Request{
		ID: a.id, RequestID: rw.Header().Get(middleware.RequestIDHeader), Method: r.Method, Path: r.URL.Path,
		Pattern: a.pattern, Status: status, Start: a.start, Duration: d.cfg.Now().Sub(a.start),
		Queries: a.queries, QueryTime: a.queryTime,
	}

	for sql, n := range a.counts {
		if n >= d.cfg.NPlusOneThreshold && isSelect(sql) {
			req.NPlusOne = append(req.NPlusOne, Repeat{SQL: sql, Count: n})
		}
	}
	a.mu.Unlock()

	slices.SortFunc(req.NPlusOne, func(x, y Repeat) int {
		return cmp.Or(cmp.Compare(y.Count, x.Count), strings.Compare(x.SQL, y.SQL))
	})

	req.Route = d.routeName(req.Pattern)
	d.requests.push(req)
}

func (d *Devtools) routeName(pattern string) string {
	if pattern == "" || d.cfg.Routes == nil {
		return ""
	}

	// Routes are compiled before the first request and never change.
	d.routeOnce.Do(func() {
		d.routeNames = map[string]string{}

		for _, rt := range d.cfg.Routes() {
			key := rt.Path
			if rt.Method != "ANY" {
				key = rt.Method + " " + rt.Path
			}

			d.routeNames[key] = rt.Name
		}
	})

	return d.routeNames[pattern]
}

// ObserveQuery implements database.QueryObserver.
func (d *Devtools) ObserveQuery(ctx context.Context, q database.Query) {
	id, ok := traceID(ctx)
	if !ok {
		return
	}

	norm := NormalizeSQL(q.SQL)
	rec := Query{
		TraceID: id, Time: d.cfg.Now().Add(-q.Duration), SQL: norm, Args: RedactArgs(q.Args),
		Duration: q.Duration, Caller: caller(),
	}

	if q.Err != nil {
		rec.Err = q.Err.Error()
	}

	if a := activeFrom(ctx); a != nil {
		a.mu.Lock()
		a.queries++
		a.queryTime += q.Duration
		a.counts[norm]++
		rec.NPlusOne = a.counts[norm] >= d.cfg.NPlusOneThreshold && isSelect(norm)
		a.mu.Unlock()
	}

	d.queries.push(rec)
}

// ObserveJob implements queue.Observer.
func (d *Devtools) ObserveJob(ctx context.Context, e queue.JobEvent) {
	id, ok := traceID(ctx)
	if !ok {
		return
	}

	d.jobs.push(Job{
		TraceID: id, Time: d.cfg.Now(), ID: e.ID, Kind: e.Kind, Queue: e.Queue, Status: string(e.Status),
		Attempt: e.Attempt, Duration: e.Duration, Err: errText(e.Err),
	})
}

// ObserveEvent implements events.Observer.
func (d *Devtools) ObserveEvent(ctx context.Context, e events.Event) {
	id, ok := traceID(ctx)
	if !ok {
		return
	}

	d.events.push(Event{
		TraceID: id, Time: d.cfg.Now(), Type: e.Type, Listeners: e.Listeners, Duration: e.Duration,
		Err: errText(e.Err),
	})
}

// MailTransport wraps next so every message it sends is kept for preview.
func (d *Devtools) MailTransport(next mail.Transport) mail.Transport {
	return &mailRecorder{d: d, next: next}
}

type mailRecorder struct {
	d    *Devtools
	next mail.Transport
}

func (m *mailRecorder) Send(ctx context.Context, msg *mail.Message) error {
	err := m.next.Send(ctx, msg)

	id, ok := traceID(ctx)
	if !ok {
		return err
	}

	rec := Mail{
		ID: "m" + strconv.FormatUint(m.d.seq.Add(1), 10), TraceID: id, Time: m.d.cfg.Now(), From: msg.From,
		To: msg.To, Cc: msg.Cc, Subject: msg.Subject, HTML: truncate(msg.HTML), Text: truncate(msg.Text),
		Err: errText(err),
	}

	for _, a := range msg.Attachments {
		rec.Attachments = append(
			rec.Attachments,
			fmt.Sprintf("%s (%s, %d bytes)", a.Filename, a.ContentType, len(a.Data)),
		)
	}

	m.d.mails.push(rec)

	return err
}

func truncate(s string) string {
	if len(s) <= maxMailBody {
		return s
	}

	return s[:maxMailBody] + "\n[truncated by devtools]"
}

func errText(err error) string {
	if err == nil {
		return ""
	}

	return err.Error()
}

// Requests returns the recorded requests, newest first.
func (d *Devtools) Requests() []Request { return d.requests.items() }

// Queries returns the recorded queries, newest first.
func (d *Devtools) Queries() []Query { return d.queries.items() }

// Jobs returns the recorded job steps, newest first.
func (d *Devtools) Jobs() []Job { return d.jobs.items() }

// Events returns the recorded dispatches, newest first.
func (d *Devtools) Events() []Event { return d.events.items() }

// Mails returns the recorded messages, newest first.
func (d *Devtools) Mails() []Mail { return d.mails.items() }

// Logs returns the recorded log records, newest first.
func (d *Devtools) Logs() []Log { return d.logs.items() }
