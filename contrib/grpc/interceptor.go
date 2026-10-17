package grpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"connectrpc.com/connect"

	"github.com/AlexGalhardo/vukem/web"
	"github.com/AlexGalhardo/vukem/z"
)

// DefaultMaxMessageBytes caps the size of a request message, like
// grpc-go's default receive limit.
const DefaultMaxMessageBytes = 4 << 20

// Options configures HandlerOptions.
type Options struct {
	// Log receives one line per RPC; nil uses the router's logger.
	Log *slog.Logger
	// Rules validate request messages before the method runs (Validate).
	Rules []Rule
	// MaxMessageBytes caps a request message; zero means
	// DefaultMaxMessageBytes. Larger messages get ResourceExhausted.
	MaxMessageBytes int
	// Interceptors run inside the Vukem ones, so they see validated
	// requests and their errors are mapped too.
	Interceptors []connect.Interceptor
}

// HandlerOptions returns the connect-go handler options that bridge an RPC
// service to Vukem: request validation (Rules and messages with a
// Validate() error method), error mapping (Error), one log line per RPC
// with its code, duration and request id, panic recovery (Internal for the
// client, the stack in the log) and the message size limit. A Validate
// error is InvalidArgument only for z.Issues, *errs.ValidationError and
// Field()/Reason() errors (protoc-gen-validate); any other error is
// mapped, masked and logged like a handler error.
func HandlerOptions(opts Options) []connect.HandlerOption {
	maxBytes := opts.MaxMessageBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxMessageBytes
	}

	in := &interceptor{log: opts.Log, rules: opts.Rules}

	return []connect.HandlerOption{
		connect.WithInterceptors(append([]connect.Interceptor{in}, opts.Interceptors...)...),
		connect.WithRecover(in.recover),
		connect.WithReadMaxBytes(maxBytes),
	}
}

// Rule validates request messages of one type.
type Rule interface {
	check(msg any) (matched bool, err error)
}

type schemaRule[T any] struct{ schema z.Schema[T] }

// Validate checks every request message of type *T against schema before
// the method runs; failures are InvalidArgument with a google.rpc.BadRequest
// detail. Keys are the message's Go JSON names (the proto field names):
//
//	grpc.Validate(z.Struct[greetv1.GreetRequest](z.Shape{"name": z.String().Min(1)}))
func Validate[T any](schema z.Schema[T]) Rule { return schemaRule[T]{schema} }

func (r schemaRule[T]) check(msg any) (bool, error) {
	m, ok := msg.(*T)
	if !ok {
		return false, nil
	}

	b, err := json.Marshal(m)
	if err != nil {
		return true, fmt.Errorf("grpc: encode %T for validation: %w", msg, err)
	}

	_, err = z.ParseJSON(bytes.NewReader(b), r.schema)

	return true, err
}

// validator is implemented by messages with generated or hand-written
// validation (protovalidate-style Validate methods).
type validator interface{ Validate() error }

// fieldReasoner is the per-field error of protoc-gen-validate (PGV):
// its text names the field and the broken rule, safe for clients.
// z.Issues and *errs.ValidationError need no case: ErrorContext maps them
// to InvalidArgument with field violations.
type fieldReasoner interface {
	error
	Field() string
	Reason() string
}

type interceptor struct {
	log   *slog.Logger
	rules []Rule
}

func (in *interceptor) validate(msg any) error {
	for _, r := range in.rules {
		if ok, err := r.check(msg); ok && err != nil {
			return err
		}
	}

	if v, ok := msg.(validator); ok {
		if err := v.Validate(); err != nil {
			// Only known validation errors are the client's fault; anything
			// else (a failed uniqueness query, a 5xx) goes through
			// ErrorContext, which masks and logs it (CWE-209).
			if _, ok := errors.AsType[fieldReasoner](err); ok {
				return connect.NewError(connect.CodeInvalidArgument, err)
			}

			return err
		}
	}

	return nil
}

func (in *interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if req.Spec().IsClient {
			return next(ctx, req)
		}

		start := time.Now()

		err := in.validate(req.Any())

		var res connect.AnyResponse
		if err == nil {
			res, err = next(ctx, req)
		}

		if cerr := in.finish(ctx, req.Spec(), req.Peer(), start, err); cerr != nil {
			return nil, cerr
		}

		return res, nil
	}
}

func (in *interceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (in *interceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		start := time.Now()
		err := next(ctx, &validatingConn{StreamingHandlerConn: conn, in: in})

		if cerr := in.finish(ctx, conn.Spec(), conn.Peer(), start, err); cerr != nil {
			return cerr
		}

		return nil
	}
}

type validatingConn struct {
	connect.StreamingHandlerConn

	in *interceptor
}

func (c *validatingConn) Receive(msg any) error {
	if err := c.StreamingHandlerConn.Receive(msg); err != nil {
		return err
	}

	return c.in.validate(msg)
}

// finish logs the RPC and returns the client-facing error.
func (in *interceptor) finish(
	ctx context.Context,
	spec connect.Spec,
	peer connect.Peer,
	start time.Time,
	err error,
) *connect.Error {
	cerr := ErrorContext(ctx, err)

	code := "ok"
	level := slog.LevelInfo
	attrs := []any{
		"procedure", spec.Procedure,
		"protocol", peer.Protocol,
		"duration", time.Since(start),
	}

	if id := web.RequestID(ctx); id != "" {
		attrs = append(attrs, "request_id", id)
	}

	if cerr != nil {
		code = cerr.Code().String()
		if logsCause(err, cerr) {
			level = slog.LevelError
			attrs = append(attrs, "err", err)
		}
	}

	in.logger(ctx).Log(ctx, level, "rpc", append(attrs, "code", code)...)

	return cerr
}

func (in *interceptor) recover(ctx context.Context, spec connect.Spec, _ http.Header, p any) error {
	in.logger(ctx).ErrorContext(ctx, "rpc panic",
		"procedure", spec.Procedure,
		"request_id", web.RequestID(ctx),
		"panic", fmt.Sprint(p),
		"stack", string(debug.Stack()),
	)

	return connect.NewError(connect.CodeInternal, errors.New(internalMessage))
}

func (in *interceptor) logger(ctx context.Context) *slog.Logger {
	if in.log != nil {
		return in.log
	}

	if l := web.ServicesFrom(ctx).Log; l != nil {
		return l
	}

	return slog.Default()
}
