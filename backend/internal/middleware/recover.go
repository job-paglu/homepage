package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/job-paglu/homepage/backend/internal/httpx"
)

// Recover turns a panic into the same 500 envelope every other error uses.
//
// A panic in a request path is a bug, and the response must not advertise that:
// the stack goes to the log with the request id, and the client gets the
// content-free INTERNAL_ERROR body.
//
// http.ErrAbortHandler is re-panicked untouched. net/http uses it to abort a
// connection on purpose, and swallowing it would turn a deliberate abort into a
// bogus 500 in the log.
func Recover(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}
				if recovered == http.ErrAbortHandler {
					panic(recovered)
				}

				requestID := httpx.RequestID(r.Context())
				logger.ErrorContext(r.Context(), "panic recovered",
					slog.String("request_id", requestID),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
					slog.Any("panic", recovered),
					slog.String("stack", string(debug.Stack())),
				)

				// If the handler already committed a status there is nothing
				// left to say on the wire; the log line above is the record.
				if committed, ok := w.(interface{ Wrote() bool }); ok && committed.Wrote() {
					return
				}

				httpx.Fail(w, r, httpx.Internal(fmt.Errorf("panic: %v", recovered)))
			}()

			next.ServeHTTP(w, r)
		})
	}
}
