// Command adminctl creates and repairs administrator accounts.
//
// It has no HTTP surface on purpose. Account creation is the one operation that
// cannot be gated by an existing account, so exposing it as an endpoint would
// mean either an unauthenticated route that creates administrators or a
// bootstrap token that is itself a credential to leak. A CLI run by whoever
// already has the database URL avoids both.
//
//	adminctl create-admin  -email admin@example.com -name "Ada"
//	adminctl reset-password -email admin@example.com
//	adminctl migrate
//
// `migrate` lives here rather than in cmd/server because on a serverless host
// startup migrations are the wrong shape: every cold instance would contend for
// the advisory lock on the request-serving critical path, and a bad migration
// would surface as a platform deploy failure rather than as a migration
// failure. Production therefore runs with RUN_MIGRATIONS_ON_START=false and
// applies the schema as a deliberate, observable action — see
// docs/DEPLOYMENT.md and .github/workflows/migrate.yml.
//
// The password is never a flag. Flags land in shell history, in `ps` output for
// every user on the machine, and in CI logs. It comes from
// ADMIN_BOOTSTRAP_PASSWORD or from an interactive prompt with the echo turned
// off.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/job-paglu/homepage/backend/internal/auth"
	"github.com/job-paglu/homepage/backend/internal/config"
	"github.com/job-paglu/homepage/backend/internal/database"
	"github.com/job-paglu/homepage/backend/internal/validation"
	"github.com/job-paglu/homepage/backend/migrations"
)

const bootstrapPasswordEnv = "ADMIN_BOOTSTRAP_PASSWORD"

// commandTimeout bounds the whole operation. Argon2id at 64 MiB is the slow part
// and takes well under a second.
const commandTimeout = 30 * time.Second

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "adminctl: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("no subcommand given")
	}

	switch args[0] {
	case "create-admin":
		return createAdmin(args[1:])
	case "reset-password":
		return resetPassword(args[1:])
	case "migrate":
		return migrate(args[1:])
	case "-h", "--help", "help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `adminctl — Job Paglu operations

  adminctl create-admin  -email <address> -name <display name>
  adminctl reset-password -email <address>
  adminctl migrate       [-dry-run]

The password is read from `+bootstrapPasswordEnv+` if set, otherwise prompted for
without echo. It is never accepted as a command-line flag.

create-admin and reset-password require DATABASE_URL and the other server
variables in the environment. `+"`migrate`"+` requires DATABASE_URL only, and it must
be the DIRECT (non-pooled) connection string.
`)
}

func createAdmin(args []string) error {
	fs := flag.NewFlagSet("create-admin", flag.ContinueOnError)
	email := fs.String("email", "", "administrator email address")
	name := fs.String("name", "", "display name")
	if err := fs.Parse(args); err != nil {
		return err
	}

	normalisedEmail, err := requireEmail(*email)
	if err != nil {
		return err
	}
	displayName := validation.CleanSingleLine(*name)
	if displayName == "" {
		return errors.New("-name is required")
	}
	if validation.Length(displayName) > 120 {
		return errors.New("-name must be at most 120 characters")
	}

	password, err := readPassword("Password for "+normalisedEmail+": ", true)
	if err != nil {
		return err
	}

	hash, err := auth.Hash(password)
	if err != nil {
		return fmt.Errorf("hashing the password: %w", err)
	}

	return withStore(func(ctx context.Context, store *auth.Store) error {
		created, err := store.UpsertAdmin(ctx, normalisedEmail, displayName, hash)
		if err != nil {
			return fmt.Errorf("writing the administrator: %w", err)
		}
		if created {
			fmt.Printf("created administrator %s\n", normalisedEmail)
		} else {
			// Upsert rather than fail: the common reason to re-run this is a
			// forgotten password on a fresh deployment, and a "already exists"
			// error there just sends the operator to reset-password.
			fmt.Printf("updated existing administrator %s (name and password replaced)\n", normalisedEmail)
		}
		return nil
	})
}

func resetPassword(args []string) error {
	fs := flag.NewFlagSet("reset-password", flag.ContinueOnError)
	email := fs.String("email", "", "administrator email address")
	if err := fs.Parse(args); err != nil {
		return err
	}

	normalisedEmail, err := requireEmail(*email)
	if err != nil {
		return err
	}

	password, err := readPassword("New password for "+normalisedEmail+": ", true)
	if err != nil {
		return err
	}

	hash, err := auth.Hash(password)
	if err != nil {
		return fmt.Errorf("hashing the password: %w", err)
	}

	return withStore(func(ctx context.Context, store *auth.Store) error {
		if err := store.SetPassword(ctx, normalisedEmail, hash); err != nil {
			if errors.Is(err, auth.ErrNoAdmin) {
				return fmt.Errorf("no administrator with the address %s", normalisedEmail)
			}
			return fmt.Errorf("setting the password: %w", err)
		}
		fmt.Printf("password reset for %s\n", normalisedEmail)
		// Existing sessions are deliberately left alone. Revoking them is a
		// separate decision — a password reset by the account owner should not
		// log them out of the tab they are working in — and there is no evidence
		// of compromise here to justify it.
		return nil
	})
}

// migrateTimeout bounds the whole migration run. Generously longer than
// commandTimeout: an index build on a real table is the one operation here that
// can legitimately take minutes, and killing it half way is worse than waiting.
const migrateTimeout = 10 * time.Minute

// migrate applies pending migrations as a deliberate, one-shot action.
//
// It exists so that RUN_MIGRATIONS_ON_START can be false in production. On a
// serverless host every cold instance would otherwise run this check before
// serving its first request: correct, because of the advisory lock, but paid for
// on the critical path and impossible to observe when it fails.
//
// -dry-run reports what would be applied and touches nothing, which is what
// makes this safe to run from CI against production before the real run.
func migrate(args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "report pending migrations without applying them")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Deliberately NOT config.Load(). A migration needs one thing — the database
	// — and Load also demands ADMIN_SESSION_SECRET and FRONTEND_URL. Requiring
	// them here would push an operator into inventing values for a job that
	// never serves a request, and an invented session secret has a way of
	// becoming a real one.
	databaseURL := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if databaseURL == "" {
		return errors.New("DATABASE_URL is required, and for migrations it must be the " +
			"DIRECT (non-pooled) connection string: DDL in a transaction and the advisory " +
			"lock both need every statement on the same session, which a transaction-mode " +
			"pooler does not guarantee")
	}

	cfg := &config.Config{
		DatabaseURL: databaseURL,
		// One connection. This process does exactly one thing in sequence, and
		// the runner takes its lock on a single connection anyway.
		DBMaxConns: 1,
	}

	// Migrations are an operation whose log IS the output, so unlike the other
	// subcommands this one keeps the runner's info-level lines.
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	ctx, cancel := context.WithTimeout(context.Background(), migrateTimeout)
	defer cancel()

	pool, err := database.Open(ctx, cfg, logger)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer pool.Close()

	pending, err := database.Pending(ctx, pool, migrations.FS)
	if err != nil {
		return err
	}

	if len(pending) == 0 {
		fmt.Println("schema is up to date; nothing to apply")
		return nil
	}

	if *dryRun {
		fmt.Printf("%d migration(s) pending: %v\n", len(pending), pending)
		fmt.Println("dry run — nothing was applied")
		return nil
	}

	fmt.Printf("applying %d migration(s): %v\n", len(pending), pending)
	if err := database.Migrate(ctx, pool, migrations.FS, logger); err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	fmt.Println("migrations applied")
	return nil
}

func requireEmail(raw string) (string, error) {
	fieldErrors := validation.New()
	normalised := validation.Email(fieldErrors, "email", raw)
	if fieldErrors.Any() {
		return "", fmt.Errorf("-email: %s", fieldErrors["email"])
	}
	return auth.TrimEmail(normalised), nil
}

// readPassword obtains a password without ever putting it on a command line.
//
// ADMIN_BOOTSTRAP_PASSWORD is checked first so the command is usable from a
// deploy script; otherwise the terminal is prompted with echo disabled. If echo
// cannot be disabled the command fails rather than falling back to a visible
// prompt: a password typed onto a shared screen is worse than an error message.
func readPassword(prompt string, confirm bool) (string, error) {
	if fromEnv := os.Getenv(bootstrapPasswordEnv); fromEnv != "" {
		if err := checkPasswordStrength(fromEnv); err != nil {
			return "", fmt.Errorf("%s: %w", bootstrapPasswordEnv, err)
		}
		return fromEnv, nil
	}

	password, err := promptNoEcho(prompt)
	if err != nil {
		return "", err
	}
	if err := checkPasswordStrength(password); err != nil {
		return "", err
	}

	if confirm {
		again, err := promptNoEcho("Confirm password: ")
		if err != nil {
			return "", err
		}
		if again != password {
			return "", errors.New("the two passwords do not match")
		}
	}
	return password, nil
}

// checkPasswordStrength enforces the minimum length and nothing else.
//
// No composition rules: they push people towards "Password1!" and are not what
// makes a password hard to guess. Length is, and Argon2id handles the rest.
func checkPasswordStrength(password string) error {
	if validation.Length(password) < auth.MinPasswordLen {
		return fmt.Errorf("the password must be at least %d characters", auth.MinPasswordLen)
	}
	if strings.TrimSpace(password) == "" {
		return errors.New("the password cannot be only whitespace")
	}
	return nil
}

// withStore opens a pool, runs fn, and closes up.
//
// Migrations are deliberately not run here: this command may well be pointed at
// a database an older server version is serving, and quietly upgrading its
// schema from a CLI would be a surprising side effect. `adminctl migrate` is the
// explicit way to do it.
func withStore(fn func(ctx context.Context, store *auth.Store) error) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Discard the logs: the operator wants the command's own output, not the
	// pool's. Errors still surface as returned values.
	quiet := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	ctx, cancel := context.WithTimeout(context.Background(), commandTimeout)
	defer cancel()

	pool, err := database.Open(ctx, cfg, quiet)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer pool.Close()

	return fn(ctx, auth.NewStore(pool))
}
