package middleware

import (
	"net/http"

	"github.com/job-paglu/homepage/backend/internal/httpx"
)

// BodyLimit caps request bodies at httpx.MaxBodyBytes (64 KiB, contract
// section 1).
//
// Two layers, because either alone is insufficient:
//
//  1. A declared Content-Length over the cap is rejected before a byte is read,
//     so a 5 MB paste does not travel.
//  2. The body is wrapped in MaxBytesReader regardless, because Content-Length
//     is a claim: a chunked request declares nothing, and a lying header would
//     otherwise get a free pass.
//
// The largest legitimate body is a job with a 20 000-character description,
// which is comfortably under the cap even after JSON escaping.
func BodyLimit(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength > httpx.MaxBodyBytes {
			httpx.Fail(w, r, httpx.PayloadTooLarge())
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, httpx.MaxBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}
