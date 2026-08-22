package middleware

import "net/http"

// SecurityHeaders sets the response headers that apply to a JSON API.
//
// The set is smaller than a web page's would be, because this origin never
// serves HTML: the interesting cases are a browser being tricked into treating a
// response as a document (nosniff, the locked-down CSP) and a response being
// framed (frame-ancestors, X-Frame-Options).
//
// HSTS is emitted only outside development, where the API is reachable over
// plain http on localhost and pinning the browser to https would break it for
// weeks.
func SecurityHeaders(isDevelopment bool) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()

			// Stops a browser from guessing that a JSON body is HTML, which is
			// the mechanism behind most "API returns XSS" reports.
			h.Set("X-Content-Type-Options", "nosniff")

			// default-src 'none' is correct here precisely because the response
			// is data: there is nothing to load. frame-ancestors 'none' is the
			// modern clickjacking control; X-Frame-Options covers older
			// browsers that ignore it.
			h.Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")
			h.Set("X-Frame-Options", "DENY")

			// A referrer would leak an admin URL to whatever a job's apply link
			// points at. Nothing here needs one.
			h.Set("Referrer-Policy", "no-referrer")

			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			h.Set("Permissions-Policy", "geolocation=(), camera=(), microphone=(), payment=(), usb=()")

			if !isDevelopment {
				h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			}

			next.ServeHTTP(w, r)
		})
	}
}
