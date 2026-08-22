package auth

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"

	"github.com/job-paglu/homepage/backend/internal/config"
)

// SetCSRFCookie writes the CSRF token as a cookie the client can read.
//
// Deliberately not HttpOnly: the admin dashboard has to read this value to put
// it in the X-CSRF-Token header, and that is the whole mechanism. It is safe
// because the token is not a credential on its own — an attacker's page can
// neither read it (different origin) nor make the browser send the session
// cookie together with a forged header.
//
// Path "/" and no Domain: both are preconditions of the `__Host-` name prefix
// this cookie carries in production. Do not add a Domain here — the browser
// would discard the cookie outright, the dashboard would never obtain a token
// after a reload, and every mutation would 403. See config.Config.CookieName.
func SetCSRFCookie(w http.ResponseWriter, cfg *config.Config, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName(cfg),
		Value:    token,
		Path:     "/",
		HttpOnly: false,
		Secure:   cfg.SecureCookies(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(IdleExpiry.Seconds()),
	})
}

// ClearCSRFCookie expires the CSRF cookie. Name and attributes must match the
// ones it was set with, `__Host-` prefix included, or the browser keeps the
// original.
func ClearCSRFCookie(w http.ResponseWriter, cfg *config.Config) {
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName(cfg),
		Value:    "",
		Path:     "/",
		HttpOnly: false,
		Secure:   cfg.SecureCookies(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// CSRFTokenMatches compares a presented token with the session's, in constant
// time.
//
// Constant time matters less here than for a password — the token changes every
// session — but a variable-time compare on a secret is never the right default,
// and the cost is nil.
func CSRFTokenMatches(presented, expected string) bool {
	if presented == "" || expected == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(presented), []byte(expected)) == 1
}

// CheckOrigin verifies that a state-changing request came from the configured
// frontend.
//
// On the production domain this is not the *second* line of defence, it is one
// of the two that are actually load-bearing. SameSite=Lax does not separate us
// from a neighbouring host on a shared registrable suffix, so a cross-origin
// POST from `attacker.<suffix>` arrives with our session cookie attached. What
// stops it is this comparison against FRONTEND_URL, plus the X-CSRF-Token check.
// Treat both as required, not as belt-and-braces (docs/SECURITY.md).
//
// It also catches what a token cannot: a request forged before the client ever
// obtained a token. Origin is preferred over Referer because it is sent on
// cross-origin POSTs, carries no path, and cannot be set by page JavaScript.
//
// A missing Origin *and* a missing Referer is a rejection, not a pass. Browsers
// send Origin on every state-changing request; a request without one is either a
// non-browser client — which should use a different mechanism — or an attempt to
// dodge this check.
func CheckOrigin(r *http.Request, cfg *config.Config) bool {
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		// "null" is what a sandboxed iframe or a file:// page sends. It is never
		// the configured frontend.
		return origin != "null" && origin == cfg.FrontendOrigin
	}

	// Referer is the fallback for the rare browser or proxy that strips Origin.
	// Only its scheme and host are considered, since the path is irrelevant and
	// comparing it would break as soon as the dashboard gains a route.
	referer := strings.TrimSpace(r.Header.Get("Referer"))
	if referer == "" {
		return false
	}
	parsed, err := url.Parse(referer)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	return parsed.Scheme+"://"+parsed.Host == cfg.FrontendOrigin
}
