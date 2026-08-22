package database

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// advisoryLockKey is an arbitrary but fixed number identifying this migration
// runner. Any process holding it is migrating; anyone else waits.
//
// The value is the ASCII of "jobpagl1", which makes an accidental collision with
// another application's lock in a shared database vanishingly unlikely.
const advisoryLockKey int64 = 0x6A6F62_7061676C

// migrationLockTimeoutSeconds bounds the wait for the advisory lock. If another
// instance has been migrating for longer than this something is wrong, and
// failing fast is better than a container that never becomes ready.
const migrationLockTimeoutSeconds = 60

// Migrate applies every pending migration from fsys, in lexical order.
//
// Two instances starting together will both call this. The advisory lock is what
// makes that safe: the second one blocks, then finds every version already
// recorded and does nothing. Without it both would run 001 and one would fail on
// a duplicate object, crash-looping a perfectly good deploy.
//
// Safe is not the same as free. On a serverless host the number of instances is
// not something you set, so "safe under concurrency" becomes "every cold start
// pays for a lock round-trip before answering its first request". That is why
// production sets RUN_MIGRATIONS_ON_START=false and calls this once, on purpose,
// through `adminctl migrate` — see docs/DEPLOYMENT.md.
//
// Each migration runs in its own transaction, so a failure half way through a
// file leaves that file entirely unapplied rather than partially applied.
func Migrate(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS, logger *slog.Logger) error {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquiring a connection for migrations: %w", err)
	}
	defer conn.Release()

	// The lock must be taken and released on one connection, which is why this
	// works on a dedicated *pgxpool.Conn rather than on the pool.
	//
	// SET does not accept a bind parameter, so the timeout goes through
	// set_config, which does. Still parameterised, still no value in the SQL.
	if _, err := conn.Exec(ctx, "SELECT set_config('lock_timeout', $1, false)",
		strconv.Itoa(migrationLockTimeoutSeconds*1000)); err != nil {
		return fmt.Errorf("setting the migration lock timeout: %w", err)
	}
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", advisoryLockKey); err != nil {
		return fmt.Errorf("acquiring the migration advisory lock: %w", err)
	}
	defer func() {
		// Released on a context that is not the caller's: if ctx is already
		// cancelled the unlock still has to be sent, or the lock survives until
		// the connection is reaped.
		if _, err := conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", advisoryLockKey); err != nil {
			logger.Warn("could not release the migration advisory lock", slog.String("error", err.Error()))
		}
	}()

	// 001 creates this table, but the applied-version query needs it to exist
	// first. Creating it here — idempotently, outside the numbered files — breaks
	// that circularity.
	const bootstrap = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    int PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`
	if _, err := conn.Exec(ctx, bootstrap); err != nil {
		return fmt.Errorf("creating schema_migrations: %w", err)
	}

	applied := map[int]bool{}
	rows, err := conn.Query(ctx, "SELECT version FROM schema_migrations")
	if err != nil {
		return fmt.Errorf("reading applied migrations: %w", err)
	}
	for rows.Next() {
		var version int
		if err := rows.Scan(&version); err != nil {
			rows.Close()
			return fmt.Errorf("scanning an applied migration version: %w", err)
		}
		applied[version] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("reading applied migrations: %w", err)
	}

	files, err := migrationFiles(fsys)
	if err != nil {
		return err
	}

	for _, file := range files {
		if applied[file.version] {
			continue
		}

		body, err := fs.ReadFile(fsys, file.name)
		if err != nil {
			return fmt.Errorf("reading migration %s: %w", file.name, err)
		}

		tx, err := conn.Begin(ctx)
		if err != nil {
			return fmt.Errorf("beginning migration %s: %w", file.name, err)
		}
		if _, err := tx.Exec(ctx, string(body)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("applying migration %s: %w", file.name, err)
		}
		if _, err := tx.Exec(ctx, "INSERT INTO schema_migrations (version) VALUES ($1)", file.version); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("recording migration %s: %w", file.name, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("committing migration %s: %w", file.name, err)
		}

		logger.Info("migration applied", slog.Int("version", file.version), slog.String("file", file.name))
	}

	return nil
}

// Pending reports the migration versions present in fsys that are not recorded
// in schema_migrations, in ascending order.
//
// It takes no lock, opens no transaction and applies nothing, which is what
// makes it safe to run against production before deciding to migrate — and safe
// to run from CI as a check. It is read-only in the strict sense: it does not
// even create schema_migrations, so on a virgin database it reports every
// migration as pending rather than leaving a table behind.
//
// A concurrent Migrate can change the answer the moment this returns. That is
// accepted: this is a report for a human, not a guard for the runner. Migrate's
// advisory lock is the guard.
func Pending(ctx context.Context, pool *pgxpool.Pool, fsys fs.FS) ([]int, error) {
	files, err := migrationFiles(fsys)
	if err != nil {
		return nil, err
	}

	applied := map[int]bool{}

	// to_regclass returns NULL rather than erroring for a missing relation, so
	// this distinguishes "no migrations table yet" from a real query failure
	// without a catch-the-error-string check.
	var exists bool
	if err := pool.QueryRow(ctx,
		"SELECT to_regclass('schema_migrations') IS NOT NULL").Scan(&exists); err != nil {
		return nil, fmt.Errorf("checking for schema_migrations: %w", err)
	}

	if exists {
		rows, err := pool.Query(ctx, "SELECT version FROM schema_migrations")
		if err != nil {
			return nil, fmt.Errorf("reading applied migrations: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var version int
			if err := rows.Scan(&version); err != nil {
				return nil, fmt.Errorf("scanning an applied migration version: %w", err)
			}
			applied[version] = true
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("reading applied migrations: %w", err)
		}
	}

	var pending []int
	for _, file := range files {
		if !applied[file.version] {
			pending = append(pending, file.version)
		}
	}
	return pending, nil
}

type migration struct {
	version int
	name    string
}

// migrationFiles lists the .sql files in fsys, ordered by their numeric prefix.
//
// Sorting on the parsed number rather than the string matters at exactly one
// point: the day a tenth migration exists, "010" sorts before "002"
// lexically and the schema is built in the wrong order.
func migrationFiles(fsys fs.FS) ([]migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("listing migrations: %w", err)
	}

	var files []migration
	seen := map[int]string{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}

		version, err := parseVersion(name)
		if err != nil {
			return nil, err
		}
		if previous, duplicate := seen[version]; duplicate {
			return nil, fmt.Errorf("migrations %s and %s share version %d", previous, name, version)
		}
		seen[version] = name
		files = append(files, migration{version: version, name: name})
	}

	sort.Slice(files, func(i, j int) bool { return files[i].version < files[j].version })
	return files, nil
}

// parseVersion reads the leading digits of a filename like 002_indexes.sql.
func parseVersion(name string) (int, error) {
	digits := 0
	for digits < len(name) && name[digits] >= '0' && name[digits] <= '9' {
		digits++
	}
	if digits == 0 {
		return 0, fmt.Errorf("migration %q does not start with a version number", name)
	}
	version, err := strconv.Atoi(name[:digits])
	if err != nil {
		return 0, fmt.Errorf("migration %q has an unreadable version: %w", name, err)
	}
	if version < 1 {
		return 0, fmt.Errorf("migration %q has version %d; versions start at 1", name, version)
	}
	return version, nil
}
