// Command server is the Job Paglu API.
//
// It is the only binary that serves traffic. Everything it needs comes from the
// environment (internal/config), and it fails at startup rather than at first
// request if anything is missing.
//
// DEPLOYMENT CONTRACT — this file's path is load-bearing
//
//	Production is Vercel's Go runtime, as the project `job-paglu-api` with its
//	Root Directory set to `backend` (docs/DECISIONS.md D-20). That runtime
//	detects a Go project from a `go.mod` at the project root — which `backend`
//	being the Root Directory is what makes true — and one of three entrypoints:
//	`main.go`, `cmd/api/main.go`, or `cmd/server/main.go`. This is the third.
//	Moving or renaming it silently changes the deployment from "a Go server" to
//	"no framework detected".
//
//	The other half of the contract is PORT: the platform injects it and expects
//	this process to listen on it. config.Load reads it, Addr() binds it, and the
//	fallback of 8080 exists only for `go run` and `docker run`. Binding a fixed
//	port, or binding 127.0.0.1 instead of all interfaces, makes the service
//	unreachable in a way whose error message mentions neither.
//
//	`"framework": "go"` in backend/vercel.json is required for the runtime to
//	run a server at all; the Go version comes from the `go` directive in go.mod.
//
// WHAT A DISPOSABLE PROCESS MEANS FOR THE CODE BELOW
//
//	This binary is written as a long-lived server and still is one — the graceful
//	shutdown, the session sweeper and the pgx pool are all correct for a
//	container. On a serverless host the same code runs, but the process is
//	created and discarded outside its own control, and three consequences follow.
//	They are documented rather than coded around, because every alternative
//	trades a real cost for them:
//
//	 1. The pgx pool is per-instance, so the count Postgres sees is
//	    DB_MAX_CONNS times the live instance count. Hence the small default and
//	    Neon's pooled endpoint. See internal/database.Open.
//	 2. The login rate limiter is in-process, so the effective limit is the
//	    configured limit times the instance count. The honest fix is a shared
//	    store; see docs/SECURITY.md section 6.
//	 3. Background tickers — the session sweeper here, and the auth janitors —
//	    only tick while an instance is alive and unfrozen. They are housekeeping,
//	    not correctness: every expiry is enforced on read. See
//	    startSessionSweeper.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/job-paglu/homepage/backend/internal/auth"
	"github.com/job-paglu/homepage/backend/internal/config"
	"github.com/job-paglu/homepage/backend/internal/database"
	"github.com/job-paglu/homepage/backend/internal/jobs"
	"github.com/job-paglu/homepage/backend/internal/server"
	"github.com/job-paglu/homepage/backend/internal/validation"
	"github.com/job-paglu/homepage/backend/migrations"
)

// Timeouts on the http.Server.
//
// ReadHeaderTimeout is the one that matters for safety: without it a slowloris
// client holds a connection open indefinitely for free. WriteTimeout is generous
// because a cold Neon instance can make a first request slow, and shutdownGrace
// is 15s because a container host sends SIGTERM and then waits before killing.
//
// These are all deliberately ABOVE the platform's own function-duration limit
// (10s in the documented Vercel deployment). They are a backstop against a stuck
// handler on a long-lived host, not the request budget: the numbers that actually
// bound a request are the platform limit and the three client-side budgets in
// docs/DEPLOYMENT.md. Tightening these to match the platform would only move the
// same failure from the platform's clock to this one, with a worse error.
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 120 * time.Second
	shutdownGrace     = 15 * time.Second
)

func main() {
	// All real work is in run() so every exit path can return an error and
	// os.Exit lives in exactly one place. log.Fatal deeper in the tree would skip
	// the deferred pool close.
	if err := run(); err != nil {
		// The logger may not exist yet, so this goes to stderr directly.
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := newLogger(cfg)
	slog.SetDefault(logger) // httpx logs through the default logger

	logger.Info("starting job paglu api", slog.String("config", cfg.Redacted()))

	// Cancelled on SIGINT or SIGTERM. Everything with a background goroutine
	// takes this context so shutdown is one signal, not a set of stop methods.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := database.Open(ctx, cfg, logger)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer pool.Close()

	if cfg.RunMigrationsOnStart {
		if err := database.Migrate(ctx, pool, migrations.FS, logger); err != nil {
			return fmt.Errorf("migrations: %w", err)
		}
	} else {
		logger.Warn("RUN_MIGRATIONS_ON_START is false; assuming the schema is already current",
			slog.String("apply_with", "adminctl migrate"))
	}

	applyPolicy := validation.ApplyURLPolicy{
		AllowHTTP: cfg.AllowHTTPApplyURLs,
		// localhost apply links are a development affordance only.
		AllowLocalhost: cfg.IsDevelopment(),
	}

	jobService := jobs.NewService(jobs.NewRepository(pool), applyPolicy)
	authStore := auth.NewStore(pool)
	authHandler := auth.NewHandler(authStore, cfg, logger)

	authHandler.StartJanitors(ctx)
	startSessionSweeper(ctx, authStore, logger)

	handler := server.New(server.Deps{
		Config:     cfg,
		Logger:     logger,
		Pool:       pool,
		PublicJobs: jobs.NewPublicHandler(jobService),
		AdminJobs:  jobs.NewAdminHandler(jobService),
		Auth:       authHandler,
	})

	httpServer := &http.Server{
		Addr:              cfg.Addr(),
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
		// Route the server's own errors — a bad TLS handshake, a malformed
		// request line — into the structured log rather than the standard logger.
		ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("listening", slog.String("addr", httpServer.Addr))
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("http server: %w", err)
		}
		return nil

	case <-ctx.Done():
		// A container host sends SIGTERM and gives the process time to finish what
		// it is doing. Shutdown stops accepting, drains in-flight requests, and
		// returns; the grace period bounds how long a stuck handler can hold up
		// the deploy.
		logger.Info("shutdown signal received, draining", slog.Duration("grace", shutdownGrace))

		drainCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()

		if err := httpServer.Shutdown(drainCtx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		logger.Info("shutdown complete")
		return nil
	}
}

// newLogger builds the structured logger.
//
// JSON in production because every hosted log viewer — Vercel's included —
// parses it into queryable fields; text in development because a human is
// reading it. Nothing anywhere logs a credential, a token or a cookie (rule R2).
func newLogger(cfg *config.Config) *slog.Logger {
	options := &slog.HandlerOptions{Level: cfg.LogLevel}

	if cfg.IsDevelopment() {
		return slog.New(slog.NewTextHandler(os.Stdout, options))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, options))
}

// sessionSweepInterval is how often expired sessions are collected. Hourly is
// ample: LookupSession already refuses an expired row, so this only reclaims
// disk.
const sessionSweepInterval = time.Hour

// startSessionSweeper deletes expired sessions in the background.
//
// A failure is logged and ignored. This is housekeeping — if it never runs, the
// only consequence is a growing table, not a security hole — so it must never be
// able to take the process down.
//
// On a serverless host it will frequently never run: an instance that handles a
// request and is discarded a second later never reaches the first tick. That is
// tolerable precisely because expiry is enforced on every read in
// LookupSession, so the table growing is a storage question and not a security
// one. If the `sessions` table ever needs guaranteed collection, the answer is a
// scheduled job (a cron trigger calling a route, or `pg_cron`), not a longer
// ticker in a process that may not exist.
func startSessionSweeper(ctx context.Context, store *auth.Store, logger *slog.Logger) {
	go func() {
		ticker := time.NewTicker(sessionSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				removed, err := store.DeleteExpiredSessions(ctx)
				if err != nil {
					logger.Warn("session sweep failed", slog.String("error", err.Error()))
					continue
				}
				if removed > 0 {
					logger.Info("expired sessions removed", slog.Int64("count", removed))
				}
			}
		}
	}()
}
