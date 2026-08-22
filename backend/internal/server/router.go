// Package server assembles the routing table and the middleware stack.
//
// This is the one file that knows about every other package, which is
// deliberate: the wiring is worth reading in one place, and the access-control
// story — which routes are behind a session, which are behind CSRF — should be
// visible without following a call chain.
package server

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/job-paglu/homepage/backend/internal/auth"
	"github.com/job-paglu/homepage/backend/internal/config"
	"github.com/job-paglu/homepage/backend/internal/database"
	"github.com/job-paglu/homepage/backend/internal/httpx"
	"github.com/job-paglu/homepage/backend/internal/jobs"
	"github.com/job-paglu/homepage/backend/internal/middleware"
)

// Deps is everything the router needs, supplied by main.
type Deps struct {
	Config     *config.Config
	Logger     *slog.Logger
	Pool       *pgxpool.Pool
	PublicJobs *jobs.PublicHandler
	AdminJobs  *jobs.AdminHandler
	Auth       *auth.Handler
}

// New builds the complete handler.
//
// Middleware order, outermost first, and each position is a decision:
//
//	RequestID   first, so every later log line and error body has an id
//	Logging     next, so its recorder wraps the writer Recover inspects
//	Recover     inside Logging, so a panic is still logged as a 500 response
//	Security    before anything can write a body
//	CORS        before routing, because a preflight must not reach a handler
//	BodyLimit   last, so the cap applies to handlers and nothing else
func New(deps Deps) http.Handler {
	mux := http.NewServeMux()

	// ------------------------------------------------------------- probes
	// /health never touches the database. A liveness probe that fails because a
	// dependency is down gets the container killed for someone else's outage.
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		httpx.Data(w, r, http.StatusOK, map[string]string{"status": "ok"})
	})

	// /ready does, because that is what readiness means: this instance can serve
	// a request end to end.
	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, r *http.Request) {
		if err := database.Ready(r.Context(), deps.Pool); err != nil {
			httpx.Fail(w, r, httpx.ServiceUnavailable(err))
			return
		}
		httpx.Data(w, r, http.StatusOK, map[string]string{"status": "ready"})
	})

	// ------------------------------------------------------------- public
	mux.HandleFunc("GET /api/jobs", deps.PublicJobs.List)
	mux.HandleFunc("GET /api/jobs/{slug}", deps.PublicJobs.Detail)

	// -------------------------------------------------------------- admin
	// Login and logout are outside RequireSession by design: login has no
	// session yet, and logout is specified as idempotent, so "no session" is a
	// 204 rather than a 401. Both do their own Origin check.
	mux.HandleFunc("POST /api/admin/login", deps.Auth.Login)
	mux.HandleFunc("POST /api/admin/logout", deps.Auth.Logout)

	// Everything below is session-gated. Reads need only a session; writes need
	// a session and CSRF. Applying the guard here rather than inside each
	// handler is the access control: a handler cannot skip a check it was never
	// given the chance to make.
	session := deps.Auth.RequireSession
	guarded := func(h http.HandlerFunc) http.Handler {
		return session(h)
	}
	guardedWrite := func(h http.HandlerFunc) http.Handler {
		return session(deps.Auth.RequireCSRF(h))
	}

	mux.Handle("GET /api/admin/me", guarded(deps.Auth.Me))

	mux.Handle("GET /api/admin/jobs", guarded(deps.AdminJobs.List))
	mux.Handle("GET /api/admin/jobs/{id}", guarded(deps.AdminJobs.Get))
	mux.Handle("POST /api/admin/jobs", guardedWrite(deps.AdminJobs.Create))
	mux.Handle("PUT /api/admin/jobs/{id}", guardedWrite(deps.AdminJobs.Update))
	mux.Handle("DELETE /api/admin/jobs/{id}", guardedWrite(deps.AdminJobs.Delete))
	mux.Handle("POST /api/admin/jobs/{id}/publish", guardedWrite(deps.AdminJobs.Publish))
	mux.Handle("POST /api/admin/jobs/{id}/unpublish", guardedWrite(deps.AdminJobs.Unpublish))

	return middleware.Chain(
		envelopeMuxErrors(mux),
		httpx.RequestIDMiddleware,
		middleware.Logging(deps.Logger),
		middleware.Recover(deps.Logger),
		middleware.SecurityHeaders(deps.Config.IsDevelopment()),
		middleware.CORS(middleware.CORSOptions{
			AllowedOrigin: deps.Config.FrontendOrigin,
			// Public reads stay embeddable from any origin, without credentials.
			// A cookie-bearing request is still held to the single allowlisted
			// origin (contract section 5).
			PublicPathPrefixes: []string{"/api/jobs", "/health", "/ready"},
		}),
		middleware.BodyLimit,
	)
}

// envelopeMuxErrors rewrites net/http's own 404 and 405 replies as the JSON
// envelope.
//
// ServeMux generates both itself — "404 page not found" as text/plain, and a 405
// with an Allow header — and offers no hook to change them. Registering a
// catch-all "/" pattern would replace the 404s but also swallow every 405,
// because "/" matches any method and so counts as a full match. Intercepting the
// write is the only way to keep both behaviours and still answer in the shape the
// contract promises.
//
// Our own handlers also return 404s (JOB_NOT_FOUND), so the interception is
// conditioned on the Content-Type: anything httpx wrote is already JSON and is
// passed through untouched.
func envelopeMuxErrors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		interceptor := &muxErrorInterceptor{ResponseWriter: w, request: r}
		next.ServeHTTP(interceptor, r)
	})
}

type muxErrorInterceptor struct {
	http.ResponseWriter
	request  *http.Request
	replaced bool
	passing  bool
}

func (m *muxErrorInterceptor) WriteHeader(status int) {
	if m.replaced || m.passing {
		m.ResponseWriter.WriteHeader(status)
		return
	}

	isMuxError := status == http.StatusNotFound || status == http.StatusMethodNotAllowed
	writtenByUs := strings.HasPrefix(m.Header().Get("Content-Type"), "application/json")

	if !isMuxError || writtenByUs {
		m.passing = true
		m.ResponseWriter.WriteHeader(status)
		return
	}

	// net/http's plain-text reply. Its Content-Type has to go so httpx can set
	// the JSON one; the nosniff header it also set is kept, since it is what the
	// security middleware would have added anyway. The Allow header from a 405 is
	// preserved too: it is genuinely useful and the contract does not forbid it.
	m.Header().Del("Content-Type")
	m.replaced = true

	if status == http.StatusMethodNotAllowed {
		httpx.Fail(m.ResponseWriter, m.request, httpx.MethodNotAllowed())
		return
	}
	httpx.Fail(m.ResponseWriter, m.request, httpx.NotFound())
}

// Write swallows the body net/http was going to send once the reply has been
// replaced. Without this the JSON envelope would be followed by
// "404 page not found".
func (m *muxErrorInterceptor) Write(b []byte) (int, error) {
	if m.replaced {
		return len(b), nil
	}
	if !m.passing {
		// A handler that writes without calling WriteHeader implies 200.
		m.WriteHeader(http.StatusOK)
	}
	return m.ResponseWriter.Write(b)
}

// Unwrap keeps http.ResponseController — and the Wrote() probe Recover uses —
// working through this wrapper.
func (m *muxErrorInterceptor) Unwrap() http.ResponseWriter { return m.ResponseWriter }
