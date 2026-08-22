// Package database owns the connection pool and the migration runner.
//
// It knows nothing about jobs or admins: those packages own their own SQL. What
// lives here is the lifecycle — open, probe, close — and the pool tuning that a
// serverless Postgres behind a scale-to-zero container actually needs.
package database

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/job-paglu/homepage/backend/internal/config"
)

// Pool tuning for Neon behind a serverless Go function.
//
// DefaultMaxConns is 2, and the smallness is the entire point. The pool size
// that matters to Postgres is not this number — it is this number multiplied by
// the number of live instances, and on a serverless platform nothing in this
// repository decides that count. A cold start creates a new process, a new
// pool, and a new set of connections; a traffic spike creates several at once.
// A per-instance pool of 8 looks modest and becomes 80 connections against a
// free-tier ceiling the moment ten instances exist, which surfaces as
// `too many connections` under exactly the load you wanted to serve.
//
// Two is chosen over one so a single instance can overlap two queries (a page
// render that reads a job and then its list, say) without serialising them.
// Raise it only for a host with a *known, bounded* instance count, and set
// DB_MAX_CONNS rather than editing this — see config.Config.DBMaxConns.
//
// This is also why production must point DATABASE_URL at Neon's *pooled*
// endpoint: the pooler is what absorbs a fan-out this code cannot see.
//
// MinConns is zero because the process itself is disposable: holding a warm
// connection open from an instance that may be frozen or discarded in a second
// buys nothing and occupies a slot someone else needs.
const (
	DefaultMaxConns   = 2
	minConns          = 0
	maxConnLifetime   = 30 * time.Minute
	maxConnIdleTime   = 5 * time.Minute
	healthCheckPeriod = 1 * time.Minute
	connectTimeout    = 10 * time.Second
)

// Open parses the DSN, builds a pool and confirms it can reach Postgres.
//
// The initial probe is deliberate: a bad DSN or a wrong password should fail the
// deploy, not the first visitor. It is also why connectTimeout is generous —
// a Neon instance resuming from scale-to-zero can take several seconds, and
// treating that as a fatal misconfiguration would make every cold deploy flaky.
func Open(ctx context.Context, cfg *config.Config, logger *slog.Logger) (*pgxpool.Pool, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		// The error text can echo the DSN, which contains the password, so it is
		// not wrapped into the returned message.
		return nil, fmt.Errorf("DATABASE_URL could not be parsed as a Postgres connection string")
	}

	// Zero from config means "unset"; the default lives here so there is exactly
	// one number to change and config does not have to know about pool tuning.
	maxConns := cfg.DBMaxConns
	if maxConns <= 0 {
		maxConns = DefaultMaxConns
	}

	poolConfig.MaxConns = int32(maxConns)
	poolConfig.MinConns = minConns
	poolConfig.MaxConnLifetime = maxConnLifetime
	poolConfig.MaxConnIdleTime = maxConnIdleTime
	poolConfig.HealthCheckPeriod = healthCheckPeriod
	poolConfig.ConnConfig.ConnectTimeout = connectTimeout

	// Server-side *named* prepared statements break behind a connection pooler in
	// transaction mode, which is how Neon's pooled endpoint works: the second
	// query can land on a different backend that has never seen the statement.
	// CacheDescribe caches the statement *description* client-side and sends an
	// unnamed prepared statement, so the same code and the same DSN work against
	// either the direct or the pooled endpoint.
	//
	// This is what lets production run on the `-pooler` host without a second
	// code path, and it is why the only thing that has to be remembered at deploy
	// time is which URL goes where: pooled for the application, direct for
	// migrations (which need session-level state the pooler will not guarantee).
	poolConfig.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheDescribe

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("creating the connection pool: %w", err)
	}

	probeCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := pool.Ping(probeCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connecting to Postgres: %w", err)
	}

	logger.Info("database connected",
		slog.Int("max_conns", maxConns),
		slog.String("host", poolConfig.ConnConfig.Host),
		slog.String("database", poolConfig.ConnConfig.Database),
	)
	return pool, nil
}

// readyTimeout bounds the /ready probe. Shorter than the query timeout: a
// readiness check that takes three seconds has already failed its purpose
// (contract section 3).
const readyTimeout = 2 * time.Second

// Ready reports whether Postgres answers a trivial query.
//
// Used by /ready only. /health must never call this: a liveness probe that
// depends on a downstream service gets the container restarted for someone
// else's outage.
func Ready(ctx context.Context, pool *pgxpool.Pool) error {
	if pool == nil {
		return fmt.Errorf("no connection pool")
	}

	ctx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()

	var one int
	if err := pool.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		return fmt.Errorf("database readiness probe: %w", err)
	}
	return nil
}
