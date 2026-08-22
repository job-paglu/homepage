package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/job-paglu/homepage/backend/internal/httpx"
)

// recorder observes the status and size of a response without buffering it.
//
// It also lets Recover, which sits inside this middleware, ask whether the
// response has already been committed — a panic after the first byte cannot be
// turned into a clean 500.
type recorder struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (r *recorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.status = status
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *recorder) Write(b []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(b)
	r.bytes += n
	return n, err
}

// Wrote reports whether a status line has already gone out. Recover depends on
// this via an interface assertion rather than an import, so the two middlewares
// stay independent.
func (r *recorder) Wrote() bool { return r.wroteHeader }

// Unwrap lets net/http's ResponseController reach the underlying writer, which
// is what keeps flushing and deadline control working through the wrapper.
func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Logging emits one structured line per request.
//
// Deliberately absent from the line: the query string, any header, and the
// request body. A query string can carry a token a client appended by mistake,
// and a header set can carry the session cookie — neither is worth the
// convenience (rule R2).
func Logging(logger *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &recorder{ResponseWriter: w, status: http.StatusOK}

			next.ServeHTTP(rec, r)

			elapsed := time.Since(start)
			attrs := []any{
				slog.String("request_id", httpx.RequestID(r.Context())),
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int64("duration_ms", elapsed.Milliseconds()),
				slog.Int("bytes", rec.bytes),
				slog.String("ip", RedactIP(ClientIP(r))),
			}

			switch {
			case rec.status >= 500:
				logger.ErrorContext(r.Context(), "request", attrs...)
			case rec.status >= 400:
				logger.WarnContext(r.Context(), "request", attrs...)
			default:
				logger.InfoContext(r.Context(), "request", attrs...)
			}
		})
	}
}

// ClientIP returns the caller's address.
//
// Behind any managed platform's edge the real client is the leftmost
// X-Forwarded-For entry, so that is what is used. That value is
// technically client-settable: a caller who forges it can only make their own
// login attempts count against a different bucket, and the per-email limiter —
// which no header can influence — still stops the attack. Documented rather
// than pretended away.
func ClientIP(r *http.Request) string {
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		first := strings.TrimSpace(strings.Split(forwarded, ",")[0])
		if first != "" {
			return first
		}
	}
	if real := strings.TrimSpace(r.Header.Get("X-Real-Ip")); real != "" {
		return real
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// RedactIP truncates an address so a log is useful for rate-limit forensics but
// is not a record of who read which job posting.
//
// IPv4 loses the final octet, IPv6 everything past the /48.
func RedactIP(raw string) string {
	ip := net.ParseIP(raw)
	if ip == nil {
		return "unknown"
	}
	if v4 := ip.To4(); v4 != nil {
		return net.IP{v4[0], v4[1], v4[2], 0}.String() + "/24"
	}
	masked := ip.Mask(net.CIDRMask(48, 128))
	return masked.String() + "/48"
}
