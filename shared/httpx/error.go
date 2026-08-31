// Package httpx maps domain errors onto HTTP responses for Gin handlers.
package httpx

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// Sentinel errors that usecases return to describe a failure class without
// knowing anything about HTTP. Repositories translate driver errors into
// these; handlers pass them to Fail.
var (
	ErrInternalServer      = errors.New("internal server error")
	ErrBadRequest          = errors.New("bad request")
	ErrValidation          = errors.New("validation failed")
	ErrNotFound            = errors.New("not found")
	ErrConflict            = errors.New("resource conflict")
	ErrUnauthorized        = errors.New("unauthorized access")
	ErrForbidden           = errors.New("forbidden")
	ErrUnprocessableEntity = errors.New("unprocessable entity")
	ErrTooManyRequests     = errors.New("too many requests")
	ErrRequestTimeout      = errors.New("request timeout")
	ErrBadGateway          = errors.New("bad gateway")
	ErrServiceUnavailable  = errors.New("service unavailable")
	ErrGatewayTimeout      = errors.New("gateway timeout")
)

// msgInternal is the only thing a client is ever told about a 5xx: the specific
// cause goes to the log, never over the wire.
const msgInternal = "internal server error"

// Error carries a sentinel plus a caller-facing message and optional field
// details. It implements Unwrap so errors.Is(err, ErrNotFound) keeps working
// after wrapping — the reference project compared errors with == and so lost
// the match as soon as an error was wrapped.
type Error struct {
	Kind    error          // one of the sentinels above
	Message string         // safe to show the client
	Details map[string]any // optional per-field detail
	cause   error          // internal, never serialised
}

func (e *Error) Error() string {
	if e.Message == "" {
		return e.Kind.Error()
	}
	return fmt.Sprintf("%s: %s", e.Kind, e.Message)
}

// Unwrap exposes the sentinel so errors.Is matches it.
func (e *Error) Unwrap() error { return e.Kind }

// Cause returns the wrapped internal error, for logging only.
func (e *Error) Cause() error { return e.cause }

// Wrap attaches an internal cause that is logged but never sent to the client.
func (e *Error) Wrap(cause error) *Error {
	e.cause = cause
	return e
}

// WithDetails attaches structured field detail, typically validation errors.
func (e *Error) WithDetails(d map[string]any) *Error {
	e.Details = d
	return e
}

// New builds an Error of the given kind.
func New(kind error, message string) *Error {
	return &Error{Kind: kind, Message: message}
}

// Constructors for the common cases.
func NotFound(msg string) *Error     { return New(ErrNotFound, msg) }
func BadRequest(msg string) *Error   { return New(ErrBadRequest, msg) }
func Conflict(msg string) *Error     { return New(ErrConflict, msg) }
func Unauthorized(msg string) *Error { return New(ErrUnauthorized, msg) }
func Forbidden(msg string) *Error    { return New(ErrForbidden, msg) }
func Internal(cause error) *Error {
	return (&Error{Kind: ErrInternalServer, Message: msgInternal}).Wrap(cause)
}

// statusByKind drives both the status code and the machine-readable code.
var statusByKind = []struct {
	kind   error
	status int
	code   string
}{
	{ErrBadRequest, http.StatusBadRequest, "BAD_REQUEST"},
	{ErrValidation, http.StatusUnprocessableEntity, "VALIDATION_FAILED"},
	{ErrUnauthorized, http.StatusUnauthorized, "UNAUTHORIZED"},
	{ErrForbidden, http.StatusForbidden, "FORBIDDEN"},
	{ErrNotFound, http.StatusNotFound, "NOT_FOUND"},
	{ErrConflict, http.StatusConflict, "CONFLICT"},
	{ErrUnprocessableEntity, http.StatusUnprocessableEntity, "UNPROCESSABLE_ENTITY"},
	{ErrTooManyRequests, http.StatusTooManyRequests, "TOO_MANY_REQUESTS"},
	{ErrRequestTimeout, http.StatusRequestTimeout, "REQUEST_TIMEOUT"},
	{ErrBadGateway, http.StatusBadGateway, "BAD_GATEWAY"},
	{ErrServiceUnavailable, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE"},
	{ErrGatewayTimeout, http.StatusGatewayTimeout, "GATEWAY_TIMEOUT"},
	{ErrInternalServer, http.StatusInternalServerError, "INTERNAL_ERROR"},
}

// StatusCode resolves err to an HTTP status, defaulting to 500. It uses
// errors.Is throughout, so wrapped and fmt.Errorf-decorated errors still map.
func StatusCode(err error) int {
	status, _ := classify(err)
	return status
}

// Code resolves err to a stable machine-readable error code.
func Code(err error) string {
	_, code := classify(err)
	return code
}

func classify(err error) (int, string) {
	if err == nil {
		return http.StatusOK, ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return http.StatusGatewayTimeout, "GATEWAY_TIMEOUT"
	}
	if errors.Is(err, context.Canceled) {
		// The client hung up; 499 is nginx's convention for it.
		return 499, "CLIENT_CLOSED_REQUEST"
	}
	for _, m := range statusByKind {
		if errors.Is(err, m.kind) {
			return m.status, m.code
		}
	}
	return http.StatusInternalServerError, "INTERNAL_ERROR"
}
