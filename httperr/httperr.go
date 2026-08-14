// Package httperr provides typed errors that carry HTTP status semantics.
// Handlers return them as ordinary error values; the framework maps them to
// status codes. There is no c.Abort() and no scattered c.JSON(4xx, ...).
package httperr

import (
	"fmt"
	"net/http"
	"strings"
)

// Error is an error with an HTTP status code and an optional wrapped root
// cause. It is safe to wrap with fmt.Errorf and %w; errors.As still finds it.
type Error struct {
	code   int
	msg    string
	fields []field
	err    error
}

type field struct {
	key string
	val any
}

// New creates a typed HTTP error.
func New(code int, msg string) *Error {
	return &Error{code: code, msg: msg}
}

// Error implements the error interface.
func (e *Error) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "httperr %d: %s", e.code, e.msg)
	for _, f := range e.fields {
		fmt.Fprintf(&b, " %s=%v", f.key, f.val)
	}
	if e.err != nil {
		fmt.Fprintf(&b, ": %v", e.err)
	}
	return b.String()
}

// StatusCode returns the HTTP status carried by the error.
func (e *Error) StatusCode() int { return e.code }

// Message returns the user-facing message (safe to render in a response).
func (e *Error) Message() string { return e.msg }

// Unwrap returns the wrapped root cause.
func (e *Error) Unwrap() error { return e.err }

// Wrap returns a copy carrying err as the root cause.
func (e *Error) Wrap(err error) *Error {
	n := *e
	n.err = err
	return &n
}

// With returns a copy carrying one structured detail field.
func (e *Error) With(key string, val any) *Error {
	n := *e
	n.fields = append(append([]field(nil), e.fields...), field{key: key, val: val})
	return &n
}

// BadRequest maps a parse/validation failure to 400.
func BadRequest(err error) *Error {
	return New(http.StatusBadRequest, "bad request").Wrap(err)
}

// Unauthorized is a 401.
func Unauthorized() *Error { return New(http.StatusUnauthorized, "unauthorized") }

// Forbidden is a 403.
func Forbidden() *Error { return New(http.StatusForbidden, "forbidden") }

// NotFound is a 404.
func NotFound() *Error { return New(http.StatusNotFound, "not found") }

// Conflict is a 409 with an explicit message.
func Conflict(msg string) *Error { return New(http.StatusConflict, msg) }

// Internal wraps a server error as a 500; the root cause is not exposed to
// clients.
func Internal(err error) *Error {
	return New(http.StatusInternalServerError, "internal server error").Wrap(err)
}

// TooManyRequests is a 429.
func TooManyRequests() *Error { return New(http.StatusTooManyRequests, "too many requests") }
