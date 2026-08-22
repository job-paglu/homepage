//go:build integration

// Package tests holds the end-to-end suite: a real HTTP server, a real router,
// a real Postgres.
//
// Build-tagged so `go test ./...` on a laptop with no database still passes.
// Run it with:
//
//	TEST_DATABASE_URL=postgres://... go test -tags=integration ./tests/...
//
// Everything here exercises behaviour that cannot be proven with a fake: the
// visibility predicate is in SQL, the CSRF check is in middleware, the rate limit
// spans requests, and the soft delete is a column. Those are exactly the places a
// unit test would be testing its own mock.
package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/job-paglu/homepage/backend/internal/auth"
	"github.com/job-paglu/homepage/backend/internal/config"
	"github.com/job-paglu/homepage/backend/internal/database"
	"github.com/job-paglu/homepage/backend/internal/jobs"
	"github.com/job-paglu/homepage/backend/internal/server"
	"github.com/job-paglu/homepage/backend/internal/validation"
	"github.com/job-paglu/homepage/backend/migrations"
)

const (
	frontendOrigin = "http://localhost:4321"
	adminEmail     = "integration@example.test"
	adminPassword  = "integration-test-password"
)

// ------------------------------------------------------------------ harness

type harness struct {
	t      *testing.T
	server *httptest.Server
	pool   *pgxpool.Pool
	client *http.Client
	csrf   string
	// cfg is kept because the cookie names are configuration-derived: the suite
	// runs with APP_ENV=development, so it must look for `jp_session` rather than
	// the production `__Host-jp_session`. Deriving the name the same way the
	// server does keeps the test honest if that mapping ever changes.
	cfg *config.Config
}

// newHarness brings up the full stack against TEST_DATABASE_URL.
//
// APP_ENV is development so the session cookie is not Secure: httptest serves
// plain HTTP, and a Secure cookie would be silently dropped by the cookie jar,
// producing a wall of 401s that look like an auth bug.
func newHarness(t *testing.T) *harness {
	t.Helper()

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping the integration suite")
	}

	t.Setenv("DATABASE_URL", dsn)
	t.Setenv("ADMIN_SESSION_SECRET", "integration-suite-session-secret-0123456789")
	t.Setenv("FRONTEND_URL", frontendOrigin)
	t.Setenv("APP_ENV", "development")
	t.Setenv("LOG_LEVEL", "error")
	t.Setenv("ALLOW_HTTP_APPLY_URLS", "false")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() = %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()

	pool, err := database.Open(ctx, cfg, logger)
	if err != nil {
		t.Fatalf("database.Open() = %v", err)
	}
	if err := database.Migrate(ctx, pool, migrations.FS, logger); err != nil {
		t.Fatalf("database.Migrate() = %v", err)
	}

	// A clean slate per test. Sessions cascade from admin_users, so removing the
	// test administrator removes its sessions too.
	truncate(t, pool)

	hash, err := auth.Hash(adminPassword)
	if err != nil {
		t.Fatalf("auth.Hash() = %v", err)
	}
	store := auth.NewStore(pool)
	if _, err := store.UpsertAdmin(ctx, adminEmail, "Integration Admin", hash); err != nil {
		t.Fatalf("UpsertAdmin() = %v", err)
	}

	service := jobs.NewService(jobs.NewRepository(pool), validation.ApplyURLPolicy{})
	handler := server.New(server.Deps{
		Config:     cfg,
		Logger:     logger,
		Pool:       pool,
		PublicJobs: jobs.NewPublicHandler(service),
		AdminJobs:  jobs.NewAdminHandler(service),
		Auth:       auth.NewHandler(store, cfg, logger),
	})

	ts := httptest.NewServer(handler)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New() = %v", err)
	}

	h := &harness{
		t:      t,
		server: ts,
		pool:   pool,
		client: &http.Client{Jar: jar, Timeout: 20 * time.Second},
		cfg:    cfg,
	}
	t.Cleanup(func() {
		ts.Close()
		pool.Close()
	})
	return h
}

// newOutageHarness builds the same stack against a pool that points at nothing.
//
// pgxpool.New is lazy — it does not dial until the first query — so this is a
// faithful simulation of "Neon is unreachable" without needing a real outage,
// and it is closer to production than closing a working pool would be: a closed
// pool is a shutdown condition, an unreachable host is the one that happens at
// three in the morning.
func newOutageHarness(t *testing.T) *harness {
	t.Helper()

	if os.Getenv("TEST_DATABASE_URL") == "" {
		t.Skip("TEST_DATABASE_URL is not set; skipping the integration suite")
	}

	// Port 1 on the loopback: nothing listens there, and the connection is
	// refused immediately rather than timing out.
	t.Setenv("DATABASE_URL", "postgres://nobody:nothing@127.0.0.1:1/nowhere?sslmode=disable")
	t.Setenv("ADMIN_SESSION_SECRET", "integration-suite-session-secret-0123456789")
	t.Setenv("FRONTEND_URL", frontendOrigin)
	t.Setenv("APP_ENV", "development")
	t.Setenv("LOG_LEVEL", "error")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load() = %v", err)
	}

	pool, err := pgxpool.New(context.Background(), cfg.DatabaseURL)
	if err != nil {
		t.Fatalf("pgxpool.New() = %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := jobs.NewService(jobs.NewRepository(pool), validation.ApplyURLPolicy{})
	ts := httptest.NewServer(server.New(server.Deps{
		Config:     cfg,
		Logger:     logger,
		Pool:       pool,
		PublicJobs: jobs.NewPublicHandler(service),
		AdminJobs:  jobs.NewAdminHandler(service),
		Auth:       auth.NewHandler(auth.NewStore(pool), cfg, logger),
	}))

	t.Cleanup(func() {
		ts.Close()
		pool.Close()
	})

	return &harness{t: t, server: ts, pool: pool, client: &http.Client{Timeout: 20 * time.Second}}
}

func truncate(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for _, statement := range []string{
		`DELETE FROM sessions`,
		`DELETE FROM jobs`,
		`DELETE FROM admin_users`,
	} {
		if _, err := pool.Exec(context.Background(), statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
}

type response struct {
	status  int
	headers http.Header
	body    []byte
}

func (r response) code() string {
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(r.body, &envelope)
	return envelope.Error.Code
}

func (r response) data(t *testing.T, into any) {
	t.Helper()
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(r.body, &envelope); err != nil {
		t.Fatalf("response is not an envelope: %v (%s)", err, r.body)
	}
	if err := json.Unmarshal(envelope.Data, into); err != nil {
		t.Fatalf("data does not match the expected shape: %v (%s)", err, envelope.Data)
	}
}

// do issues a request with the Origin header set, since every state-changing
// admin route requires it.
func (h *harness) do(method, path string, body any) response {
	h.t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			h.t.Fatalf("marshalling the request body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		h.t.Fatalf("building the request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Origin", frontendOrigin)
	if h.csrf != "" {
		req.Header.Set(auth.CSRFHeaderName, h.csrf)
	}

	res, err := h.client.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()

	payload, err := io.ReadAll(res.Body)
	if err != nil {
		h.t.Fatalf("reading the response body: %v", err)
	}
	return response{status: res.StatusCode, headers: res.Header, body: payload}
}

// doRaw issues a request with full control over the headers, for the cases where
// a missing header is the thing being tested.
func (h *harness) doRaw(method, path string, body string, headers map[string]string) response {
	h.t.Helper()

	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		h.t.Fatalf("building the request: %v", err)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}

	res, err := h.client.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()

	payload, err := io.ReadAll(res.Body)
	if err != nil {
		h.t.Fatalf("reading the response body: %v", err)
	}
	return response{status: res.StatusCode, headers: res.Header, body: payload}
}

func (h *harness) login() {
	h.t.Helper()

	res := h.doRaw(http.MethodPost, "/api/admin/login",
		fmt.Sprintf(`{"email":%q,"password":%q}`, adminEmail, adminPassword),
		map[string]string{"Content-Type": "application/json", "Origin": frontendOrigin})

	if res.status != http.StatusOK {
		h.t.Fatalf("login: status = %d, want 200 (%s)", res.status, res.body)
	}

	var session struct {
		Admin     map[string]any `json:"admin"`
		CSRFToken string         `json:"csrf_token"`
	}
	res.data(h.t, &session)
	if session.CSRFToken == "" {
		h.t.Fatal("login returned no csrf_token")
	}
	h.csrf = session.CSRFToken
}

func validJobBody() map[string]any {
	return map[string]any{
		"title":         "Senior Cloud Engineer",
		"description":   "Own the platform that serves millions of requests a day. Remote friendly.",
		"salary":        "$140k - $175k",
		"qualification": "B.Sc. Computer Science or equivalent experience",
		"job_type":      "full-time",
		"location":      "Berlin, Germany",
		"apply_url":     "https://example.com/careers/senior-cloud-engineer",
	}
}

// createJob posts a job and returns its id and slug.
func (h *harness) createJob(overrides map[string]any) (id, slug string) {
	h.t.Helper()

	body := validJobBody()
	for key, value := range overrides {
		body[key] = value
	}

	res := h.do(http.MethodPost, "/api/admin/jobs", body)
	if res.status != http.StatusCreated {
		h.t.Fatalf("create job: status = %d, want 201 (%s)", res.status, res.body)
	}

	var job struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	}
	res.data(h.t, &job)
	return job.ID, job.Slug
}

// ------------------------------------------------------------------ probes

func TestHealthAndReadyWithAHealthyDatabase(t *testing.T) {
	h := newHarness(t)

	for path, want := range map[string]string{"/health": "ok", "/ready": "ready"} {
		res := h.doRaw(http.MethodGet, path, "", nil)
		if res.status != http.StatusOK {
			t.Fatalf("%s = %d, want 200 (%s)", path, res.status, res.body)
		}
		var status struct {
			Status string `json:"status"`
		}
		res.data(t, &status)
		if status.Status != want {
			t.Errorf("%s status = %q, want %q", path, status.Status, want)
		}
	}
}

// Liveness must not depend on a dependency, or the platform restarts a perfectly
// healthy instance because Neon is asleep. Readiness must.
func TestHealthSurvivesAnOutageAndReadyDoesNot(t *testing.T) {
	h := newOutageHarness(t)

	res := h.doRaw(http.MethodGet, "/health", "", nil)
	if res.status != http.StatusOK {
		t.Errorf("/health during an outage = %d, want 200 (%s)", res.status, res.body)
	}

	res = h.doRaw(http.MethodGet, "/ready", "", nil)
	if res.status != http.StatusServiceUnavailable {
		t.Errorf("/ready during an outage = %d, want 503 (%s)", res.status, res.body)
	}
	if got := res.code(); got != "SERVICE_UNAVAILABLE" {
		t.Errorf("/ready error code = %q, want SERVICE_UNAVAILABLE", got)
	}
}

// A database outage is a 503, never a 500 and never a panic. The frontend
// renders 503 as retryable, so getting this wrong changes what a visitor sees.
func TestPublicEndpointsDegradeTo503(t *testing.T) {
	h := newOutageHarness(t)

	for _, path := range []string{"/api/jobs", "/api/jobs/anything"} {
		res := h.doRaw(http.MethodGet, path, "", nil)
		if res.status != http.StatusServiceUnavailable {
			t.Errorf("%s during an outage = %d, want 503 (%s)", path, res.status, res.body)
		}
		if got := res.code(); got != "SERVICE_UNAVAILABLE" {
			t.Errorf("%s error code = %q, want SERVICE_UNAVAILABLE", path, got)
		}
		if strings.Contains(string(res.body), "127.0.0.1") {
			t.Errorf("%s leaked the connection target: %s", path, res.body)
		}
	}
}

// -------------------------------------------------------- access control

// Every admin route rejects an anonymous caller. Logout is excluded on purpose:
// the contract makes it idempotent, so no session is a 204.
func TestAdminRoutesRequireASession(t *testing.T) {
	h := newHarness(t)

	routes := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/admin/me"},
		{http.MethodGet, "/api/admin/jobs"},
		{http.MethodGet, "/api/admin/jobs/8f14e45f-ceea-467a-9c5b-2b1a0e1c9d33"},
		{http.MethodPost, "/api/admin/jobs"},
		{http.MethodPut, "/api/admin/jobs/8f14e45f-ceea-467a-9c5b-2b1a0e1c9d33"},
		{http.MethodDelete, "/api/admin/jobs/8f14e45f-ceea-467a-9c5b-2b1a0e1c9d33"},
		{http.MethodPost, "/api/admin/jobs/8f14e45f-ceea-467a-9c5b-2b1a0e1c9d33/publish"},
		{http.MethodPost, "/api/admin/jobs/8f14e45f-ceea-467a-9c5b-2b1a0e1c9d33/unpublish"},
	}

	for _, route := range routes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			res := h.doRaw(route.method, route.path, `{}`, map[string]string{
				"Content-Type": "application/json",
				"Origin":       frontendOrigin,
			})
			if res.status != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401 (%s)", res.status, res.body)
			}
			if got := res.code(); got != "UNAUTHORIZED" {
				t.Errorf("code = %q, want UNAUTHORIZED", got)
			}
		})
	}

	// Logout without a session is still 204.
	res := h.doRaw(http.MethodPost, "/api/admin/logout", "", map[string]string{"Origin": frontendOrigin})
	if res.status != http.StatusNoContent {
		t.Errorf("logout without a session = %d, want 204 (%s)", res.status, res.body)
	}
}

func TestStateChangingRequestsRequireCSRF(t *testing.T) {
	h := newHarness(t)
	h.login()
	id, _ := h.createJob(nil)

	writes := []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodPost, "/api/admin/jobs", `{}`},
		{http.MethodPut, "/api/admin/jobs/" + id, `{}`},
		{http.MethodDelete, "/api/admin/jobs/" + id, ""},
		{http.MethodPost, "/api/admin/jobs/" + id + "/publish", ""},
		{http.MethodPost, "/api/admin/jobs/" + id + "/unpublish", ""},
	}

	for _, w := range writes {
		t.Run("missing token: "+w.method+" "+w.path, func(t *testing.T) {
			res := h.doRaw(w.method, w.path, w.body, map[string]string{
				"Content-Type": "application/json",
				"Origin":       frontendOrigin,
			})
			if res.status != http.StatusForbidden {
				t.Errorf("status = %d, want 403 (%s)", res.status, res.body)
			}
			if got := res.code(); got != "CSRF_INVALID" {
				t.Errorf("code = %q, want CSRF_INVALID", got)
			}
		})

		t.Run("wrong token: "+w.method+" "+w.path, func(t *testing.T) {
			res := h.doRaw(w.method, w.path, w.body, map[string]string{
				"Content-Type":      "application/json",
				"Origin":            frontendOrigin,
				auth.CSRFHeaderName: "not-the-session-token",
			})
			if res.status != http.StatusForbidden {
				t.Errorf("status = %d, want 403 (%s)", res.status, res.body)
			}
		})
	}

	// A read is not state-changing and needs no token.
	res := h.doRaw(http.MethodGet, "/api/admin/jobs", "", nil)
	if res.status != http.StatusOK {
		t.Errorf("GET without a CSRF token = %d, want 200 (%s)", res.status, res.body)
	}
}

func TestStateChangingRequestsRequireTheConfiguredOrigin(t *testing.T) {
	h := newHarness(t)
	h.login()

	for name, origin := range map[string]string{
		"missing":                 "",
		"foreign":                 "https://evil.example",
		"sandboxed null":          "null",
		"right host wrong scheme": "https://localhost:4321",
		"right host wrong port":   "http://localhost:4322",
	} {
		t.Run(name, func(t *testing.T) {
			headers := map[string]string{
				"Content-Type":      "application/json",
				auth.CSRFHeaderName: h.csrf,
			}
			if origin != "" {
				headers["Origin"] = origin
			}

			res := h.doRaw(http.MethodPost, "/api/admin/jobs", `{}`, headers)
			if res.status != http.StatusForbidden {
				t.Errorf("status = %d, want 403 (%s)", res.status, res.body)
			}
			if got := res.code(); got != "CSRF_INVALID" {
				t.Errorf("code = %q, want CSRF_INVALID", got)
			}
		})
	}
}

// ------------------------------------------------------- public visibility

// The single most important behaviour in the API: nothing that is not published,
// live and undeleted may be reachable publicly, by slug or in a list.
func TestNonPublishedJobsAreInvisiblePublicly(t *testing.T) {
	h := newHarness(t)
	h.login()

	// draft
	_, draftSlug := h.createJob(map[string]any{"title": "Draft Job"})

	// unpublished
	unpublishedID, unpublishedSlug := h.createJob(map[string]any{"title": "Unpublished Job"})
	if res := h.do(http.MethodPost, "/api/admin/jobs/"+unpublishedID+"/publish", nil); res.status != http.StatusOK {
		t.Fatalf("publish: %d (%s)", res.status, res.body)
	}
	if res := h.do(http.MethodPost, "/api/admin/jobs/"+unpublishedID+"/unpublish", nil); res.status != http.StatusOK {
		t.Fatalf("unpublish: %d (%s)", res.status, res.body)
	}

	// published then soft-deleted
	deletedID, deletedSlug := h.createJob(map[string]any{"title": "Deleted Job"})
	if res := h.do(http.MethodPost, "/api/admin/jobs/"+deletedID+"/publish", nil); res.status != http.StatusOK {
		t.Fatalf("publish: %d (%s)", res.status, res.body)
	}
	if res := h.do(http.MethodDelete, "/api/admin/jobs/"+deletedID, nil); res.status != http.StatusNoContent {
		t.Fatalf("delete: %d (%s)", res.status, res.body)
	}

	// published but already expired. expires_at must be in the future on create,
	// so it is moved into the past directly — which is also a fair simulation of
	// a job that simply aged out.
	expiredID, expiredSlug := h.createJob(map[string]any{
		"title":      "Expired Job",
		"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
	})
	if res := h.do(http.MethodPost, "/api/admin/jobs/"+expiredID+"/publish", nil); res.status != http.StatusOK {
		t.Fatalf("publish: %d (%s)", res.status, res.body)
	}
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE jobs SET expires_at = now() - interval '1 hour' WHERE id = $1::uuid`, expiredID); err != nil {
		t.Fatalf("ageing out the expired job: %v", err)
	}

	// One genuinely visible job, so an empty list would not pass by accident.
	visibleID, visibleSlug := h.createJob(map[string]any{"title": "Visible Job"})
	if res := h.do(http.MethodPost, "/api/admin/jobs/"+visibleID+"/publish", nil); res.status != http.StatusOK {
		t.Fatalf("publish: %d (%s)", res.status, res.body)
	}

	for name, slug := range map[string]string{
		"draft":        draftSlug,
		"unpublished":  unpublishedSlug,
		"soft-deleted": deletedSlug,
		"expired":      expiredSlug,
		"nonexistent":  "no-such-job",
	} {
		t.Run("detail 404: "+name, func(t *testing.T) {
			res := h.doRaw(http.MethodGet, "/api/jobs/"+url.PathEscape(slug), "", nil)
			if res.status != http.StatusNotFound {
				t.Errorf("status = %d, want 404 (%s)", res.status, res.body)
			}
			if got := res.code(); got != "JOB_NOT_FOUND" {
				t.Errorf("code = %q, want JOB_NOT_FOUND", got)
			}
		})
	}

	res := h.doRaw(http.MethodGet, "/api/jobs?per_page=50", "", nil)
	if res.status != http.StatusOK {
		t.Fatalf("list: %d (%s)", res.status, res.body)
	}

	var listed []struct {
		Slug   string `json:"slug"`
		Status string `json:"status"`
	}
	res.data(t, &listed)

	if len(listed) != 1 || listed[0].Slug != visibleSlug {
		t.Fatalf("public list = %+v, want only %q", listed, visibleSlug)
	}
	if listed[0].Status != "published" {
		t.Errorf("status = %q on a public listing, want published", listed[0].Status)
	}

	var meta struct {
		Total int64 `json:"total"`
	}
	var envelope struct {
		Meta json.RawMessage `json:"meta"`
	}
	if err := json.Unmarshal(res.body, &envelope); err != nil {
		t.Fatalf("unmarshalling the envelope: %v", err)
	}
	if err := json.Unmarshal(envelope.Meta, &meta); err != nil {
		t.Fatalf("unmarshalling meta: %v", err)
	}
	if meta.Total != 1 {
		t.Errorf("meta.total = %d, want 1 — the count must use the same predicate as the rows", meta.Total)
	}
}

func TestEmptyListIs200NotNotFound(t *testing.T) {
	h := newHarness(t)

	res := h.doRaw(http.MethodGet, "/api/jobs", "", nil)
	if res.status != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.status)
	}
	if !strings.Contains(string(res.body), `"data":[]`) {
		t.Errorf("body = %s, want an empty array rather than null", res.body)
	}
}

// ------------------------------------------------------- publish semantics

func TestPublishStampsPublishedAtOnceAndUnpublishHidesImmediately(t *testing.T) {
	h := newHarness(t)
	h.login()
	id, slug := h.createJob(nil)

	type job struct {
		Status      string  `json:"status"`
		PublishedAt *string `json:"published_at"`
	}

	res := h.do(http.MethodPost, "/api/admin/jobs/"+id+"/publish", nil)
	if res.status != http.StatusOK {
		t.Fatalf("publish: %d (%s)", res.status, res.body)
	}
	var first job
	res.data(t, &first)
	if first.Status != "published" || first.PublishedAt == nil {
		t.Fatalf("after publish: %+v, want published with a published_at", first)
	}
	original := *first.PublishedAt

	// Visible publicly, immediately.
	if res := h.doRaw(http.MethodGet, "/api/jobs/"+slug, "", nil); res.status != http.StatusOK {
		t.Errorf("public detail after publish = %d, want 200 (%s)", res.status, res.body)
	}

	// Unpublish hides it with no cache-driven delay on the API side.
	if res := h.do(http.MethodPost, "/api/admin/jobs/"+id+"/unpublish", nil); res.status != http.StatusOK {
		t.Fatalf("unpublish: %d (%s)", res.status, res.body)
	}
	if res := h.doRaw(http.MethodGet, "/api/jobs/"+slug, "", nil); res.status != http.StatusNotFound {
		t.Errorf("public detail after unpublish = %d, want 404 (%s)", res.status, res.body)
	}

	// Re-publishing keeps the original date.
	time.Sleep(1100 * time.Millisecond) // RFC 3339 second precision needs a tick
	res = h.do(http.MethodPost, "/api/admin/jobs/"+id+"/publish", nil)
	if res.status != http.StatusOK {
		t.Fatalf("re-publish: %d (%s)", res.status, res.body)
	}
	var second job
	res.data(t, &second)
	if second.PublishedAt == nil || *second.PublishedAt != original {
		t.Errorf("published_at = %v after re-publish, want the original %q", second.PublishedAt, original)
	}
}

func TestPublishRefusesAJobWithAnUnusableApplyURL(t *testing.T) {
	h := newHarness(t)
	h.login()
	id, _ := h.createJob(nil)

	// Bypass the API to plant what a manual database edit could plant.
	if _, err := h.pool.Exec(context.Background(),
		`UPDATE jobs SET apply_url = $2 WHERE id = $1::uuid`, id, "javascript:alert(1)"); err != nil {
		t.Fatalf("planting a bad apply_url: %v", err)
	}

	res := h.do(http.MethodPost, "/api/admin/jobs/"+id+"/publish", nil)
	if res.status != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422 (%s)", res.status, res.body)
	}
	if got := res.code(); got != "VALIDATION_FAILED" {
		t.Errorf("code = %q, want VALIDATION_FAILED", got)
	}
}

// --------------------------------------------------------------- soft delete

func TestDeleteIsSoftAndTheRowSurvives(t *testing.T) {
	h := newHarness(t)
	h.login()
	id, slug := h.createJob(nil)

	if res := h.do(http.MethodDelete, "/api/admin/jobs/"+id, nil); res.status != http.StatusNoContent {
		t.Fatalf("delete: %d (%s)", res.status, res.body)
	}

	var deletedAt *time.Time
	err := h.pool.QueryRow(context.Background(),
		`SELECT deleted_at FROM jobs WHERE id = $1::uuid`, id).Scan(&deletedAt)
	if err != nil {
		t.Fatalf("the row was physically removed: %v", err)
	}
	if deletedAt == nil {
		t.Error("deleted_at is null after a delete")
	}

	// Gone from the admin list, the admin detail, and the public detail.
	if res := h.do(http.MethodGet, "/api/admin/jobs/"+id, nil); res.status != http.StatusNotFound {
		t.Errorf("admin detail after delete = %d, want 404", res.status)
	}
	if res := h.doRaw(http.MethodGet, "/api/jobs/"+slug, "", nil); res.status != http.StatusNotFound {
		t.Errorf("public detail after delete = %d, want 404", res.status)
	}
	if res := h.do(http.MethodDelete, "/api/admin/jobs/"+id, nil); res.status != http.StatusNotFound {
		t.Errorf("second delete = %d, want 404", res.status)
	}

	// And the slug is immediately reusable, which is what the partial unique
	// index is for.
	if _, reused := h.createJob(map[string]any{"slug": slug}); reused != slug {
		t.Errorf("reused slug = %q, want %q", reused, slug)
	}
}

// ---------------------------------------------------------------- rate limit

func TestSixRapidBadLoginsAreRateLimited(t *testing.T) {
	h := newHarness(t)

	headers := map[string]string{"Content-Type": "application/json", "Origin": frontendOrigin}
	body := fmt.Sprintf(`{"email":%q,"password":"wrong-password-entirely"}`, adminEmail)

	// Five failures are allowed (contract section 4: 5 per IP in 15 minutes).
	for attempt := 1; attempt <= 5; attempt++ {
		res := h.doRaw(http.MethodPost, "/api/admin/login", body, headers)
		if res.status != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401 (%s)", attempt, res.status, res.body)
		}
		if got := res.code(); got != "INVALID_CREDENTIALS" {
			t.Fatalf("attempt %d: code = %q, want INVALID_CREDENTIALS", attempt, got)
		}
	}

	// The sixth is refused before the password is even considered.
	res := h.doRaw(http.MethodPost, "/api/admin/login", body, headers)
	if res.status != http.StatusTooManyRequests {
		t.Fatalf("sixth attempt: status = %d, want 429 (%s)", res.status, res.body)
	}
	if got := res.code(); got != "RATE_LIMITED" {
		t.Errorf("code = %q, want RATE_LIMITED", got)
	}
	retryAfter := res.headers.Get("Retry-After")
	if retryAfter == "" {
		t.Error("Retry-After is missing; a client has nothing to wait on")
	}
	if retryAfter == "0" {
		t.Error("Retry-After: 0 tells the client to retry immediately")
	}

	// The correct password is refused too: the limit is on the source, not on
	// the guess.
	correct := fmt.Sprintf(`{"email":%q,"password":%q}`, adminEmail, adminPassword)
	if res := h.doRaw(http.MethodPost, "/api/admin/login", correct, headers); res.status != http.StatusTooManyRequests {
		t.Errorf("correct password while limited = %d, want 429", res.status)
	}
}

func TestUnknownEmailAndWrongPasswordAreIndistinguishable(t *testing.T) {
	h := newHarness(t)
	headers := map[string]string{"Content-Type": "application/json", "Origin": frontendOrigin}

	unknown := h.doRaw(http.MethodPost, "/api/admin/login",
		`{"email":"nobody@example.test","password":"some-password-here"}`, headers)
	wrong := h.doRaw(http.MethodPost, "/api/admin/login",
		fmt.Sprintf(`{"email":%q,"password":"some-password-here"}`, adminEmail), headers)

	if unknown.status != http.StatusUnauthorized || wrong.status != http.StatusUnauthorized {
		t.Fatalf("statuses = %d and %d, want both 401", unknown.status, wrong.status)
	}

	// Bodies must match apart from the request id, which is per-request.
	strip := func(body []byte) string {
		var envelope struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(body, &envelope)
		return envelope.Error.Code + "|" + envelope.Error.Message
	}
	if strip(unknown.body) != strip(wrong.body) {
		t.Errorf("bodies differ:\n unknown email: %s\n wrong password: %s", unknown.body, wrong.body)
	}
}

// ------------------------------------------------------------------- session

func TestLoginRotatesTheSessionToken(t *testing.T) {
	h := newHarness(t)

	h.login()
	firstToken := h.sessionCookie()

	h.login()
	secondToken := h.sessionCookie()

	if firstToken == "" || secondToken == "" {
		t.Fatal("no session cookie was set")
	}
	if firstToken == secondToken {
		t.Error("the session token did not change on re-login; that is session fixation")
	}

	// The old token must be dead server-side, not merely replaced in the jar. A
	// jar-less client is used so the new cookie is not sent alongside the old one.
	req, err := http.NewRequest(http.MethodGet, h.server.URL+"/api/admin/me", nil)
	if err != nil {
		t.Fatalf("building the request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName(h.cfg), Value: firstToken})

	res, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("replaying the old session: %v", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("the rotated-away session still works: %d", res.StatusCode)
	}
}

func (h *harness) sessionCookie() string {
	h.t.Helper()
	parsed, err := url.Parse(h.server.URL)
	if err != nil {
		h.t.Fatalf("parsing the test server URL: %v", err)
	}
	for _, cookie := range h.client.Jar.Cookies(parsed) {
		if cookie.Name == auth.SessionCookieName(h.cfg) {
			return cookie.Value
		}
	}
	return ""
}

func TestLogoutInvalidatesTheSession(t *testing.T) {
	h := newHarness(t)
	h.login()

	if res := h.do(http.MethodPost, "/api/admin/logout", nil); res.status != http.StatusNoContent {
		t.Fatalf("logout: %d (%s)", res.status, res.body)
	}
	if res := h.doRaw(http.MethodGet, "/api/admin/me", "", nil); res.status != http.StatusUnauthorized {
		t.Errorf("me after logout = %d, want 401", res.status)
	}
	// Idempotent.
	if res := h.doRaw(http.MethodPost, "/api/admin/logout", "", map[string]string{"Origin": frontendOrigin}); res.status != http.StatusNoContent {
		t.Errorf("second logout = %d, want 204", res.status)
	}
}

// ---------------------------------------------------------- routing surface

func TestRoutingErrorsUseTheJSONEnvelope(t *testing.T) {
	h := newHarness(t)

	t.Run("unknown route is NOT_FOUND", func(t *testing.T) {
		res := h.doRaw(http.MethodGet, "/api/nope", "", nil)
		if res.status != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", res.status)
		}
		if got := res.code(); got != "NOT_FOUND" {
			t.Errorf("code = %q, want NOT_FOUND (body: %s)", got, res.body)
		}
		if ct := res.headers.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("Content-Type = %q, want JSON — net/http's plain-text 404 must not escape", ct)
		}
	})

	t.Run("wrong method is METHOD_NOT_ALLOWED", func(t *testing.T) {
		res := h.doRaw(http.MethodDelete, "/api/jobs", "", nil)
		if res.status != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405 (%s)", res.status, res.body)
		}
		if got := res.code(); got != "METHOD_NOT_ALLOWED" {
			t.Errorf("code = %q, want METHOD_NOT_ALLOWED", got)
		}
	})
}

func TestBodyOverTheCapIs413(t *testing.T) {
	h := newHarness(t)
	h.login()

	oversized := fmt.Sprintf(`{"title":%q}`, strings.Repeat("x", 70*1024))
	res := h.doRaw(http.MethodPost, "/api/admin/jobs", oversized, map[string]string{
		"Content-Type":      "application/json",
		"Origin":            frontendOrigin,
		auth.CSRFHeaderName: h.csrf,
	})

	if res.status != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (%s)", res.status, res.body)
	}
	if got := res.code(); got != "PAYLOAD_TOO_LARGE" {
		t.Errorf("code = %q, want PAYLOAD_TOO_LARGE", got)
	}
}

func TestBadQueryParametersAre400(t *testing.T) {
	h := newHarness(t)

	for _, query := range []string{
		"?page=abc",
		"?per_page=lots",
		"?job_type=permanent",
		"?remote_type=anywhere",
		"?featured=maybe",
	} {
		res := h.doRaw(http.MethodGet, "/api/jobs"+query, "", nil)
		if res.status != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400 (%s)", query, res.status, res.body)
		}
		if got := res.code(); got != "BAD_REQUEST" {
			t.Errorf("%s code = %q, want BAD_REQUEST", query, got)
		}
	}
}

func TestExplicitSlugConflictIs409(t *testing.T) {
	h := newHarness(t)
	h.login()
	_, slug := h.createJob(nil)

	body := validJobBody()
	body["slug"] = slug
	body["title"] = "A Different Title"

	res := h.do(http.MethodPost, "/api/admin/jobs", body)
	if res.status != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (%s)", res.status, res.body)
	}
	if got := res.code(); got != "SLUG_CONFLICT" {
		t.Errorf("code = %q, want SLUG_CONFLICT", got)
	}

	// A derived slug never conflicts: it is de-duplicated instead.
	_, second := h.createJob(nil)
	if second != slug+"-2" {
		t.Errorf("second derived slug = %q, want %q", second, slug+"-2")
	}
}

func TestCacheHeaders(t *testing.T) {
	h := newHarness(t)
	h.login()

	id, slug := h.createJob(nil)
	if res := h.do(http.MethodPost, "/api/admin/jobs/"+id+"/publish", nil); res.status != http.StatusOK {
		t.Fatalf("publish: %d (%s)", res.status, res.body)
	}

	tests := []struct {
		name string
		got  string
		want string
	}{
		{
			name: "public list is CDN-cacheable for a minute",
			got:  h.doRaw(http.MethodGet, "/api/jobs", "", nil).headers.Get("Cache-Control"),
			want: "public, max-age=0, s-maxage=60, stale-while-revalidate=300",
		},
		{
			name: "public detail is CDN-cacheable for five minutes",
			got:  h.doRaw(http.MethodGet, "/api/jobs/"+slug, "", nil).headers.Get("Cache-Control"),
			want: "public, max-age=0, s-maxage=300, stale-while-revalidate=600",
		},
		{
			// An admin list cached anywhere is both a staleness bug and a
			// disclosure risk.
			name: "admin list is never stored",
			got:  h.do(http.MethodGet, "/api/admin/jobs", nil).headers.Get("Cache-Control"),
			want: "no-store",
		},
		{
			name: "admin detail is never stored",
			got:  h.do(http.MethodGet, "/api/admin/jobs/"+id, nil).headers.Get("Cache-Control"),
			want: "no-store",
		},
	}

	for _, tc := range tests {
		if tc.got != tc.want {
			t.Errorf("%s: Cache-Control = %q, want %q", tc.name, tc.got, tc.want)
		}
	}
}

func TestSecurityHeadersArePresent(t *testing.T) {
	h := newHarness(t)

	headers := h.doRaw(http.MethodGet, "/api/jobs", "", nil).headers
	for name, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := headers.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if csp := headers.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Errorf("Content-Security-Policy = %q, want frame-ancestors 'none'", csp)
	}
	if headers.Get("X-Request-Id") == "" {
		t.Error("X-Request-Id is missing; a user-reported failure would not be traceable")
	}
}

func TestCORSAllowsTheFrontendWithCredentialsAndOthersWithout(t *testing.T) {
	h := newHarness(t)

	t.Run("configured origin gets credentials", func(t *testing.T) {
		headers := h.doRaw(http.MethodGet, "/api/jobs", "", map[string]string{"Origin": frontendOrigin}).headers
		if got := headers.Get("Access-Control-Allow-Origin"); got != frontendOrigin {
			t.Errorf("Allow-Origin = %q, want %q — never a wildcard for a credentialed origin", got, frontendOrigin)
		}
		if got := headers.Get("Access-Control-Allow-Credentials"); got != "true" {
			t.Errorf("Allow-Credentials = %q, want true", got)
		}
	})

	t.Run("other origins may read public jobs without credentials", func(t *testing.T) {
		headers := h.doRaw(http.MethodGet, "/api/jobs", "", map[string]string{"Origin": "https://elsewhere.example"}).headers
		if got := headers.Get("Access-Control-Allow-Origin"); got != "*" {
			t.Errorf("Allow-Origin = %q, want * for an anonymous public read", got)
		}
		if got := headers.Get("Access-Control-Allow-Credentials"); got != "" {
			t.Errorf("Allow-Credentials = %q; a wildcard origin must never be credentialed", got)
		}
	})

	t.Run("other origins get nothing on an admin path", func(t *testing.T) {
		headers := h.doRaw(http.MethodGet, "/api/admin/jobs", "", map[string]string{"Origin": "https://elsewhere.example"}).headers
		if got := headers.Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("Allow-Origin = %q on an admin path from a foreign origin, want none", got)
		}
	})
}
