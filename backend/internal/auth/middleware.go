package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/job-paglu/homepage/backend/internal/httpx"
)

type contextKey struct{ name string }

var (
	sessionContextKey = contextKey{"session"}
	adminContextKey   = contextKey{"admin"}
)

// SessionFromContext returns the session established by RequireSession.
func SessionFromContext(ctx context.Context) (Session, bool) {
	session, ok := ctx.Value(sessionContextKey).(Session)
	return session, ok
}

// AdminFromContext returns the administrator established by RequireSession.
func AdminFromContext(ctx context.Context) (Admin, bool) {
	admin, ok := ctx.Value(adminContextKey).(Admin)
	return admin, ok
}

// RequireSession rejects anything without a live session.
//
// Every /api/admin route except login and logout is wrapped in this, at the
// router. That placement is the access control: a handler cannot forget to check
// something it never had the option to check (contract section 4).
func (h *Handler) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, duplicate := SessionTokenFromRequest(r, h.cfg)
		if duplicate {
			// Two cookies with our session name. In production the `__Host-`
			// prefix should make this unreachable, so it is logged loudly: either a
			// neighbour on the shared registrable suffix is attempting cookie
			// tossing, or a stale non-prefixed cookie survived a rename. Either
			// way, picking one of the two would be guessing, and guessing here is
			// what turns a tossed cookie into a fixated session.
			h.logger.WarnContext(r.Context(), "rejecting a request with duplicate session cookies",
				slog.String("request_id", httpx.RequestID(r.Context())))
			httpx.Fail(w, r, httpx.Unauthorized())
			return
		}
		if token == "" {
			httpx.Fail(w, r, httpx.Unauthorized())
			return
		}

		session, admin, err := h.store.LookupSession(r.Context(), token)
		if err != nil {
			switch {
			case errors.Is(err, ErrNoSession):
				// Unknown, expired, idle-timed-out or disabled: one answer for
				// all four. Distinguishing them tells a caller which of their
				// guesses was closest.
				httpx.Fail(w, r, httpx.Unauthorized())
			case errors.Is(err, ErrUnavailable):
				httpx.Fail(w, r, httpx.ServiceUnavailable(err))
			default:
				httpx.Fail(w, r, httpx.Internal(err))
			}
			return
		}

		// Slide the idle window. A failure here is logged and ignored: the
		// request is authenticated, and refusing it because a bookkeeping UPDATE
		// failed would be a worse outcome than a session expiring early.
		if err := h.store.TouchSession(r.Context(), session.ID); err != nil {
			h.logger.WarnContext(r.Context(), "could not refresh session activity",
				slog.String("request_id", httpx.RequestID(r.Context())),
				slog.String("error", err.Error()))
		}

		ctx := context.WithValue(r.Context(), sessionContextKey, session)
		ctx = context.WithValue(ctx, adminContextKey, admin)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireCSRF guards state-changing admin requests.
//
// Two independent checks, both required (contract section 5):
//
//  1. The Origin (or Referer) must be the configured frontend. This stops a
//     forged request from any other site, token or not.
//  2. X-CSRF-Token must equal the session's token. This stops a request that
//     somehow carried a correct Origin but could not read the token.
//
// On the production domain these two are the whole CSRF defence — the cookie's
// SameSite=Lax does not hold against a neighbouring host on a shared registrable
// suffix (docs/SECURITY.md). Neither check may be relaxed "because SameSite
// covers it".
//
// Must be composed inside RequireSession, since it needs the session. A GET is
// passed through untouched: safe methods are not state-changing, and requiring a
// header on them would break every plain link.
func (h *Handler) RequireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isSafeMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}

		if !CheckOrigin(r, h.cfg) {
			httpx.Fail(w, r, httpx.CSRFInvalid())
			return
		}

		session, ok := SessionFromContext(r.Context())
		if !ok {
			// Reaching here means RequireCSRF was wired outside RequireSession.
			// That is a routing bug, not a client error, so it must not look
			// like one.
			httpx.Fail(w, r, httpx.Internal(errors.New("RequireCSRF used without RequireSession")))
			return
		}

		if !CSRFTokenMatches(r.Header.Get(CSRFHeaderName), session.CSRFToken) {
			httpx.Fail(w, r, httpx.CSRFInvalid())
			return
		}

		next.ServeHTTP(w, r)
	})
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}
