package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The contract's section 6 table, transcribed. If a code is added to the
// implementation without being added here the count assertion fails, which is
// the point: the set is closed.
var contractCodes = map[Code]int{
	CodeBadRequest:         400,
	CodeUnauthorized:       401,
	CodeInvalidCredentials: 401,
	CodeForbidden:          403,
	CodeCSRFInvalid:        403,
	CodeJobNotFound:        404,
	CodeNotFound:           404,
	CodeMethodNotAllowed:   405,
	CodeSlugConflict:       409,
	CodePayloadTooLarge:    413,
	CodeValidationFailed:   422,
	CodeRateLimited:        429,
	CodeInternalError:      500,
	CodeServiceUnavailable: 503,
}

func TestCodeStatusMatchesContract(t *testing.T) {
	for code, want := range contractCodes {
		if got := code.Status(); got != want {
			t.Errorf("%s.Status() = %d, want %d", code, got, want)
		}
		if !code.Known() {
			t.Errorf("%s is not in the implementation's closed set", code)
		}
	}

	if got, want := len(Codes()), len(contractCodes); got != want {
		t.Errorf("implementation has %d codes, contract section 6 lists %d — the set is closed", got, want)
	}
}

func TestUnknownCodeFallsBackTo500(t *testing.T) {
	var bogus Code = "TEAPOT"
	if bogus.Known() {
		t.Fatal(`Known() = true for "TEAPOT"`)
	}
	if got := bogus.Status(); got != http.StatusInternalServerError {
		t.Errorf("Status() = %d for an unknown code, want 500", got)
	}
}

func TestConstructorsProduceContractShapes(t *testing.T) {
	tests := []struct {
		name       string
		err        *AppError
		wantCode   Code
		wantStatus int
	}{
		{"bad request", BadRequest(""), CodeBadRequest, 400},
		{"unauthorized", Unauthorized(), CodeUnauthorized, 401},
		{"invalid credentials", InvalidCredentials(), CodeInvalidCredentials, 401},
		{"forbidden", Forbidden(""), CodeForbidden, 403},
		{"csrf", CSRFInvalid(), CodeCSRFInvalid, 403},
		{"job not found", JobNotFound(), CodeJobNotFound, 404},
		{"not found", NotFound(), CodeNotFound, 404},
		{"method not allowed", MethodNotAllowed(), CodeMethodNotAllowed, 405},
		{"slug conflict", SlugConflict(), CodeSlugConflict, 409},
		{"payload too large", PayloadTooLarge(), CodePayloadTooLarge, 413},
		{"validation", ValidationFailed(nil), CodeValidationFailed, 422},
		{"rate limited", RateLimited(60), CodeRateLimited, 429},
		{"internal", Internal(errors.New("boom")), CodeInternalError, 500},
		{"unavailable", ServiceUnavailable(errors.New("no route to host")), CodeServiceUnavailable, 503},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err.Code != tc.wantCode {
				t.Errorf("Code = %s, want %s", tc.err.Code, tc.wantCode)
			}
			if tc.err.Status() != tc.wantStatus {
				t.Errorf("Status() = %d, want %d", tc.err.Status(), tc.wantStatus)
			}
			if tc.err.Message == "" {
				t.Error("Message is empty; every error must carry something showable to a user")
			}
		})
	}
}

func TestRateLimitedFloorsRetryAfter(t *testing.T) {
	// Retry-After: 0 is an instruction to retry immediately, which defeats the
	// limiter it came from.
	for _, in := range []int{-5, 0} {
		if got := RateLimited(in).RetryAfter; got != 1 {
			t.Errorf("RateLimited(%d).RetryAfter = %d, want 1", in, got)
		}
	}
	if got := RateLimited(900).RetryAfter; got != 900 {
		t.Errorf("RateLimited(900).RetryAfter = %d, want 900", got)
	}
}

func TestAsAppErrorFindsWrappedError(t *testing.T) {
	inner := JobNotFound()
	wrapped := fmt.Errorf("service: loading job: %w", inner)

	got, ok := AsAppError(wrapped)
	if !ok {
		t.Fatal("AsAppError() did not find the wrapped *AppError")
	}
	if got.Code != CodeJobNotFound {
		t.Errorf("Code = %s, want %s", got.Code, CodeJobNotFound)
	}

	if _, ok := AsAppError(errors.New("plain")); ok {
		t.Error("AsAppError() reported success for a plain error")
	}
}

func TestUnwrapExposesCauseToErrorsIs(t *testing.T) {
	sentinel := errors.New("connection refused")
	err := ServiceUnavailable(sentinel)

	if !errors.Is(err, sentinel) {
		t.Error("errors.Is() cannot see the cause; the log would lose the root error")
	}
	if !strings.Contains(err.Error(), "connection refused") {
		t.Errorf("Error() = %q, want it to include the cause for the log", err.Error())
	}
}

// The most important test in this file: an internal error must expose the
// request id and nothing else.
func TestFailDoesNotLeakInternalDetail(t *testing.T) {
	const secret = "pq: relation \"jobs\" does not exist at /app/internal/jobs/repository.go:118"

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	req = req.WithContext(WithRequestID(req.Context(), "REQ123456"))

	Fail(rec, req, Internal(errors.New(secret)))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, "relation") || strings.Contains(body, "repository.go") || strings.Contains(body, "pq:") {
		t.Fatalf("response body leaked the internal cause: %s", body)
	}

	var envelope struct {
		Error struct {
			Code      Code              `json:"code"`
			Message   string            `json:"message"`
			Details   map[string]string `json:"details"`
			RequestID string            `json:"request_id"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("response is not valid JSON: %v (%s)", err, body)
	}
	if envelope.Error.Code != CodeInternalError {
		t.Errorf("code = %s, want INTERNAL_ERROR", envelope.Error.Code)
	}
	if envelope.Error.RequestID != "REQ123456" {
		t.Errorf("request_id = %q, want REQ123456 — it is the only handle a user has", envelope.Error.RequestID)
	}
	if envelope.Error.Details != nil {
		t.Errorf("details = %v on a non-validation error, want absent", envelope.Error.Details)
	}
	if ct := rec.Header().Get("Content-Type"); ct != ContentTypeJSON {
		t.Errorf("Content-Type = %q, want %q", ct, ContentTypeJSON)
	}
}

func TestFailEmitsDetailsAndRetryAfterOnlyWhereDue(t *testing.T) {
	t.Run("validation carries details", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/admin/jobs", nil)
		Fail(rec, req, ValidationFailed(map[string]string{
			"title":     "Must be between 3 and 160 characters.",
			"apply_url": "Must be an absolute https:// URL.",
		}))

		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", rec.Code)
		}
		var envelope errorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("bad JSON: %v", err)
		}
		if len(envelope.Error.Details) != 2 {
			t.Errorf("details = %v, want both fields reported at once", envelope.Error.Details)
		}
		if rec.Header().Get("Retry-After") != "" {
			t.Error("Retry-After set on a validation failure")
		}
	})

	t.Run("rate limit carries Retry-After", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/admin/login", nil)
		Fail(rec, req, RateLimited(120))

		if rec.Code != http.StatusTooManyRequests {
			t.Fatalf("status = %d, want 429", rec.Code)
		}
		if got := rec.Header().Get("Retry-After"); got != "120" {
			t.Errorf("Retry-After = %q, want 120", got)
		}
	})

	t.Run("plain error becomes a content-free 500", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
		Fail(rec, req, errors.New("nil pointer dereference in scanRecord"))

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500", rec.Code)
		}
		if strings.Contains(rec.Body.String(), "scanRecord") {
			t.Errorf("leaked the raw error: %s", rec.Body.String())
		}
	})
}

func TestDataAndCollectionEnvelopes(t *testing.T) {
	t.Run("single resource", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/jobs/x", nil)
		Data(rec, req, http.StatusOK, map[string]string{"slug": "x"})

		if got := rec.Body.String(); got != `{"data":{"slug":"x"}}` {
			t.Errorf("body = %s, want a single data envelope", got)
		}
	})

	t.Run("collection always has meta", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
		Collection(rec, req, []string{}, map[string]int{"page": 1})

		body := rec.Body.String()
		if !strings.HasPrefix(body, `{"data":[]`) {
			t.Errorf("body = %s, want data first and never a bare array", body)
		}
		if !strings.Contains(body, `"meta"`) {
			t.Errorf("body = %s, want meta present even when empty", body)
		}
	})

	t.Run("no content has no body", func(t *testing.T) {
		rec := httptest.NewRecorder()
		NoContent(rec)
		if rec.Code != http.StatusNoContent {
			t.Errorf("status = %d, want 204", rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("204 wrote a body: %q", rec.Body.String())
		}
	})
}

func TestDecodeJSON(t *testing.T) {
	type payload struct {
		Title string `json:"title"`
	}

	tests := []struct {
		name        string
		body        string
		contentType string
		wantCode    Code
		wantTitle   string
	}{
		{name: "valid", body: `{"title":"ok"}`, contentType: "application/json", wantTitle: "ok"},
		{name: "unknown fields ignored", body: `{"title":"ok","future":1}`, contentType: "application/json", wantTitle: "ok"},
		{name: "charset parameter accepted", body: `{"title":"ok"}`, contentType: "application/json; charset=utf-8", wantTitle: "ok"},
		{name: "missing content type accepted", body: `{"title":"ok"}`, wantTitle: "ok"},
		{name: "form content type rejected", body: `title=ok`, contentType: "application/x-www-form-urlencoded", wantCode: CodeBadRequest},
		{name: "malformed json", body: `{"title":`, contentType: "application/json", wantCode: CodeBadRequest},
		{name: "empty body", body: ``, contentType: "application/json", wantCode: CodeBadRequest},
		{name: "two objects", body: `{"title":"a"}{"title":"b"}`, contentType: "application/json", wantCode: CodeBadRequest},
		{
			name:        "over the 64 KiB cap",
			body:        `{"title":"` + strings.Repeat("x", int(MaxBodyBytes)+64) + `"}`,
			contentType: "application/json",
			wantCode:    CodePayloadTooLarge,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/admin/jobs", strings.NewReader(tc.body))
			if tc.contentType != "" {
				req.Header.Set("Content-Type", tc.contentType)
			}

			var dst payload
			err := DecodeJSON(rec, req, &dst)

			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("DecodeJSON() = %v, want nil", err)
				}
				if dst.Title != tc.wantTitle {
					t.Errorf("Title = %q, want %q", dst.Title, tc.wantTitle)
				}
				return
			}

			appErr, ok := AsAppError(err)
			if !ok {
				t.Fatalf("DecodeJSON() = %v, want an *AppError with code %s", err, tc.wantCode)
			}
			if appErr.Code != tc.wantCode {
				t.Errorf("code = %s, want %s", appErr.Code, tc.wantCode)
			}
		})
	}
}

func TestRequestIDMiddleware(t *testing.T) {
	var seen string
	handler := RequestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = RequestID(r.Context())
	}))

	t.Run("generates one when absent", func(t *testing.T) {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))

		if seen == "" {
			t.Fatal("no request id on the context")
		}
		if rec.Header().Get(HeaderRequestID) != seen {
			t.Errorf("header %q != context %q", rec.Header().Get(HeaderRequestID), seen)
		}
	})

	t.Run("honours a safe inbound id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		req.Header.Set(HeaderRequestID, "frontend-abc123")
		handler.ServeHTTP(httptest.NewRecorder(), req)

		if seen != "frontend-abc123" {
			t.Errorf("request id = %q, want the propagated value", seen)
		}
	})

	t.Run("rejects an id that could forge a log line", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		req.Header.Set(HeaderRequestID, "abc123 level=ERROR msg=fake")
		handler.ServeHTTP(httptest.NewRecorder(), req)

		if strings.Contains(seen, " ") {
			t.Errorf("request id = %q, want a generated id instead of attacker text", seen)
		}
	})

	t.Run("rejects an id that is too short or too long", func(t *testing.T) {
		for _, bad := range []string{"abc", strings.Repeat("a", 65)} {
			req := httptest.NewRequest(http.MethodGet, "/health", nil)
			req.Header.Set(HeaderRequestID, bad)
			handler.ServeHTTP(httptest.NewRecorder(), req)
			if seen == bad {
				t.Errorf("accepted out-of-range request id %q", bad)
			}
		}
	})
}

func TestNewRequestIDIsUnique(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for range 1000 {
		id := NewRequestID()
		if len(id) != 16 {
			t.Fatalf("NewRequestID() = %q, want 16 characters", id)
		}
		if seen[id] {
			t.Fatalf("NewRequestID() collided on %q within 1000 draws", id)
		}
		seen[id] = true
	}
}
