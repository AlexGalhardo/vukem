package grpc

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"slices"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/rpc/errdetails"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/z"
)

// internalMessage is all clients learn about unexpected errors and panics;
// the cause is logged server-side only (CWE-209).
const internalMessage = "internal error"

// Error converts err into the RPC error clients receive:
//   - a *connect.Error is kept as is;
//   - context cancellation and deadlines become Canceled and DeadlineExceeded;
//   - z.Issues and *errs.ValidationError become InvalidArgument with a
//     google.rpc.BadRequest detail listing the field violations;
//   - an *errs.HTTPError (or errs.StatusCoder) gets the code of its status
//     (401 Unauthenticated, 403 PermissionDenied, 404 NotFound, 409
//     AlreadyExists, 422 InvalidArgument, 429 ResourceExhausted, 503
//     Unavailable, ...) and its client-safe message;
//   - anything else is Internal with the message "internal error", never the
//     error text.
func Error(err error) *connect.Error {
	if err == nil {
		return nil
	}

	if cerr, ok := errors.AsType[*connect.Error](err); ok {
		return cerr
	}

	switch {
	case errors.Is(err, context.Canceled):
		return connect.NewError(connect.CodeCanceled, errors.New("request canceled"))
	case errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, errors.New("deadline exceeded"))
	}

	if issues, ok := z.AsIssues(err); ok {
		return invalid(issues.Fields())
	}

	if verr, ok := errors.AsType[*errs.ValidationError](err); ok {
		return invalid(verr.Fields)
	}

	if herr, ok := errors.AsType[*errs.HTTPError](err); ok {
		cerr := connect.NewError(CodeOf(herr.Status), errors.New(herr.Text()))
		for k, v := range herr.Headers {
			cerr.Meta()[k] = slices.Clone(v)
		}

		return cerr
	}

	if sc, ok := errors.AsType[errs.StatusCoder](err); ok {
		return connect.NewError(CodeOf(sc.HTTPStatus()), errors.New(http.StatusText(sc.HTTPStatus())))
	}

	return connect.NewError(connect.CodeInternal, errors.New(internalMessage))
}

// CodeOf maps an HTTP status to the closest RPC code.
func CodeOf(status int) connect.Code {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity, http.StatusUnsupportedMediaType:
		return connect.CodeInvalidArgument
	case http.StatusUnauthorized:
		return connect.CodeUnauthenticated
	case http.StatusForbidden:
		return connect.CodePermissionDenied
	case http.StatusNotFound, http.StatusGone:
		return connect.CodeNotFound
	case http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return connect.CodeUnimplemented
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return connect.CodeDeadlineExceeded
	case http.StatusConflict:
		return connect.CodeAlreadyExists
	case http.StatusPreconditionFailed, http.StatusPreconditionRequired:
		return connect.CodeFailedPrecondition
	case http.StatusRequestEntityTooLarge, http.StatusTooManyRequests:
		return connect.CodeResourceExhausted
	case 499: // client closed request (nginx)
		return connect.CodeCanceled
	case http.StatusServiceUnavailable:
		return connect.CodeUnavailable
	}

	if status >= 400 && status < 500 {
		return connect.CodeFailedPrecondition
	}

	return connect.CodeInternal
}

func invalid(fields map[string][]string) *connect.Error {
	cerr := connect.NewError(connect.CodeInvalidArgument, errors.New("invalid request"))

	br := &errdetails.BadRequest{}
	for _, field := range slices.Sorted(maps.Keys(fields)) {
		for _, msg := range fields[field] {
			br.FieldViolations = append(br.FieldViolations, &errdetails.BadRequest_FieldViolation{
				Field:       field,
				Description: msg,
			})
		}
	}

	if detail, err := connect.NewErrorDetail(br); err == nil {
		cerr.AddDetail(detail)
	}

	return cerr
}

// isServerFault reports codes whose cause must be logged: the client only
// sees a generic message.
func isServerFault(c connect.Code) bool {
	switch c {
	case connect.CodeInternal, connect.CodeUnknown, connect.CodeDataLoss, connect.CodeUnavailable:
		return true
	default:
		return false
	}
}
