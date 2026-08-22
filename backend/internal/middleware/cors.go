package middleware

import (
	"net/http"
	"strings"

	"github.com/job-paglu/homepage/backend/internal/httpx"
)

// CORSOptions configures the single allowed credentialed origin plus the paths
// that may be read anonymously by anyone.
type CORSOptions struct {
	// AllowedOrigin is the exact scheme://host[:port] of the frontend. Never a
	// wildcard, never a list: one deployment, one origin.
	AllowedOrigin string
	// PublicPathPrefixes may be read cross-origin without credentials, so the
	// job data stays embeddable (contract section 5).
	PublicPathPrefixes []string
}

const (
	corsAllowMethods = "GET, POST, PUT, DELETE, OPTIONS"
	corsAllowHeaders = "Content-Type, X-CSRF-Token, X-Request-Id"
	corsMaxAge       = "600"
)

// CORS answers preflights and attaches the response headers.
//
// Two distinct modes, and keeping them apart is the whole point:
//
//   - The configured frontend origin gets its own origin echoed plus
//     Allow-Credentials: true, so the admin session cookie is usable.
//   - Any other origin gets `*` on public GET paths and no credentials. A
//     wildcard and credentials are mutually exclusive in the browser, which is
//     exactly the property being relied on: an arbitrary site can read published
//     jobs and can never attach a cookie.
//
// An origin that is neither gets no CORS headers at all, and the browser blocks
// the read. That is a client-side outcome by design — returning 403 here would
// break server-to-server callers, which are not subject to CORS.
//
// # Why this is a fallback rather than the primary control
//
// In the recommended topology the site and its /api/* path share one origin: the
// browser talks to https://home.jobpaglu.eu.cc and /api/* is forwarded to the
// API deployment server-to-server — by a platform rewrite in production, or by
// the Astro catch-all route in frontend/src/pages/api/[...path].ts.
// A same-origin fetch is not a CORS request at all, so **for admin traffic this
// middleware never engages** — no preflight, no Allow-Credentials, no Vary
// bookkeeping in the credential path. That is the point: the fewer moving parts
// between a session cookie and the handler, the fewer ways it can go wrong, and
// a cookie on its own origin is genuinely first-party rather than
// same-site-by-argument.
//
// It is deliberately kept, not deleted. A two-host deployment (site on one
// origin, API on api.<domain>) is still supported, and the moment someone
// switches PUBLIC_API_BASE_URL back to the API host this code is what makes the
// dashboard work. Public anonymous reads also still rely on the `*` branch
// regardless of topology, so the file is load-bearing either way.
func CORS(o CORSOptions) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")

			// Vary is unconditional: without it a CDN can cache the response it
			// built for the frontend origin and serve it to everyone else.
			w.Header().Add("Vary", "Origin")

			switch {
			case origin != "" && origin == o.AllowedOrigin:
				h := w.Header()
				h.Set("Access-Control-Allow-Origin", origin)
				h.Set("Access-Control-Allow-Credentials", "true")

			case origin != "" && isPublicRead(r, o.PublicPathPrefixes):
				w.Header().Set("Access-Control-Allow-Origin", "*")
			}

			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				handlePreflight(w, r, o)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func handlePreflight(w http.ResponseWriter, r *http.Request, o CORSOptions) {
	origin := r.Header.Get("Origin")
	allowed := origin == o.AllowedOrigin || isPublicRead(r, o.PublicPathPrefixes)
	if !allowed {
		// 403 on a preflight is clearer in the browser console than a 204 with
		// no allow header, which surfaces as an opaque CORS failure.
		httpx.Fail(w, r, httpx.Forbidden("This origin is not allowed to call the API."))
		return
	}

	h := w.Header()
	h.Set("Access-Control-Allow-Methods", corsAllowMethods)
	h.Set("Access-Control-Allow-Headers", corsAllowHeaders)
	h.Set("Access-Control-Max-Age", corsMaxAge)
	h.Add("Vary", "Access-Control-Request-Method")
	h.Add("Vary", "Access-Control-Request-Headers")
	w.WriteHeader(http.StatusNoContent)
}

// isPublicRead reports whether this request is a safe read of a public path. For
// a preflight the method being asked about is the one that matters, not OPTIONS.
func isPublicRead(r *http.Request, prefixes []string) bool {
	method := r.Method
	if method == http.MethodOptions {
		method = r.Header.Get("Access-Control-Request-Method")
	}
	if method != http.MethodGet && method != http.MethodHead {
		return false
	}
	for _, prefix := range prefixes {
		if r.URL.Path == prefix || strings.HasPrefix(r.URL.Path, prefix+"/") {
			return true
		}
	}
	return false
}
