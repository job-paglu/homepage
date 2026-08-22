package config

import (
	"log/slog"
	"strings"
	"testing"
)

// A 44-character value with no template markers in it, standing in for
// `openssl rand -base64 48` output.
const goodSecret = "aG9tZXBhZ2UtcnVudGltZS1zZWNyZXQtMDEyMzQ1Njc4"

const goodDSN = "postgres://api:pw@db.example.invalid:5432/jobpaglu?sslmode=require"

// setEnv wipes every variable this package reads, then applies the overrides.
// Without the wipe a variable left over from the developer's shell decides the
// result of the test.
func setEnv(t *testing.T, overrides map[string]string) {
	t.Helper()
	all := []string{
		"DATABASE_URL", "ADMIN_SESSION_SECRET", "PORT", "APP_ENV", "FRONTEND_URL",
		"ALLOW_HTTP_APPLY_URLS", "LOG_LEVEL", "RUN_MIGRATIONS_ON_START",
	}
	for _, name := range all {
		t.Setenv(name, "")
	}
	for name, value := range overrides {
		t.Setenv(name, value)
	}
}

func baseEnv() map[string]string {
	return map[string]string{
		"DATABASE_URL":         goodDSN,
		"ADMIN_SESSION_SECRET": goodSecret,
		"FRONTEND_URL":         "https://jobpaglu.com",
	}
}

func TestLoadDefaults(t *testing.T) {
	setEnv(t, baseEnv())

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned %v, want nil", err)
	}

	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want the 8080 fallback", cfg.Port)
	}
	if cfg.AppEnv != EnvProduction {
		t.Errorf("AppEnv = %q, want %q — an unset APP_ENV must fail safe", cfg.AppEnv, EnvProduction)
	}
	if cfg.IsDevelopment() {
		t.Error("IsDevelopment() = true with APP_ENV unset")
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
	if cfg.AllowHTTPApplyURLs {
		t.Error("AllowHTTPApplyURLs = true by default; http apply URLs must be opt-in")
	}
	if !cfg.RunMigrationsOnStart {
		t.Error("RunMigrationsOnStart = false by default, want true")
	}
	if cfg.FrontendOrigin != "https://jobpaglu.com" {
		t.Errorf("FrontendOrigin = %q, want https://jobpaglu.com", cfg.FrontendOrigin)
	}
	if cfg.Addr() != ":8080" {
		t.Errorf("Addr() = %q, want :8080", cfg.Addr())
	}
}

func TestLoadFrontendOriginDropsPathAndKeepsPort(t *testing.T) {
	env := baseEnv()
	env["FRONTEND_URL"] = "http://localhost:4321/admin/"
	setEnv(t, env)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned %v", err)
	}
	// An Origin header never carries a path, so the comparison target must not.
	if cfg.FrontendOrigin != "http://localhost:4321" {
		t.Errorf("FrontendOrigin = %q, want http://localhost:4321", cfg.FrontendOrigin)
	}
}

func TestLoadRejections(t *testing.T) {
	tests := []struct {
		name      string
		overrides map[string]string
		wantInErr string
	}{
		{
			name:      "missing DATABASE_URL",
			overrides: map[string]string{"DATABASE_URL": ""},
			wantInErr: "DATABASE_URL is required",
		},
		{
			name:      "DATABASE_URL wrong scheme",
			overrides: map[string]string{"DATABASE_URL": "mysql://user@host/db"},
			wantInErr: "postgres://",
		},
		{
			name:      "missing ADMIN_SESSION_SECRET",
			overrides: map[string]string{"ADMIN_SESSION_SECRET": ""},
			wantInErr: "ADMIN_SESSION_SECRET is required",
		},
		{
			name:      "short ADMIN_SESSION_SECRET",
			overrides: map[string]string{"ADMIN_SESSION_SECRET": "tooshort"},
			wantInErr: "at least 32 characters",
		},
		{
			name:      "placeholder ADMIN_SESSION_SECRET",
			overrides: map[string]string{"ADMIN_SESSION_SECRET": "replace_me_with_a_real_random_secret_value"},
			wantInErr: "placeholder",
		},
		{
			name:      "uniform ADMIN_SESSION_SECRET",
			overrides: map[string]string{"ADMIN_SESSION_SECRET": strings.Repeat("a", 48)},
			wantInErr: "placeholder",
		},
		{
			name:      "missing FRONTEND_URL",
			overrides: map[string]string{"FRONTEND_URL": ""},
			wantInErr: "FRONTEND_URL is required",
		},
		{
			name:      "relative FRONTEND_URL",
			overrides: map[string]string{"FRONTEND_URL": "/jobs"},
			wantInErr: "absolute",
		},
		{
			name:      "non-numeric PORT",
			overrides: map[string]string{"PORT": "http"},
			wantInErr: "PORT must be a number",
		},
		{
			name:      "out-of-range PORT",
			overrides: map[string]string{"PORT": "70000"},
			wantInErr: "PORT must be a number",
		},
		{
			name:      "unknown APP_ENV",
			overrides: map[string]string{"APP_ENV": "staging"},
			wantInErr: "APP_ENV must be",
		},
		{
			name:      "unknown LOG_LEVEL",
			overrides: map[string]string{"LOG_LEVEL": "verbose"},
			wantInErr: "LOG_LEVEL must be",
		},
		{
			name:      "non-boolean ALLOW_HTTP_APPLY_URLS",
			overrides: map[string]string{"ALLOW_HTTP_APPLY_URLS": "maybe"},
			wantInErr: "ALLOW_HTTP_APPLY_URLS must be true or false",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env := baseEnv()
			for k, v := range tc.overrides {
				env[k] = v
			}
			setEnv(t, env)

			cfg, err := Load()
			if err == nil {
				t.Fatalf("Load() succeeded, want an error mentioning %q (got %s)", tc.wantInErr, cfg.Redacted())
			}
			if !strings.Contains(err.Error(), tc.wantInErr) {
				t.Errorf("Load() error = %q, want it to contain %q", err.Error(), tc.wantInErr)
			}
		})
	}
}

func TestLoadReportsEveryProblemAtOnce(t *testing.T) {
	setEnv(t, map[string]string{
		"DATABASE_URL":         "",
		"ADMIN_SESSION_SECRET": "",
		"FRONTEND_URL":         "",
	})

	_, err := Load()
	if err == nil {
		t.Fatal("Load() succeeded with three missing variables")
	}
	for _, want := range []string{"DATABASE_URL", "ADMIN_SESSION_SECRET", "FRONTEND_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %s; one restart per mistake is the failure mode this avoids:\n%s", want, err)
		}
	}
}

func TestBooleanAndLevelParsing(t *testing.T) {
	env := baseEnv()
	env["ALLOW_HTTP_APPLY_URLS"] = "TRUE"
	env["RUN_MIGRATIONS_ON_START"] = "off"
	env["LOG_LEVEL"] = "Debug"
	env["APP_ENV"] = "development"
	setEnv(t, env)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned %v", err)
	}
	if !cfg.AllowHTTPApplyURLs {
		t.Error(`ALLOW_HTTP_APPLY_URLS="TRUE" did not parse as true`)
	}
	if cfg.RunMigrationsOnStart {
		t.Error(`RUN_MIGRATIONS_ON_START="off" did not parse as false`)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, want debug", cfg.LogLevel)
	}
	if !cfg.IsDevelopment() {
		t.Error("IsDevelopment() = false with APP_ENV=development")
	}
}

// CookieName is the single place the `__Host-` decision is made, and it is
// coupled to SecureCookies because the prefix is only legal on a Secure cookie.
// Decoupling them would produce a cookie the browser silently discards.
func TestCookieNameFollowsSecureCookies(t *testing.T) {
	for appEnv, want := range map[string]string{
		EnvProduction:  "__Host-jp_session",
		EnvDevelopment: "jp_session",
	} {
		env := baseEnv()
		env["APP_ENV"] = appEnv
		setEnv(t, env)

		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load() with APP_ENV=%s returned %v", appEnv, err)
		}
		if got := cfg.CookieName("jp_session"); got != want {
			t.Errorf("APP_ENV=%s: CookieName() = %q, want %q", appEnv, got, want)
		}
		if cfg.SecureCookies() != (appEnv == EnvProduction) {
			t.Errorf("APP_ENV=%s: SecureCookies() = %v", appEnv, cfg.SecureCookies())
		}
		// The prefix must never appear without Secure, in either direction.
		prefixed := strings.HasPrefix(cfg.CookieName("jp_session"), HostCookiePrefix)
		if prefixed != cfg.SecureCookies() {
			t.Errorf("APP_ENV=%s: prefix=%v but SecureCookies=%v; the two must agree",
				appEnv, prefixed, cfg.SecureCookies())
		}
	}
}

// The one test in this package that is really a security control: no code path
// that stringifies a Config may emit a secret.
func TestRedactedHidesSecrets(t *testing.T) {
	setEnv(t, baseEnv())
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() returned %v", err)
	}

	for _, rendered := range []string{cfg.Redacted(), cfg.String(), cfg.LogValue().String()} {
		if strings.Contains(rendered, goodSecret) {
			t.Errorf("rendered config leaked ADMIN_SESSION_SECRET: %s", rendered)
		}
		if strings.Contains(rendered, "pw@") || strings.Contains(rendered, goodDSN) {
			t.Errorf("rendered config leaked DATABASE_URL: %s", rendered)
		}
		if !strings.Contains(rendered, "<redacted>") {
			t.Errorf("rendered config has no <redacted> marker: %s", rendered)
		}
	}
}
