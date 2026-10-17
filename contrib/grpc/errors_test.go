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
			"Service Unavailable",
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

func TestError_KeepsHeadersAsMetadata(t *testing.T) {
	t.Parallel()

	got := Error(errs.New(http.StatusTooManyRequests, "").WithHeader("Retry-After", "30"))
	if v := got.Meta().Get("Retry-After"); v != "30" {
		t.Errorf("Error(429).Meta()[Retry-After] = %q, want 30", v)
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

type selfValidating struct{ ok bool }

func (m *selfValidating) Validate() error {
	if m.ok {
		return nil
	}

	return errors.New("name is required")
}

func TestInterceptor_UsesValidateMethods(t *testing.T) {
	t.Parallel()

	in := &interceptor{}

	if err := in.validate(&selfValidating{ok: true}); err != nil {
		t.Errorf("validate(valid) = %v", err)
	}

	err := Error(in.validate(&selfValidating{}))
	if err.Code() != connect.CodeInvalidArgument || err.Message() != "name is required" {
		t.Errorf("validate(invalid) = %v, want invalid_argument: name is required", err)
	}
}
