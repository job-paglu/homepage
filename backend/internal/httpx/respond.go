package httpx

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
)

// ContentTypeJSON is set on every response with a body. The router relies on
// this exact value to tell its own 404/405 replies apart from net/http's
// plain-text ones, so it is a constant rather than a literal.
const ContentTypeJSON = "application/json; charset=utf-8"

// MaxBodyBytes is the request body cap from contract section 1: 64 KiB.
const MaxBodyBytes int64 = 64 << 10

// envelope shapes are private: callers go through the writers below so a bare
// array or a naked error string can never be emitted.

type dataEnvelope struct {
	Data any `json:"data"`
}

type collectionEnvelope struct {
	Data any `json:"data"`
	Meta any `json:"meta"`
}

type errorBody struct {
	Code      Code              `json:"code"`
	Message   string            `json:"message"`
	Details   map[string]string `json:"details,omitempty"`
	RequestID string            `json:"request_id,omitempty"`
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

// Data writes `{"data": ...}` with the given status.
func Data(w http.ResponseWriter, r *http.Request, status int, data any) {
	writeJSON(w, r, status, dataEnvelope{Data: data})
}

// Collection writes `{"data": [...], "meta": {...}}`.
//
// data must marshal to a JSON array. A nil slice marshals to `null`, which the
// frontend would have to special-case, so callers are expected to pass an empty
// slice — this is asserted in the integration tests rather than patched here.
func Collection(w http.ResponseWriter, r *http.Request, data any, meta any) {
	writeJSON(w, r, http.StatusOK, collectionEnvelope{Data: data, Meta: meta})
}

// NoContent writes 204 with no body at all (contract section 6).
func NoContent(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNoContent)
}

// Fail writes the error envelope for any error.
//
// An *AppError anywhere in the chain decides the code and the client-visible
// message. Anything else is a bug that reached the boundary and becomes a
// content-free 500. Either way the full chain — including the cause an
// *AppError carries — is logged, never serialised.
func Fail(w http.ResponseWriter, r *http.Request, err error) {
	appErr, ok := AsAppError(err)
	if !ok {
		appErr = Internal(err)
	}

	requestID := RequestID(r.Context())
	logFailure(r, appErr, requestID)

	if appErr.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(appErr.RetryAfter))
	}

	// Details only ever accompanies VALIDATION_FAILED (contract section 1.2).
	details := appErr.Details
	if appErr.Code != CodeValidationFailed {
		details = nil
	}

	writeJSON(w, r, appErr.Status(), errorEnvelope{Error: errorBody{
		Code:      appErr.Code,
		Message:   appErr.Message,
		Details:   details,
		RequestID: requestID,
	}})
}

// logFailure records server-side faults at error level and client mistakes at
// debug, so a noisy scanner probing for /wp-admin cannot bury a real 500.
func logFailure(r *http.Request, appErr *AppError, requestID string) {
	attrs := []any{
		slog.String("request_id", requestID),
		slog.String("code", string(appErr.Code)),
		slog.String("method", r.Method),
		slog.String("path", r.URL.Path),
		slog.Int("status", appErr.Status()),
	}
	// The cause is the whole point of the log line for a 5xx. It is the only
	// place the underlying driver or SQL error is ever rendered.
	if cause := appErr.Unwrap(); cause != nil {
		attrs = append(attrs, slog.String("cause", cause.Error()))
	}

	switch {
	case appErr.Status() >= 500:
		slog.ErrorContext(r.Context(), "request failed", attrs...)
	case appErr.Code == CodeRateLimited, appErr.Code == CodeCSRFInvalid, appErr.Code == CodeInvalidCredentials:
		// Security-relevant but caller-caused: worth seeing by default.
		slog.WarnContext(r.Context(), "request rejected", attrs...)
	default:
		slog.DebugContext(r.Context(), "request rejected", attrs...)
	}
}

func writeJSON(w http.ResponseWriter, r *http.Request, status int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		// Marshalling our own envelope should be impossible. If it happens the
		// response is already committed to being a failure, so emit a minimal
		// hand-written body rather than recursing into Fail.
		slog.ErrorContext(r.Context(), "failed to marshal response envelope", slog.String("error", err.Error()))
		w.Header().Set("Content-Type", ContentTypeJSON)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":{"code":"INTERNAL_ERROR","message":"Something went wrong on our side. Please try again."}}`))
		return
	}

	w.Header().Set("Content-Type", ContentTypeJSON)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// DecodeJSON reads and decodes a request body into dst.
//
// It returns an *AppError ready to hand to Fail, so no handler has to work out
// whether a decode failure is a 400 or a 413. Unknown fields are accepted and
// ignored, which the contract requires so a newer frontend can post a field an
// older backend does not know yet.
func DecodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	if r.Body == nil {
		return BadRequest("A JSON request body is required.")
	}

	// Content-Type is checked loosely: charset parameters and a missing type are
	// both common from legitimate clients, but a form post is always a mistake
	// here and rejecting it early gives a clearer error than a decode failure.
	if ct := r.Header.Get("Content-Type"); ct != "" {
		mediaType := strings.TrimSpace(strings.Split(ct, ";")[0])
		if !strings.EqualFold(mediaType, "application/json") {
			return BadRequest("Content-Type must be application/json.")
		}
	}

	// Defence in depth: the body-limit middleware already wrapped the body, but
	// a handler reached directly in a test still gets the cap.
	limited := http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	decoder := json.NewDecoder(limited)

	if err := decoder.Decode(dst); err != nil {
		var maxBytes *http.MaxBytesError
		switch {
		case errors.As(err, &maxBytes):
			return PayloadTooLarge().WithCause(err)
		case errors.Is(err, io.EOF):
			return BadRequest("A JSON request body is required.")
		default:
			// The decoder's message can quote the body, which may contain a
			// password, so it is logged as the cause and never surfaced.
			return BadRequest("The request body is not valid JSON.").WithCause(err)
		}
	}

	// A second value in the stream means the client sent something like
	// `{}{}`; accepting the first object silently would hide a real bug.
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return BadRequest("The request body must contain exactly one JSON object.")
	}
	return nil
}
