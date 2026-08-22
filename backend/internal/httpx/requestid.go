package httpx

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"net/http"
	"strings"
)

// HeaderRequestID is the response header, and the header a client may send to
// propagate its own correlation id.
const HeaderRequestID = "X-Request-Id"

type requestIDKey struct{}

// requestIDEncoding is Crockford-ish base32 without padding: case-insensitive,
// URL-safe, and readable enough that a user can retype it from a screenshot.
var requestIDEncoding = base32.NewEncoding("0123456789ABCDEFGHJKMNPQRSTVWXYZ").WithPadding(base32.NoPadding)

// NewRequestID returns a fresh 80-bit identifier.
//
// It is not a UUID and does not need to be: it only has to be unique enough
// that two concurrent requests do not collide in a log search.
func NewRequestID() string {
	var buf [10]byte
	if _, err := rand.Read(buf[:]); err != nil {
		// crypto/rand does not fail in practice, and a request id is not a
		// security boundary, so a degraded id beats failing the request.
		return "0000000000000000" // 10 bytes of base32 is 16 characters

	}
	return requestIDEncoding.EncodeToString(buf[:])
}

// WithRequestID stores an id on the context.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID returns the id stored on the context, or "" if there is none.
func RequestID(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey{}).(string); ok {
		return id
	}
	return ""
}

// RequestIDMiddleware assigns every request an id, stores it on the context and
// echoes it in the response header before any handler can write.
//
// An inbound X-Request-Id is honoured so a trace can span the frontend and the
// API, but only after sanitising: this value ends up in log lines, and an
// attacker-controlled newline in a log line can forge log entries.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := sanitiseRequestID(r.Header.Get(HeaderRequestID))
		if id == "" {
			id = NewRequestID()
		}
		w.Header().Set(HeaderRequestID, id)
		next.ServeHTTP(w, r.WithContext(WithRequestID(r.Context(), id)))
	})
}

// sanitiseRequestID accepts 8..64 characters of [A-Za-z0-9_-] and rejects
// everything else outright rather than trying to clean it up.
func sanitiseRequestID(raw string) string {
	raw = strings.TrimSpace(raw)
	if len(raw) < 8 || len(raw) > 64 {
		return ""
	}
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return ""
		}
	}
	return raw
}
