package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/job-paglu/homepage/backend/internal/config"
)

// testConfig builds a real, validated Config. Constructing the struct literally
// would let a test pass while config.Load's own defaulting (APP_ENV unset means
// production) was broken, which is exactly the rule the cookie name depends on.
func testConfig(t *testing.T, appEnv string) *config.Config {
	t.Helper()

	for name, value := range map[string]string{
		"DATABASE_URL":            "postgres://api:pw@db.example.invalid:5432/jobpaglu?sslmode=require",
		"ADMIN_SESSION_SECRET":    "aG9tZXBhZ2UtcnVudGltZS1zZWNyZXQtMDEyMzQ1Njc4",
		"FRONTEND_URL":            "https://home.jobpaglu.eu.cc",
		"APP_ENV":                 appEnv,
		"PORT":                    "",
		"LOG_LEVEL":               "",
		"ALLOW_HTTP_APPLY_URLS":   "",
		"RUN_MIGRATIONS_ON_START": "",
	} {
		t.Setenv(name, value)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() = %v", err)
	}
	return cfg
}

// setCookies returns the cookies a writer function emitted, parsed the way a
// browser would see them.
func setCookies(t *testing.T, write func(w http.ResponseWriter)) []*http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	write(rec)
	return (&http.Response{Header: rec.Header()}).Cookies()
}

func findCookie(cookies []*http.Cookie, name string) *http.Cookie {
	for _, c := range cookies {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestCookieNamesAreEnvironmentDerived(t *testing.T) {
	// Production must carry the prefix: the registrable domain is shared, so a
	// neighbour could otherwise shadow an unprefixed cookie.
	prod := testConfig(t, config.EnvProduction)
	if got, want := SessionCookieName(prod), "__Host-jp_session"; got != want {
		t.Errorf("SessionCookieName(production) = %q, want %q", got, want)
	}
	if got, want := CSRFCookieName(prod), "__Host-jp_csrf"; got != want {
		t.Errorf("CSRFCookieName(production) = %q, want %q", got, want)
	}

	// Development must not: the prefix requires Secure, and a Secure cookie never
	// arrives over plain http://localhost.
	dev := testConfig(t, config.EnvDevelopment)
	if got, want := SessionCookieName(dev), "jp_session"; got != want {
		t.Errorf("SessionCookieName(development) = %q, want %q", got, want)
	}
	if got, want := CSRFCookieName(dev), "jp_csrf"; got != want {
		t.Errorf("CSRFCookieName(development) = %q, want %q", got, want)
	}
}

// The three attributes a browser checks before it will store a `__Host-` cookie.
// Any one of them missing means the cookie is dropped silently, so they are
// asserted rather than trusted.
func TestHostPrefixedCookiesSatisfyTheirPreconditions(t *testing.T) {
	cfg := testConfig(t, config.EnvProduction)

	writers := map[string]func(w http.ResponseWriter){
		"set session":   func(w http.ResponseWriter) { SetSessionCookie(w, cfg, "token-value") },
		"clear session": func(w http.ResponseWriter) { ClearSessionCookie(w, cfg) },
		"set csrf":      func(w http.ResponseWriter) { SetCSRFCookie(w, cfg, "csrf-value") },
		"clear csrf":    func(w http.ResponseWriter) { ClearCSRFCookie(w, cfg) },
	}

	for name, write := range writers {
		t.Run(name, func(t *testing.T) {
			cookies := setCookies(t, write)
			if len(cookies) != 1 {
				t.Fatalf("emitted %d cookies, want 1", len(cookies))
			}
			cookie := cookies[0]

			if !strings.HasPrefix(cookie.Name, config.HostCookiePrefix) {
				t.Errorf("name = %q, want the %q prefix", cookie.Name, config.HostCookiePrefix)
			}
			if !cookie.Secure {
				t.Error("Secure is not set; the browser rejects a __Host- cookie without it")
			}
			if cookie.Path != "/" {
				t.Errorf("Path = %q, want /", cookie.Path)
			}
			if cookie.Domain != "" {
				t.Errorf("Domain = %q, want empty; a __Host- cookie with a Domain is discarded", cookie.Domain)
			}
		})
	}
}

// The failure this guards against is the worst kind to debug: the write path and
// the read path disagreeing on the name, so every request after a successful
// login looks unauthenticated and nothing is logged that says why.
func TestReadPathAcceptsWhatTheWritePathSets(t *testing.T) {
	for _, appEnv := range []string{config.EnvProduction, config.EnvDevelopment} {
		t.Run(appEnv, func(t *testing.T) {
			cfg := testConfig(t, appEnv)
			const want = "MRRhtCS4wKDdCLl1nu5nCPQGmUj3g4nHmoJTZOaBqXg"

			cookies := setCookies(t, func(w http.ResponseWriter) { SetSessionCookie(w, cfg, want) })

			req := httptest.NewRequest(http.MethodGet, "/api/admin/me", nil)
			for _, c := range cookies {
				req.AddCookie(c)
			}

			token, duplicate := SessionTokenFromRequest(req, cfg)
			if duplicate {
				t.Fatal("a single Set-Cookie round-tripped as a duplicate")
			}
			if token != want {
				t.Errorf("token = %q, want %q", token, want)
			}
		})
	}

	// And the clear path must target the same name, or logout leaves the cookie
	// in place while reporting 204.
	cfg := testConfig(t, config.EnvProduction)
	cleared := setCookies(t, func(w http.ResponseWriter) { ClearSessionCookie(w, cfg) })
	if findCookie(cleared, SessionCookieName(cfg)) == nil {
		t.Errorf("ClearSessionCookie did not target %q", SessionCookieName(cfg))
	}
}

// Cookie tossing: a neighbour on the shared registrable suffix sets a second
// cookie with our name. Both arrive as bare name=value pairs and the server
// cannot tell them apart, so it must refuse rather than pick one.
func TestDuplicateSessionCookieIsRejected(t *testing.T) {
	cfg := testConfig(t, config.EnvProduction)
	name := SessionCookieName(cfg)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/jobs", nil)
	req.AddCookie(&http.Cookie{Name: name, Value: "attacker-planted-token"})
	req.AddCookie(&http.Cookie{Name: name, Value: "the-real-token"})

	token, duplicate := SessionTokenFromRequest(req, cfg)
	if !duplicate {
		t.Error("duplicate = false for two cookies with the session name")
	}
	if token != "" {
		t.Errorf("token = %q, want empty; returning either value is a guess", token)
	}
}

func TestNoSessionCookieIsNotADuplicate(t *testing.T) {
	cfg := testConfig(t, config.EnvProduction)

	req := httptest.NewRequest(http.MethodGet, "/api/admin/me", nil)
	// An unrelated cookie, and the development name, must both be ignored in
	// production rather than mistaken for ours.
	req.AddCookie(&http.Cookie{Name: "unrelated", Value: "x"})
	req.AddCookie(&http.Cookie{Name: SessionCookieBaseName, Value: "stale-unprefixed"})

	token, duplicate := SessionTokenFromRequest(req, cfg)
	if duplicate {
		t.Error("duplicate = true with no cookie carrying the configured name")
	}
	if token != "" {
		t.Errorf("token = %q, want empty", token)
	}
}

// The CSRF cookie has to stay script-readable — that is the entire mechanism —
// even now that it is `__Host-` prefixed.
func TestCSRFCookieRemainsReadableByScript(t *testing.T) {
	cfg := testConfig(t, config.EnvProduction)

	cookies := setCookies(t, func(w http.ResponseWriter) { SetCSRFCookie(w, cfg, "csrf-value") })
	cookie := findCookie(cookies, CSRFCookieName(cfg))
	if cookie == nil {
		t.Fatalf("no cookie named %q was set", CSRFCookieName(cfg))
	}
	if cookie.HttpOnly {
		t.Error("HttpOnly is set; the dashboard could not read the token to echo it")
	}
}
