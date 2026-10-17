package grpc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/web"
	"github.com/AlexGalhardo/vukem/z"
)

type teapot struct{}

func (teapot) Error() string   { return "teapot: secret detail" }
func (teapot) HTTPStatus() int { return http.StatusTeapot }

func TestError(t *testing.T) {
	t.Parallel()

	own := connect.NewError(connect.CodeAborted, errors.New("retry"))
	_, issues := z.Struct[struct {
		Email string `json:"email"`
	}](z.Shape{"email": z.String().Email()}).Parse(map[string]any{"email": "x"})

	tests := []struct {
		name    string
		err     error
		code    connect.Code
		message string
	}{
		{"connect error kept", fmt.Errorf("wrapped: %w", own), connect.CodeAborted, "retry"},
		{"canceled", fmt.Errorf("query: %w", context.Canceled), connect.CodeCanceled, "request canceled"},
		{"deadline", context.DeadlineExceeded, connect.CodeDeadlineExceeded, "deadline exceeded"},
		{"z issues", issues, connect.CodeInvalidArgument, "invalid request"},
		{"validation", errs.NewValidation().Add("name", "is taken"), connect.CodeInvalidArgument, "invalid request"},
		{"unauthenticated", errs.New(http.StatusUnauthorized, ""), connect.CodeUnauthenticated, "Unauthorized"},
		{"forbidden", errs.New(http.StatusForbidden, "Nope."), connect.CodePermissionDenied, "Nope."},
		{"not found", errs.ErrNotFound, connect.CodeNotFound, "Not Found"},
		{"conflict", errs.New(http.StatusConflict, "Taken."), connect.CodeAlreadyExists, "Taken."},
		{"too many", errs.New(http.StatusTooManyRequests, "Slow down."), connect.CodeResourceExhausted, "Slow down."},
		{
			"unavailable",
			errs.Wrap(http.StatusServiceUnavailable, errors.New("redis down")),
			connect.CodeUnavailable,
			"internal error",
		},
		{"status coder", teapot{}, connect.CodeFailedPrecondition, "I'm a teapot"},
		{"unknown", errors.New("pq: password authentication failed"), connect.CodeInternal, "internal error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := Error(tt.err)
			if got.Code() != tt.code || got.Message() != tt.message {
				t.Errorf("Error(%v) = %v %q, want %v %q", tt.err, got.Code(), got.Message(), tt.code, tt.message)
			}
		})
	}

	if Error(nil) != nil {
		t.Error("Error(nil) != nil")
	}
}

func TestErrorContext_LocalizesKeyedErrors(t *testing.T) {
	t.Parallel()

	tr := func(key string, params map[string]any) (string, bool) {
		if key == "post.missing" {
			return fmt.Sprintf("Post %v não encontrado.", params["id"]), true
		}

		return "", false
	}

	keyed := fmt.Errorf(
		"wrap: %w",
		errs.NewKey(http.StatusNotFound, "post.missing", "Post not found.", map[string]any{"id": 7}),
	)

	tests := map[string]struct {
		ctx  context.Context
		err  error
		want string
	}{
		"translated":    {errs.WithTranslator(t.Context(), tr), keyed, "Post 7 não encontrado."},
		"no translator": {t.Context(), keyed, "Post not found."},
		"unknown key": {
			errs.WithTranslator(t.Context(), tr),
			errs.NewKey(http.StatusNotFound, "x.y", "Gone.", nil),
			"Gone.",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := ErrorContext(tt.ctx, tt.err); got.Code() == connect.CodeInternal || got.Message() != tt.want {
				t.Errorf("ErrorContext(%v) = %v %q, want %q", tt.err, got.Code(), got.Message(), tt.want)
			}
		})
	}
}

func TestSecurity_A10_GRPCConnectErrorsMasked(t *testing.T) {
	t.Parallel()

	downstream := connect.NewError(connect.CodeUnavailable, errors.New("dial tcp 10.0.3.7:8080: connection refused"))
	tests := map[string]struct {
		err  error
		code connect.Code
	}{
		"handler internal": {connect.NewError(connect.CodeInternal, errors.New("pq: hunter2")), connect.CodeInternal},
		"wrapped client":   {fmt.Errorf("billing: %w", downstream), connect.CodeUnavailable},
		"data loss": {
			connect.NewError(connect.CodeDataLoss, errors.New("disk /var/db corrupt")),
			connect.CodeDataLoss,
		},
		"unknown": {connect.NewError(connect.CodeUnknown, errors.New("secret")), connect.CodeUnknown},
	}

	for name, tt := range tests {
		got := Error(tt.err)
		if got.Code() != tt.code || got.Message() != internalMessage || len(got.Details()) > 0 {
			t.Errorf(
				"%s: Error(%v) = %v %q, want %v %q",
				name,
				tt.err,
				got.Code(),
				got.Message(),
				tt.code,
				internalMessage,
			)
		}
	}
}

func TestError_KeepsHeadersAsMetadata(t *testing.T) {
	t.Parallel()

	got := Error(errs.New(http.StatusTooManyRequests, "").WithHeader("Retry-After", "30"))
	if v := got.Meta().Get("Retry-After"); v != "30" {
		t.Errorf("Error(429).Meta()[Retry-After] = %q, want 30", v)
	}
}

func TestSecurity_A10_GRPCServerErrorHeadersAllowlisted(t *testing.T) {
	t.Parallel()

	got := Error(errs.New(http.StatusServiceUnavailable, "").
		WithHeader("Retry-After", "30").
		WithHeader("X-Upstream", "10.0.3.7:6379"))

	if v := got.Meta().Get("Retry-After"); v != "30" {
		t.Errorf("Error(503).Meta()[Retry-After] = %q, want 30", v)
	}

	if v := got.Meta().Get("X-Upstream"); v != "" {
		t.Errorf("Error(503).Meta()[X-Upstream] = %q, want it dropped", v)
	}
}

func TestCodeOf(t *testing.T) {
	t.Parallel()

	tests := map[int]connect.Code{
		400: connect.CodeInvalidArgument,
		405: connect.CodeUnimplemented,
		408: connect.CodeDeadlineExceeded,
		410: connect.CodeNotFound,
		412: connect.CodeFailedPrecondition,
		413: connect.CodeResourceExhausted,
		415: connect.CodeInvalidArgument,
		422: connect.CodeInvalidArgument,
		499: connect.CodeCanceled,
		500: connect.CodeInternal,
		501: connect.CodeUnimplemented,
		502: connect.CodeInternal,
		504: connect.CodeDeadlineExceeded,
	}

	for status, want := range tests {
		if got := CodeOf(status); got != want {
			t.Errorf("CodeOf(%d) = %v, want %v", status, got, want)
		}
	}
}

func TestErrors_KeepsTheAppErrorHandlerForOtherRequests(t *testing.T) {
	t.Parallel()

	h := Errors()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		web.Fail(w, r, errs.ErrNotFound)
	}))

	tests := []struct {
		name, contentType string
		wantGRPC          bool
	}{
		{"browser form", "application/x-www-form-urlencoded", false},
		{"grpc", "application/grpc", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodPost, "/greet.v1.GreetService/Greet", strings.NewReader(""))
			req.Header.Set("Content-Type", tt.contentType)

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			isGRPC := rec.Header().Get("Grpc-Status") == "5"
			if isGRPC != tt.wantGRPC {
				t.Errorf("%s: grpc-status %q (HTTP %d), want gRPC error = %v",
					tt.name, rec.Header().Get("Grpc-Status"), rec.Code, tt.wantGRPC)
			}
		})
	}
}

// selfValidating returns err from Validate, like a generated message.
type selfValidating struct{ err error }

func (m *selfValidating) Validate() error { return m.err }

// pgvError mimics protoc-gen-validate's per-field error.
type pgvError struct{ field, reason string }

func (e pgvError) Error() string  { return "invalid " + e.field + ": " + e.reason }
func (e pgvError) Field() string  { return e.field }
func (e pgvError) Reason() string { return e.reason }

func TestInterceptor_UsesValidateMethods(t *testing.T) {
	t.Parallel()

	in := &interceptor{}

	if err := in.validate(&selfValidating{}); err != nil {
		t.Errorf("validate(valid) = %v", err)
	}

	tests := map[string]struct {
		err     error
		message string
	}{
		"field reason error": {pgvError{"name", "is required"}, "invalid name: is required"},
		"validation error":   {errs.NewValidation().Add("name", "is required"), "invalid request"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := Error(in.validate(&selfValidating{err: tt.err}))
			if got.Code() != connect.CodeInvalidArgument || got.Message() != tt.message {
				t.Errorf(
					"validate(%v) = %v %q, want invalid_argument %q",
					tt.err,
					got.Code(),
					got.Message(),
					tt.message,
				)
			}
		})
	}
}

func TestSecurity_A10_GRPCValidateErrorsMasked(t *testing.T) {
	t.Parallel()

	in := &interceptor{}

	tests := map[string]struct {
		err  error
		code connect.Code
	}{
		"db error":  {fmt.Errorf("check unique name: %w", errors.New("pq: password=hunter2")), connect.CodeInternal},
		"5xx error": {errs.New(http.StatusServiceUnavailable, "redis hunter2 down"), connect.CodeUnavailable},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			err := in.validate(&selfValidating{err: tt.err})
			if !logsCause(err, ErrorContext(t.Context(), err)) {
				t.Errorf("validate(%v) cause would not be logged", tt.err)
			}

			got := ErrorContext(t.Context(), err)
			if got.Code() != tt.code || got.Message() != internalMessage {
				t.Errorf(
					"validate(%v) = %v %q, want %v %q",
					tt.err,
					got.Code(),
					got.Message(),
					tt.code,
					internalMessage,
				)
			}
		})
	}
}

type badGatewayError struct{}

func (badGatewayError) Error() string   { return "upstream 10.0.3.7: password=hunter2" }
func (badGatewayError) HTTPStatus() int { return http.StatusBadGateway }

func TestSecurity_A10_GRPCServerHTTPErrorsMasked(t *testing.T) {
	t.Parallel()

	leaky := func(string, map[string]any) (string, bool) { return "dsn postgres://app:hunter2@db", true }
	ctx := errs.WithTranslator(t.Context(), leaky)

	secret := errors.New("db: password=hunter2")
	_, issues := z.Struct[struct {
		Name string `json:"name"`
	}](z.Shape{"name": z.String().Refine(func(string) error { return secret })}).Parse(map[string]any{"name": "x"})

	tests := map[string]struct {
		err  error
		code connect.Code
	}{
		"500 message": {
			errs.New(http.StatusInternalServerError, "query failed: password=hunter2"),
			connect.CodeInternal,
		},
		"503 keyed": {
			errs.NewKey(http.StatusServiceUnavailable, "db.down", "redis hunter2 down", nil),
			connect.CodeUnavailable,
		},
		"501 message": {errs.New(http.StatusNotImplemented, "driver hunter2 missing"), connect.CodeUnimplemented},
		"504 message": {
			errs.New(http.StatusGatewayTimeout, "upstream hunter2 slow"),
			connect.CodeDeadlineExceeded,
		},
		"5xx status coder": {badGatewayError{}, connect.CodeInternal},
		"500 wraps issues": {errs.Wrap(http.StatusInternalServerError, issues), connect.CodeInternal},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := ErrorContext(ctx, tt.err)
			if got.Code() != tt.code || got.Message() != internalMessage || len(got.Details()) != 0 {
				t.Errorf("ErrorContext(%v) = %v %q (%d details), want %v %q without details",
					tt.err, got.Code(), got.Message(), len(got.Details()), tt.code, internalMessage)
			}

			if strings.Contains(got.Error(), "hunter2") {
				t.Errorf("ErrorContext(%v) leaked the cause: %v", tt.err, got)
			}
		})
	}
}
