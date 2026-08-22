// Package httpx holds the HTTP boundary vocabulary: the JSON envelope, the
// closed set of error codes, and request identifiers.
//
// It deliberately knows nothing about jobs, admins or SQL, so every other
// package can depend on it without creating a cycle.
package httpx

import (
	"errors"
	"fmt"
	"net/http"
)

// Code is a machine-readable error code from API-CONTRACT.md section 6.
//
// The set is closed. Clients switch on the code and never on the message, so
// adding a member is an API change that belongs in the contract first.
type Code string

// The complete set. Any value outside it is a bug.
const (
	CodeBadRequest         Code = "BAD_REQUEST"
	CodeUnauthorized       Code = "UNAUTHORIZED"
	CodeInvalidCredentials Code = "INVALID_CREDENTIALS"
	CodeForbidden          Code = "FORBIDDEN"
	CodeCSRFInvalid        Code = "CSRF_INVALID"
	CodeJobNotFound        Code = "JOB_NOT_FOUND"
	CodeNotFound           Code = "NOT_FOUND"
	CodeMethodNotAllowed   Code = "METHOD_NOT_ALLOWED"
	CodeSlugConflict       Code = "SLUG_CONFLICT"
	CodePayloadTooLarge    Code = "PAYLOAD_TOO_LARGE"
	CodeValidationFailed   Code = "VALIDATION_FAILED"
	CodeRateLimited        Code = "RATE_LIMITED"
	CodeInternalError      Code = "INTERNAL_ERROR"
	CodeServiceUnavailable Code = "SERVICE_UNAVAILABLE"
)

// codeStatus is the single mapping from code to HTTP status. Handlers never
// choose a status themselves, so a code and its status can never drift apart.
var codeStatus = map[Code]int{
	CodeBadRequest:         http.StatusBadRequest,
	CodeUnauthorized:       http.StatusUnauthorized,
	CodeInvalidCredentials: http.StatusUnauthorized,
	CodeForbidden:          http.StatusForbidden,
	CodeCSRFInvalid:        http.StatusForbidden,
	CodeJobNotFound:        http.StatusNotFound,
	CodeNotFound:           http.StatusNotFound,
	CodeMethodNotAllowed:   http.StatusMethodNotAllowed,
	CodeSlugConflict:       http.StatusConflict,
	CodePayloadTooLarge:    http.StatusRequestEntityTooLarge,
	CodeValidationFailed:   http.StatusUnprocessableEntity,
	CodeRateLimited:        http.StatusTooManyRequests,
	CodeInternalError:      http.StatusInternalServerError,
	CodeServiceUnavailable: http.StatusServiceUnavailable,
}

// Status returns the HTTP status for the code, or 500 for an unknown one.
func (c Code) Status() int {
	if status, ok := codeStatus[c]; ok {
		return status
	}
	return http.StatusInternalServerError
}

// Known reports whether the code is a member of the contract's closed set.
func (c Code) Known() bool {
	_, ok := codeStatus[c]
	return ok
}

// Codes returns every code in the closed set. Used by tests to assert that the
// implementation and the contract agree on the size of the set.
func Codes() []Code {
	out := make([]Code, 0, len(codeStatus))
	for c := range codeStatus {
		out = append(out, c)
	}
	return out
}

// AppError is an error that already knows how it should look to a client.
//
// The distinction that matters: Message and Details cross the wire, cause never
// does. A repository error, a driver error or a SQL fragment reaches the log via
// Unwrap and stops there.
type AppError struct {
	Code    Code
	Message string
	// Details is populated only for CodeValidationFailed and maps field name to
	// one human-readable sentence.
	Details map[string]string
	// RetryAfter is seconds, emitted as the Retry-After header for
	// CodeRateLimited only.
	RetryAfter int

	cause error
}

// Error implements error. It includes the cause because this string is for
// logs, never for a response body.
func (e *AppError) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap exposes the internal cause to errors.Is/errors.As and to the logger.
func (e *AppError) Unwrap() error { return e.cause }

// Status is the HTTP status this error should be sent with.
func (e *AppError) Status() int { return e.Code.Status() }

// WithCause attaches an internal error for logging. It returns the receiver so
// it can be chained onto a constructor.
func (e *AppError) WithCause(err error) *AppError {
	e.cause = err
	return e
}

// AsAppError extracts an *AppError from anywhere in an error chain.
func AsAppError(err error) (*AppError, bool) {
	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr, true
	}
	return nil, false
}

// ------------------------------------------------------------- constructors
//
// Messages are written for a human reading a UI. They never contain SQL, a file
// path, a stack frame, a hostname or a field value supplied by the caller.

// BadRequest is 400: malformed JSON, an unparseable query parameter, a bad UUID.
func BadRequest(message string) *AppError {
	if message == "" {
		message = "The request could not be understood."
	}
	return &AppError{Code: CodeBadRequest, Message: message}
}

// Unauthorized is 401 for a missing, expired or unknown session.
func Unauthorized() *AppError {
	return &AppError{Code: CodeUnauthorized, Message: "You are not signed in."}
}

// InvalidCredentials is 401 for a failed login.
//
// The message is identical for an unknown email and a wrong password, because a
// different message is a user-enumeration oracle (contract section 4).
func InvalidCredentials() *AppError {
	return &AppError{Code: CodeInvalidCredentials, Message: "Incorrect email or password."}
}

// Forbidden is 403 for an authenticated caller without permission.
func Forbidden(message string) *AppError {
	if message == "" {
		message = "You do not have permission to do that."
	}
	return &AppError{Code: CodeForbidden, Message: message}
}

// CSRFInvalid is 403 for a missing or mismatched CSRF token, or a bad Origin.
//
// The message does not say which of the two failed: telling an attacker whether
// their Origin or their token was the problem helps only them.
func CSRFInvalid() *AppError {
	return &AppError{Code: CodeCSRFInvalid, Message: "Your session could not be verified. Please sign in again."}
}

// JobNotFound is 404 for a job that does not exist or is not publicly visible.
func JobNotFound() *AppError {
	return &AppError{Code: CodeJobNotFound, Message: "That job could not be found."}
}

// NotFound is 404 for an unknown route.
func NotFound() *AppError {
	return &AppError{Code: CodeNotFound, Message: "That resource could not be found."}
}

// MethodNotAllowed is 405 for a known path with the wrong method.
func MethodNotAllowed() *AppError {
	return &AppError{Code: CodeMethodNotAllowed, Message: "That method is not allowed on this resource."}
}

// SlugConflict is 409, raised only when an explicitly supplied slug is taken.
// A derived slug is de-duplicated instead and never reaches this.
func SlugConflict() *AppError {
	return &AppError{Code: CodeSlugConflict, Message: "That URL slug is already in use."}
}

// PayloadTooLarge is 413 for a body over the 64 KiB cap.
func PayloadTooLarge() *AppError {
	return &AppError{Code: CodePayloadTooLarge, Message: "The request body is too large."}
}

// ValidationFailed is 422 with a per-field details map. Every invalid field is
// reported at once (contract section 9).
func ValidationFailed(details map[string]string) *AppError {
	if details == nil {
		details = map[string]string{}
	}
	return &AppError{
		Code:    CodeValidationFailed,
		Message: "One or more fields are invalid.",
		Details: details,
	}
}

// RateLimited is 429. retryAfterSeconds is emitted as the Retry-After header
// and is floored at 1, because Retry-After: 0 tells a client to hammer.
func RateLimited(retryAfterSeconds int) *AppError {
	if retryAfterSeconds < 1 {
		retryAfterSeconds = 1
	}
	return &AppError{
		Code:       CodeRateLimited,
		Message:    "Too many attempts. Please wait and try again.",
		RetryAfter: retryAfterSeconds,
	}
}

// Internal is 500. The message is fixed and content-free; the cause goes to the
// log and the client gets only the request ID to quote in a bug report.
func Internal(cause error) *AppError {
	return &AppError{
		Code:    CodeInternalError,
		Message: "Something went wrong on our side. Please try again.",
		cause:   cause,
	}
}

// ServiceUnavailable is 503, used when Postgres is unreachable or a query
// exceeds its timeout. It is deliberately distinct from Internal: this one is
// worth retrying and the frontend renders it as such.
func ServiceUnavailable(cause error) *AppError {
	return &AppError{
		Code:    CodeServiceUnavailable,
		Message: "The service is temporarily unavailable. Please try again shortly.",
		cause:   cause,
	}
}
