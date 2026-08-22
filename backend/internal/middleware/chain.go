// Package middleware holds the cross-cutting HTTP concerns: request logging,
// panic recovery, security headers, CORS, the body cap and rate limiting.
//
// Nothing here knows about jobs or admins. Anything that needs the session
// belongs in internal/auth, which owns that state.
package middleware

import "net/http"

// Middleware wraps a handler. The plain net/http signature is used deliberately
// so any function of that shape — including httpx.RequestIDMiddleware — can be
// passed to Chain without an adapter.
type Middleware func(http.Handler) http.Handler

// Chain wraps h so that the first middleware is the outermost.
//
// Reading order therefore matches execution order: Chain(h, A, B, C) runs A,
// then B, then C, then h, and unwinds in reverse. Getting this backwards is the
// classic middleware bug, so it is stated rather than implied.
func Chain(h http.Handler, mw ...Middleware) http.Handler {
	for i := len(mw) - 1; i >= 0; i-- {
		if mw[i] == nil {
			continue
		}
		h = mw[i](h)
	}
	return h
}
