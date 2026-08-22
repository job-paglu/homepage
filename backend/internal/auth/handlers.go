package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/job-paglu/homepage/backend/internal/config"
	"github.com/job-paglu/homepage/backend/internal/httpx"
	"github.com/job-paglu/homepage/backend/internal/middleware"
	"github.com/job-paglu/homepage/backend/internal/validation"
)

// Login rate limits from contract section 4. Both windows are 15 minutes;
// the per-email budget is looser than the per-IP one because several
// administrators can legitimately share an office IP, while ten wrong passwords
// against one address is not something that happens by accident.
const (
	loginWindow        = 15 * time.Minute
	loginPerIPLimit    = 5
	loginPerEmailLimit = 10
)

// Handler serves the three authentication endpoints and owns the middleware that
// guards everything else.
type Handler struct {
	store  *Store
	cfg    *config.Config
	logger *slog.Logger

	ipLimiter    *middleware.Limiter
	emailLimiter *middleware.Limiter
}

// NewHandler builds the auth handler and its rate limiters.
func NewHandler(store *Store, cfg *config.Config, logger *slog.Logger) *Handler {
	return &Handler{
		store:        store,
		cfg:          cfg,
		logger:       logger,
		ipLimiter:    middleware.NewLimiter(loginPerIPLimit, loginWindow),
		emailLimiter: middleware.NewLimiter(loginPerEmailLimit, loginWindow),
	}
}

// StartJanitors begins the rate-limiter sweeps.
//
// Call it once at startup with the process-lifetime context — not a request
// context, which would stop the sweep as soon as the first request finished and
// turn the limiter maps into a slow memory leak.
func (h *Handler) StartJanitors(ctx context.Context) {
	h.ipLimiter.StartJanitor(ctx)
	h.emailLimiter.StartJanitor(ctx)
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type sessionResponse struct {
	Admin     AdminDTO `json:"admin"`
	CSRFToken string   `json:"csrf_token"`
}

// Login handles POST /api/admin/login.
//
// The design constraint that shapes this whole function: an unknown email and a
// wrong password must be indistinguishable, in both the response body and the
// time taken. So there is exactly one failure exit, every path through it
// performs one Argon2id verification, and neither branch logs the email at a
// level that would build an account list for someone reading the logs.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	// Login creates state, so it gets the same Origin check as every other
	// state-changing admin request. A browser always sends Origin on a POST.
	if !CheckOrigin(r, h.cfg) {
		httpx.Fail(w, r, httpx.CSRFInvalid())
		return
	}

	var body loginRequest
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.Fail(w, r, err)
		return
	}

	fieldErrors := validation.New()
	email := validation.Email(fieldErrors, "email", body.Email)
	if body.Password == "" {
		fieldErrors.Add("password", "This field is required.")
	}
	if fieldErrors.Any() {
		httpx.Fail(w, r, httpx.ValidationFailed(fieldErrors.Map()))
		return
	}

	ip := middleware.ClientIP(r)
	if allowed, retryAfter := h.ipLimiter.Allowed(ip); !allowed {
		httpx.Fail(w, r, httpx.RateLimited(int(retryAfter.Seconds())))
		return
	}
	if allowed, retryAfter := h.emailLimiter.Allowed(email); !allowed {
		httpx.Fail(w, r, httpx.RateLimited(int(retryAfter.Seconds())))
		return
	}

	admin, err := h.store.FindAdminByEmail(r.Context(), email)
	switch {
	case errors.Is(err, ErrNoAdmin):
		// The account does not exist. Hash anyway: skipping this is what turns a
		// timing difference into an account-enumeration oracle.
		VerifyDummy(body.Password)
		h.rejectLogin(w, r, ip, email, "unknown account")
		return
	case errors.Is(err, ErrUnavailable):
		httpx.Fail(w, r, httpx.ServiceUnavailable(err))
		return
	case err != nil:
		httpx.Fail(w, r, httpx.Internal(err))
		return
	}

	matched, err := Verify(body.Password, admin.PasswordHash())
	if err != nil {
		// A malformed hash column is an operational fault, not a bad password.
		// The client still gets the uniform 401; the log gets the truth.
		h.logger.ErrorContext(r.Context(), "stored password hash is unusable",
			slog.String("request_id", httpx.RequestID(r.Context())),
			slog.String("admin_id", admin.ID),
			slog.String("error", err.Error()))
		h.rejectLogin(w, r, ip, email, "unusable password hash")
		return
	}
	if !matched {
		h.rejectLogin(w, r, ip, email, "wrong password")
		return
	}
	if admin.DisabledAt != nil {
		// Checked after the verification so a disabled account costs the same as
		// an active one.
		h.rejectLogin(w, r, ip, email, "disabled account")
		return
	}

	// Session fixation defence: whatever session the caller arrived with is
	// destroyed, and a fresh token is issued (contract section 4).
	//
	// A duplicate-cookie request yields an empty token, so nothing is deleted.
	// That is harmless here: the new session is created and its Set-Cookie is
	// written regardless, and a tossed cookie the server never resolved to a
	// session row had nothing to revoke in the first place.
	if previous, _ := SessionTokenFromRequest(r, h.cfg); previous != "" {
		if err := h.store.DeleteSession(r.Context(), previous); err != nil {
			h.logger.WarnContext(r.Context(), "could not rotate the previous session",
				slog.String("request_id", httpx.RequestID(r.Context())),
				slog.String("error", err.Error()))
		}
	}

	token, session, err := h.store.CreateSession(r.Context(), admin.ID, r.UserAgent(), middleware.RedactIP(ip))
	if err != nil {
		if errors.Is(err, ErrUnavailable) {
			httpx.Fail(w, r, httpx.ServiceUnavailable(err))
		} else {
			httpx.Fail(w, r, httpx.Internal(err))
		}
		return
	}

	now := time.Now().UTC()
	if err := h.store.TouchLogin(r.Context(), admin.ID, now); err != nil {
		// Not worth failing a successful login over.
		h.logger.WarnContext(r.Context(), "could not record last_login_at",
			slog.String("request_id", httpx.RequestID(r.Context())),
			slog.String("error", err.Error()))
	}
	admin.LastLoginAt = &now

	// A successful login clears both counters, so four typos followed by a
	// success leaves no penalty behind (contract section 4).
	h.ipLimiter.Reset(ip)
	h.emailLimiter.Reset(email)

	SetSessionCookie(w, h.cfg, token)
	SetCSRFCookie(w, h.cfg, session.CSRFToken)

	h.logger.InfoContext(r.Context(), "admin signed in",
		slog.String("request_id", httpx.RequestID(r.Context())),
		slog.String("admin_id", admin.ID),
		slog.String("ip", middleware.RedactIP(ip)))

	httpx.Data(w, r, http.StatusOK, sessionResponse{
		Admin:     admin.DTO(),
		CSRFToken: session.CSRFToken,
	})
}

// rejectLogin is the single failure exit. One body, one status, one message,
// whatever went wrong.
func (h *Handler) rejectLogin(w http.ResponseWriter, r *http.Request, ip, email, reason string) {
	h.ipLimiter.Fail(ip)
	h.emailLimiter.Fail(email)

	// The reason is logged and never returned. The address is not logged: the log
	// is a lower-trust artefact than the database, and a stream of "login
	// rejected for x@y" lines is an account list for anyone who can read it.
	h.logger.WarnContext(r.Context(), "login rejected",
		slog.String("request_id", httpx.RequestID(r.Context())),
		slog.String("reason", reason),
		slog.String("ip", middleware.RedactIP(ip)))

	httpx.Fail(w, r, httpx.InvalidCredentials())
}

// Logout handles POST /api/admin/logout.
//
// Deliberately not behind RequireSession: the contract makes it idempotent, so
// "no session" is a 204 rather than a 401. CSRF is still enforced whenever a
// session is actually presented, which is the only case where the call does
// anything — and the Origin check applies regardless, because a forged logout is
// still a nuisance worth refusing.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	if !CheckOrigin(r, h.cfg) {
		httpx.Fail(w, r, httpx.CSRFInvalid())
		return
	}

	// A duplicate session cookie collapses to the empty token, which lands in the
	// branch below: clear both cookies and answer 204. For logout that is the
	// desired outcome anyway — the caller wanted to end up signed out, and the
	// expired Set-Cookie headers remove our own host-only cookies.
	token, _ := SessionTokenFromRequest(r, h.cfg)
	if token == "" {
		h.clearCookies(w)
		httpx.NoContent(w)
		return
	}

	session, _, err := h.store.LookupSession(r.Context(), token)
	switch {
	case errors.Is(err, ErrNoSession):
		// The cookie refers to nothing. Clear it and report success: there is no
		// session left to protect and nothing for CSRF to guard.
		h.clearCookies(w)
		httpx.NoContent(w)
		return
	case errors.Is(err, ErrUnavailable):
		httpx.Fail(w, r, httpx.ServiceUnavailable(err))
		return
	case err != nil:
		httpx.Fail(w, r, httpx.Internal(err))
		return
	}

	if !CSRFTokenMatches(r.Header.Get(CSRFHeaderName), session.CSRFToken) {
		httpx.Fail(w, r, httpx.CSRFInvalid())
		return
	}

	if err := h.store.DeleteSession(r.Context(), token); err != nil {
		httpx.Fail(w, r, httpx.Internal(err))
		return
	}

	h.clearCookies(w)
	httpx.NoContent(w)
}

// Me handles GET /api/admin/me. Behind RequireSession, so reaching it means the
// caller is authenticated; the only job is to hand back the identity and the
// CSRF token the dashboard needs after a page reload.
func (h *Handler) Me(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")

	session, okSession := SessionFromContext(r.Context())
	admin, okAdmin := AdminFromContext(r.Context())
	if !okSession || !okAdmin {
		httpx.Fail(w, r, httpx.Unauthorized())
		return
	}

	// Re-issued on every /me so a client that lost the cookie — private mode, a
	// cleared jar — can recover without signing in again.
	SetCSRFCookie(w, h.cfg, session.CSRFToken)

	httpx.Data(w, r, http.StatusOK, sessionResponse{
		Admin:     admin.DTO(),
		CSRFToken: session.CSRFToken,
	})
}

func (h *Handler) clearCookies(w http.ResponseWriter) {
	ClearSessionCookie(w, h.cfg)
	ClearCSRFCookie(w, h.cfg)
}

// TrimEmail is exported for cmd/adminctl, which needs the same normalisation the
// login path applies so a bootstrap address matches at sign-in.
func TrimEmail(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}
