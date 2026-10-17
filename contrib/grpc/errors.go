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
//   - a *connect.Error is kept as is, except that server-fault codes
//     (Internal, Unknown, DataLoss, Unavailable) get the message
//     "internal error";
//   - context cancellation and deadlines become Canceled and DeadlineExceeded;
//   - z.Issues and *errs.ValidationError become InvalidArgument with a
//     google.rpc.BadRequest detail listing the field violations;
//   - an *errs.HTTPError (or errs.StatusCoder) gets the code of its status
//     (401 Unauthenticated, 403 PermissionDenied, 404 NotFound, 409
//     AlreadyExists, 422 InvalidArgument, 429 ResourceExhausted, 503
//     Unavailable, ...) and its client-safe message below 500; from 500 up
//     the message is "internal error" (the caller logs err), and a 5xx
//     wins over the z.Issues it wraps;
//   - anything else is Internal with the message "internal error", never the
//     error text.
//
// Keyed HTTPErrors (errs.NewKey) keep their English fallback; use
// [ErrorContext] to translate them into the request locale.
func Error(err error) *connect.Error {
	return ErrorContext(context.Background(), err)
}

// ErrorContext is [Error] with the HTTPError message translated by the
// errs.Translator of ctx ([errs.HTTPError.LocalizedText]), so keyed errors
// follow the request locale. Mount and HandlerOptions use it with the
// request ctx.
func ErrorContext(ctx context.Context, err error) *connect.Error {
	if err == nil {
		return nil
	}

	if cerr, ok := errors.AsType[*connect.Error](err); ok {
		// Server-fault codes often carry a cause (connect.NewError(Internal,
		// dbErr), or a failed downstream call with its dial address): keep
		// the code, hide the text; the caller logs err.
		if isServerFault(cerr.Code()) {
			return connect.NewError(cerr.Code(), errors.New(internalMessage))
		}

		return cerr
	}

	switch {
	case errors.Is(err, context.Canceled):
		return connect.NewError(connect.CodeCanceled, errors.New("request canceled"))
	case errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, errors.New("deadline exceeded"))
	}

	// 5xx first: errs.Wrap(500, issues) carries internal data, not field
	// errors the client should fix (CWE-209).
	if status, ok := serverStatus(err); ok {
		cerr := connect.NewError(CodeOf(status), errors.New(internalMessage))
		if herr, ok := errors.AsType[*errs.HTTPError](err); ok {
			for k, v := range herr.Headers {
				cerr.Meta()[k] = slices.Clone(v)
			}
		}

		return cerr
	}

	if issues, ok := z.AsIssues(err); ok {
		return invalid(issues.Fields())
	}

	if verr, ok := errors.AsType[*errs.ValidationError](err); ok {
		return invalid(verr.Fields)
	}

	if herr, ok := errors.AsType[*errs.HTTPError](err); ok {
		cerr := connect.NewError(CodeOf(herr.Status), errors.New(herr.LocalizedText(ctx)))
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

// serverStatus returns the status of err's *errs.HTTPError (or, without
// one, its errs.StatusCoder) when it is a 5xx: its text is a server
// detail, never sent to clients.
func serverStatus(err error) (int, bool) {
	status := 0
	if herr, ok := errors.AsType[*errs.HTTPError](err); ok {
		status = herr.Status
	} else if sc, ok := errors.AsType[errs.StatusCoder](err); ok {
		status = sc.HTTPStatus()
	}

	return status, status >= http.StatusInternalServerError
}

// logsCause reports whether the client got a generic message for err, so
// the caller must log err: a server-fault code or a 5xx status.
func logsCause(err error, cerr *connect.Error) bool {
	if isServerFault(cerr.Code()) {
		return true
	}

	_, ok := serverStatus(err)

	return ok
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
