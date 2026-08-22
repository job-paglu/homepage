// Package config loads the process configuration from the environment and
// validates it before anything else starts.
//
// The bias is to fail loudly at boot. A missing DATABASE_URL should stop a
// deploy in the first second, not surface as a 500 an hour later when the first
// visitor arrives; a placeholder session secret that survives into production is
// a vulnerability, not a warning.
package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// Environment names. Production is the default so a forgotten APP_ENV cannot
// silently drop the Secure cookie flag.
const (
	EnvDevelopment = "development"
	EnvProduction  = "production"
)

// MinSessionSecretLen is the shortest ADMIN_SESSION_SECRET the server accepts.
const MinSessionSecretLen = 32

// Config is the fully validated configuration. Every field is populated by
// Load; there are no zero-value surprises and no lazy re-reads of os.Getenv
// elsewhere in the codebase.
type Config struct {
	// DatabaseURL is a libpq/pgx connection string. Secret.
	DatabaseURL string
	// AdminSessionSecret is reserved for keyed operations on session material.
	// Session tokens themselves are random, not derived, so this is not used to
	// mint them; it exists so a future signed value has a key to hand and so a
	// deploy without one is rejected. Secret.
	AdminSessionSecret string

	Port     string
	AppEnv   string
	LogLevel slog.Level

	// FrontendURL is the exact configured frontend location, e.g.
	// https://jobpaglu.com. FrontendOrigin is its scheme+host form, which is
	// what an Origin header actually contains.
	FrontendURL    string
	FrontendOrigin string

	AllowHTTPApplyURLs   bool
	RunMigrationsOnStart bool

	// DBMaxConns is the per-process pgx pool ceiling, from DB_MAX_CONNS.
	//
	// It is configuration rather than a constant because the right value depends
	// entirely on how many copies of this process exist, and that is a property
	// of the host, not of the code. On a long-lived container with a known
	// instance cap, a pool of 8 is comfortable. On a serverless platform every
	// cold instance builds its own pool, the instance count is not something you
	// set, and the real limit is `DB_MAX_CONNS * instances` against Postgres's
	// `max_connections` — so the default is deliberately tiny. See
	// docs/DEPLOYMENT.md and internal/database.Open.
	//
	// Zero means "use the package default"; database.Open owns that number so
	// there is one place to change it.
	DBMaxConns int
}

// MaxSaneDBMaxConns is an upper bound on DB_MAX_CONNS.
//
// Not a technical limit — a typo guard. `DB_MAX_CONNS=100` on a platform that
// runs an unbounded number of instances is how a free Postgres tier is taken
// down by its own application, and the failure arrives as `too many
// connections` on the *next* deploy rather than on the one that caused it.
const MaxSaneDBMaxConns = 64

// Load reads and validates the environment.
//
// Every problem found is reported at once. Fixing configuration one restart per
// mistake is miserable on a platform where a restart is a deploy.
func Load() (*Config, error) {
	c := &Config{
		DatabaseURL:        strings.TrimSpace(os.Getenv("DATABASE_URL")),
		AdminSessionSecret: os.Getenv("ADMIN_SESSION_SECRET"),
		Port:               strings.TrimSpace(os.Getenv("PORT")),
		AppEnv:             strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV"))),
		FrontendURL:        strings.TrimSpace(os.Getenv("FRONTEND_URL")),
	}

	var problems []string
	fail := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf(format, args...))
	}

	// ---------------------------------------------------------- DATABASE_URL
	switch {
	case c.DatabaseURL == "":
		fail("DATABASE_URL is required (a postgres:// connection string)")
	default:
		if u, err := url.Parse(c.DatabaseURL); err != nil {
			// The error text can contain the password, so it is not echoed.
			fail("DATABASE_URL is not a valid URL")
		} else if u.Scheme != "postgres" && u.Scheme != "postgresql" {
			fail("DATABASE_URL must use the postgres:// or postgresql:// scheme, got %q", u.Scheme)
		} else if u.Host == "" {
			fail("DATABASE_URL has no host")
		}
	}

	// -------------------------------------------------- ADMIN_SESSION_SECRET
	switch {
	case c.AdminSessionSecret == "":
		fail("ADMIN_SESSION_SECRET is required (at least %d characters of random data)", MinSessionSecretLen)
	case len(c.AdminSessionSecret) < MinSessionSecretLen:
		fail("ADMIN_SESSION_SECRET must be at least %d characters, got %d",
			MinSessionSecretLen, len(c.AdminSessionSecret))
	case looksLikePlaceholder(c.AdminSessionSecret):
		fail("ADMIN_SESSION_SECRET still looks like the .env.example placeholder; " +
			"generate one with: openssl rand -base64 48")
	}

	// ----------------------------------------------------------- FRONTEND_URL
	if c.FrontendURL == "" {
		fail("FRONTEND_URL is required (the exact frontend origin, e.g. https://jobpaglu.com)")
	} else {
		u, err := url.Parse(c.FrontendURL)
		switch {
		case err != nil:
			fail("FRONTEND_URL is not a valid URL: %v", err)
		case u.Scheme != "http" && u.Scheme != "https":
			fail("FRONTEND_URL must be an absolute http:// or https:// URL")
		case u.Host == "":
			fail("FRONTEND_URL must include a host")
		default:
			// An Origin header is scheme://host[:port] with no path, so that is
			// what gets stored for comparison. Trailing slashes and paths in the
			// configured value are a common and harmless mistake.
			c.FrontendOrigin = u.Scheme + "://" + u.Host
		}
	}

	// ----------------------------------------------------------------- PORT
	if c.Port == "" {
		// Every container and serverless host this project targets injects PORT
		// and expects the server to bind it — Vercel's Go runtime included. The
		// default only exists so `docker run` and `go run` work without one.
		c.Port = "8080"
	} else if n, err := strconv.Atoi(c.Port); err != nil || n < 1 || n > 65535 {
		fail("PORT must be a number between 1 and 65535, got %q", c.Port)
	}

	// --------------------------------------------------------------- APP_ENV
	switch c.AppEnv {
	case "":
		c.AppEnv = EnvProduction // fail safe: production keeps Secure cookies on
	case EnvDevelopment, EnvProduction:
		// ok
	default:
		fail("APP_ENV must be %q or %q, got %q", EnvDevelopment, EnvProduction, c.AppEnv)
	}

	// ------------------------------------------------------------- LOG_LEVEL
	level, err := parseLogLevel(os.Getenv("LOG_LEVEL"))
	if err != nil {
		fail("%v", err)
	}
	c.LogLevel = level

	// ------------------------------------------------------- boolean options
	if v, err := parseBool("ALLOW_HTTP_APPLY_URLS", os.Getenv("ALLOW_HTTP_APPLY_URLS"), false); err != nil {
		fail("%v", err)
	} else {
		c.AllowHTTPApplyURLs = v
	}
	// RUN_MIGRATIONS_ON_START defaults to true because that is the right default
	// for a long-lived process and for local development: one step, and the
	// schema can never lag the code. On a serverless platform it is the wrong
	// default and must be set to false explicitly — every cold instance would
	// otherwise contend for the advisory lock on the request-serving critical
	// path. The advisory lock makes that safe, not free. docs/DEPLOYMENT.md
	// covers the deliberate one-shot path (`adminctl migrate`).
	if v, err := parseBool("RUN_MIGRATIONS_ON_START", os.Getenv("RUN_MIGRATIONS_ON_START"), true); err != nil {
		fail("%v", err)
	} else {
		c.RunMigrationsOnStart = v
	}

	// ----------------------------------------------------------- DB_MAX_CONNS
	if raw := strings.TrimSpace(os.Getenv("DB_MAX_CONNS")); raw != "" {
		n, err := strconv.Atoi(raw)
		switch {
		case err != nil:
			fail("DB_MAX_CONNS must be a whole number, got %q", raw)
		case n < 1:
			fail("DB_MAX_CONNS must be at least 1, got %d", n)
		case n > MaxSaneDBMaxConns:
			fail("DB_MAX_CONNS is %d, which is above the sanity ceiling of %d; "+
				"remember the real limit is DB_MAX_CONNS multiplied by the number of "+
				"running instances", n, MaxSaneDBMaxConns)
		default:
			c.DBMaxConns = n
		}
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("invalid configuration:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return c, nil
}

// IsDevelopment reports whether the relaxations that only make sense on a
// developer's machine apply: no Secure cookie flag, localhost apply URLs, no
// HSTS.
func (c *Config) IsDevelopment() bool { return c.AppEnv == EnvDevelopment }

// HostCookiePrefix is the `__Host-` cookie-name prefix from RFC 6265bis.
//
// A browser accepts a cookie whose name begins with it only when the cookie has
// `Secure`, has `Path=/`, and carries **no** `Domain` attribute. The absence of
// `Domain` is the property that matters here: it makes the cookie host-only, so
// no other host can write it and no other host receives it.
const HostCookiePrefix = "__Host-"

// SecureCookies reports whether cookies are written with the Secure attribute.
//
// It is a separate method from IsDevelopment because CookieName depends on it:
// the `__Host-` prefix is only legal on a Secure cookie, so the two decisions
// have to be derived from one expression rather than from two negations that
// could drift.
func (c *Config) SecureCookies() bool { return !c.IsDevelopment() }

// CookieName applies the `__Host-` prefix to a base cookie name when, and only
// when, the cookie will also carry Secure.
//
// This is the single place the prefix decision is made. The set path, the clear
// path and the read path all call it, which is what keeps them from disagreeing
// — a mismatch would mean the server sets one name and reads another, so every
// request after login would look unauthenticated with nothing in the log to
// explain it.
//
// Why the prefix is needed at all: the production host is a subdomain of a
// registrable domain that is *shared* with strangers (see docs/SECURITY.md, "The
// eu.cc shared-suffix problem"). Any neighbour on that suffix can set a
// `Domain=`-scoped cookie that our host will then also receive. A `__Host-`
// cookie cannot be forged or shadowed that way, because the browser refuses to
// store a `__Host-` cookie that has a Domain attribute at all.
//
// It is deliberately not applied in development: the prefix requires Secure, and
// a Secure cookie is silently dropped over plain http://localhost.
func (c *Config) CookieName(base string) string {
	if !c.SecureCookies() {
		return base
	}
	return HostCookiePrefix + base
}

// Addr is the listen address for net/http.
func (c *Config) Addr() string { return ":" + c.Port }

// Redacted renders the configuration for the startup log with every secret
// replaced. Nothing that returns a string from this type may ever contain
// DATABASE_URL or ADMIN_SESSION_SECRET (rule R2).
func (c *Config) Redacted() string {
	return strings.Join([]string{
		"app_env=" + c.AppEnv,
		"port=" + c.Port,
		"log_level=" + c.LogLevel.String(),
		"frontend_origin=" + c.FrontendOrigin,
		// Rendered because a cookie name that is not the one the operator expects
		// is otherwise invisible until a login mysteriously fails to stick.
		"secure_cookies=" + strconv.FormatBool(c.SecureCookies()),
		"database_url=<redacted>",
		"admin_session_secret=<redacted>",
		"allow_http_apply_urls=" + strconv.FormatBool(c.AllowHTTPApplyURLs),
		"run_migrations_on_start=" + strconv.FormatBool(c.RunMigrationsOnStart),
		// Rendered because on a serverless host the effective connection count is
		// this number times the instance count, and the first symptom of getting
		// it wrong ("too many connections") names neither.
		"db_max_conns=" + strconv.Itoa(c.DBMaxConns),
	}, " ")
}

// String is Redacted. It is defined so that an accidental %v or %s of a Config
// — in a log line, an error, a test failure — cannot leak a secret.
func (c *Config) String() string { return c.Redacted() }

// LogValue is the slog equivalent of String, for the same reason.
func (c *Config) LogValue() slog.Value { return slog.StringValue(c.Redacted()) }

// ------------------------------------------------------------------- helpers

func parseLogLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("LOG_LEVEL must be one of debug|info|warn|error, got %q", raw)
	}
}

func parseBool(name, raw string, def bool) (bool, error) {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return def, nil
	}
	switch raw {
	case "1", "t", "true", "yes", "y", "on":
		return true, nil
	case "0", "f", "false", "no", "n", "off":
		return false, nil
	default:
		return def, fmt.Errorf("%s must be true or false, got %q", name, raw)
	}
}

// placeholderMarkers are substrings that appear in an unfilled .env.example
// value. The check is a heuristic on purpose: this package cannot read
// .env.example (another component owns that file), so instead of comparing
// against one literal it refuses anything that reads like a template. A real
// random secret from `openssl rand -base64 48` contains none of these.
var placeholderMarkers = []string{
	"replace_me", "replace-me", "replaceme",
	"change_me", "change-me", "changeme",
	"your_", "your-", "yoursecret",
	"placeholder", "example", "sample", "dummy",
	"todo", "fixme", "xxxxxxxx", "secret_here", "secret-here",
	"generate", "insert_", "insert-",
}

func looksLikePlaceholder(secret string) bool {
	lower := strings.ToLower(secret)
	for _, marker := range placeholderMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	// A single repeated character passes a length check but has no entropy.
	first := rune(-1)
	uniform := true
	for _, r := range secret {
		if first == -1 {
			first = r
			continue
		}
		if r != first {
			uniform = false
			break
		}
	}
	return uniform
}
