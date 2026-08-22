package auth

// Cookie names and the session-cookie read path.
//
// Split out of session.go so that everything the browser's cookie jar depends on
// — the name, the attributes that make the name legal, and the code that reads it
// back — is in one file. The set path, the clear path and the read path must
// agree on the name, and the cheapest way to keep them agreeing is to put them
// where a reader sees all three at once.

import (
	"net/http"

	"github.com/job-paglu/homepage/backend/internal/config"
)

// Cookie base names and the CSRF header name. jp_csrf is deliberately readable
// by JavaScript; see csrf.go.
//
// These are *base* names. The name actually on the wire is derived from the
// configuration by SessionCookieName / CSRFCookieName below, because production
// prefixes both with `__Host-`. Nothing outside this package should build a
// cookie name by concatenation.
const (
	SessionCookieBaseName = "jp_session"
	CSRFCookieBaseName    = "jp_csrf"
	CSRFHeaderName        = "X-CSRF-Token"
)

// SessionCookieName is the wire name of the session cookie for this
// configuration: `__Host-jp_session` in production, `jp_session` in development.
//
// The prefix decision itself lives in config.Config.CookieName and nowhere else,
// so the set, clear and read paths cannot end up using different names.
func SessionCookieName(cfg *config.Config) string {
	return cfg.CookieName(SessionCookieBaseName)
}

// CSRFCookieName is the wire name of the CSRF cookie, derived the same way.
func CSRFCookieName(cfg *config.Config) string {
	return cfg.CookieName(CSRFCookieBaseName)
}

// SetSessionCookie writes the session cookie.
//
// HttpOnly is what keeps an XSS bug on the frontend from becoming a stolen
// session. Secure is omitted only in development, so http://localhost works
// without relaxing production.
//
// SameSite=Lax is kept, but on the production domain it is **not** a CSRF
// control: the registrable domain is shared with strangers, so a neighbouring
// host is same-site with us and the cookie is sent on its cross-origin POSTs
// anyway. The X-CSRF-Token check and the Origin check are what actually hold —
// see docs/SECURITY.md, "The eu.cc shared-suffix problem". Lax stays because it
// costs nothing and becomes load-bearing again the day a real registrable domain
// is used.
//
// Path is "/" and no Domain is set — do not add one. Both are preconditions of
// the `__Host-` name prefix this cookie carries in production: a browser
// silently refuses a `__Host-` cookie that has a Domain, lacks Secure, or has
// any Path other than "/", and "silently" means login appears to succeed and no
// cookie is stored.
func SetSessionCookie(w http.ResponseWriter, cfg *config.Config, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName(cfg),
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   cfg.SecureCookies(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(IdleExpiry.Seconds()),
	})
}

// ClearSessionCookie expires the session cookie. The attributes must match the
// ones it was set with or the browser keeps the original — including the name,
// which is why this goes through SessionCookieName rather than a literal.
//
// Path "/" and the absence of Domain are required by the `__Host-` prefix here
// too; see SetSessionCookie.
func ClearSessionCookie(w http.ResponseWriter, cfg *config.Config) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName(cfg),
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   cfg.SecureCookies(),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// SessionTokenFromRequest reads the raw token from the request cookie, and
// reports whether the request carried more than one cookie with that name.
//
// It iterates r.Cookies() instead of calling r.Cookie(name), which returns the
// first match and would silently pick one of several. A Cookie header may
// legitimately contain two entries with the same name — a host-only cookie and a
// Domain-scoped one arrive as indistinguishable `name=value` pairs — and on a
// shared registrable suffix a neighbouring host can inject the second one
// deliberately (cookie tossing).
//
// With a `__Host-` cookie in production this should be impossible, because the
// browser will not store a Domain-scoped cookie under that name at all. If it
// happens anyway, the request is treated as unauthenticated: guessing which of
// two tokens the user meant is how a session-fixation attack succeeds, so
// failing closed is the only correct answer. The caller returns 401 and the user
// clears their cookies.
func SessionTokenFromRequest(r *http.Request, cfg *config.Config) (token string, duplicate bool) {
	name := SessionCookieName(cfg)
	found := 0
	for _, cookie := range r.Cookies() {
		if cookie.Name != name {
			continue
		}
		found++
		token = cookie.Value
	}
	if found > 1 {
		return "", true
	}
	return token, false
}
