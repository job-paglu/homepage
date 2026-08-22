#!/usr/bin/env bash
# =============================================================================
# Job Paglu - apply database migrations (bash / macOS / Linux / WSL / Git Bash)
# -----------------------------------------------------------------------------
# WHICH MECHANISM THIS USES, AND WHY
#
# It runs THE SERVER'S OWN MIGRATION PATH, not psql.
#
#   The migration runner is not a separate tool. Decision D-7 embeds the .sql
#   files in the binary with `embed.FS` and applies them from
#   internal/database/migrate.go at startup, gated on RUN_MIGRATIONS_ON_START.
#   That runner is also what maintains the `schema_migrations` bookkeeping table.
#
#   So `psql -f backend/migrations/*.sql` would appear to work and would leave
#   the database in a state the server disagrees with: the DDL applied, but
#   `schema_migrations` empty. The next real startup would try to apply
#   migration 001 again and fail on an existing table. Using the same code path
#   the server uses is the only way to keep those two in agreement.
#
#   The mechanics, therefore: build the server binary, start it once with
#   RUN_MIGRATIONS_ON_START=true on a throwaway port, wait until /health
#   answers - migrations run during startup and before the listener opens, so a
#   reply on /health proves they finished - then stop it. The temporary server
#   is not left running; scripts/dev.* tells you to start the real one yourself.
#
#   Manual escape hatch, for when you want to READ the SQL rather than run it:
#       docker compose exec -T db psql -U jobpaglu -d jobpaglu \
#           -f - < backend/migrations/001_init.sql
#   Only do that against a database you are about to throw away, for the
#   bookkeeping reason above.
#
# Usage:
#   ./scripts/migrate.sh                 # uses DATABASE_URL from .env
#   DATABASE_URL=postgres://... ./scripts/migrate.sh
#
# Exit codes: 0 applied (or already current), 1 failed.
# =============================================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
BACKEND_DIR="${REPO_ROOT}/backend"

# Throwaway port, deliberately not 8080, so this cannot collide with a backend
# you already have running in another terminal. Override if 8099 is taken.
MIGRATE_PORT="${MIGRATE_PORT:-8099}"
TIMEOUT_SECONDS="${MIGRATE_TIMEOUT:-90}"

if [ -t 1 ]; then
  C_RESET=$'\033[0m'; C_CYAN=$'\033[36m'; C_GREEN=$'\033[32m'; C_RED=$'\033[31m'
else
  C_RESET=''; C_CYAN=''; C_GREEN=''; C_RED=''
fi
step() { printf '%s==> %s%s\n' "$C_CYAN" "$1" "$C_RESET"; }
ok()   { printf '%s    %s%s\n' "$C_GREEN" "$1" "$C_RESET"; }
fail() {
  printf '\n%sERROR: %s%s\n' "$C_RED" "$1" "$C_RESET" >&2
  [ "${2:-}" != "" ] && printf '\n%s  Fix: %s%s\n' "$C_RED" "$2" "$C_RESET" >&2
  printf '\n' >&2
  exit 1
}

command -v go   >/dev/null 2>&1 || fail 'Go is not on PATH.'   'Install Go 1.24+ from https://go.dev/dl/ and open a new shell.'
command -v curl >/dev/null 2>&1 || fail 'curl is not on PATH.' 'Install curl - it is used to poll the health endpoint.'
[ -d "$BACKEND_DIR" ] || fail "backend/ not found at ${BACKEND_DIR}" 'Run this from inside the repository.'

# ---------------------------------------------------------------------------
# DATABASE_URL: environment wins, then .env. Never echoed.
# ---------------------------------------------------------------------------
if [ -z "${DATABASE_URL:-}" ]; then
  [ -f "${REPO_ROOT}/.env" ] || fail 'DATABASE_URL is not set and .env does not exist.' \
    'Run ./scripts/dev.sh first, or copy .env.example to .env and fill in DATABASE_URL.'

  # Extract exactly one key. `source .env` is avoided on purpose: it executes the
  # file, so a backtick or $(...) inside a password would run as a command.
  DATABASE_URL="$(
    awk -F= '/^[[:space:]]*DATABASE_URL[[:space:]]*=/ {
      sub(/^[^=]*=[[:space:]]*/, ""); gsub(/^["\047]|["\047][[:space:]]*$/, ""); v=$0
    } END { print v }' "${REPO_ROOT}/.env"
  )"
fi

[ -n "${DATABASE_URL:-}" ] || fail 'DATABASE_URL is empty.' \
  'Set it in .env. For the docker-compose database that is postgres://USER:PASSWORD@localhost:5432/DBNAME?sslmode=disable'

case "$DATABASE_URL" in
  *REPLACE_*) fail 'DATABASE_URL still contains a placeholder from .env.example.' \
                   'Edit .env and put your real local values in. Nothing here is a secret worth guarding - it is a throwaway container.' ;;
esac

# Reported without the credentials, so a shared terminal or a pasted log cannot
# leak the password. Everything from '//' up to the last '@' is replaced.
SAFE_URL="$(printf '%s' "$DATABASE_URL" | sed -E 's#(//)[^/@]*@#\1<redacted>@#')"
step "Target: ${SAFE_URL}"

# ---------------------------------------------------------------------------
# Build. Building rather than `go run` is deliberate: `go run` compiles to a
# temporary binary and execs it as a CHILD, so killing the `go` process can
# leave the server alive and holding the port. Building gives us the exact PID.
# ---------------------------------------------------------------------------
step 'Building the server binary'
BIN_DIR="${BACKEND_DIR}/bin"
mkdir -p "$BIN_DIR"
( cd "$BACKEND_DIR" && go build -o "${BIN_DIR}/server" ./cmd/server ) \
  || fail 'go build failed.' 'Fix the compile error above. If it is a missing module, run `cd backend && go mod tidy` and commit go.sum.'
ok 'Built backend/bin/server'

# ---------------------------------------------------------------------------
# Run once with migrations enabled, then stop.
# ---------------------------------------------------------------------------
LOG_FILE="$(mktemp -t jobpaglu-migrate.XXXXXX)"
SERVER_PID=''

cleanup() {
  if [ -n "$SERVER_PID" ] && kill -0 "$SERVER_PID" 2>/dev/null; then
    kill "$SERVER_PID" 2>/dev/null || true
    # Give it a moment to close the pool cleanly, then insist.
    for _ in 1 2 3 4 5 6 7 8 9 10; do
      kill -0 "$SERVER_PID" 2>/dev/null || break
      sleep 0.3
    done
    kill -9 "$SERVER_PID" 2>/dev/null || true
  fi
  rm -f "$LOG_FILE"
}
trap cleanup EXIT INT TERM

step "Starting the server once with RUN_MIGRATIONS_ON_START=true (port ${MIGRATE_PORT})"

# ADMIN_SESSION_SECRET: the server validates it at startup, before it would ever
# reach the migration step, so a value has to be present even for a
# migrate-and-exit run. This one is generated fresh, used for a few seconds by a
# process that serves no request, and discarded - it never touches .env, is
# never printed, and no session is created with it.
#
# HEX, not base64, and that is not arbitrary: internal/config/config.go rejects
# a secret containing a template marker such as "todo" or "sample", and it
# lowercases before comparing. A random base64 string can contain "todo" by
# chance roughly once in twenty thousand runs, which would surface as a baffling
# "still looks like the .env.example placeholder" failure. The hex alphabet
# [0-9a-f] cannot spell any of those markers, so the flake is impossible rather
# than merely unlikely. 32 bytes -> 64 characters, above the 32-char minimum.
EPHEMERAL_SECRET="$(
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 32
  else
    od -An -tx1 -N32 /dev/urandom | tr -d ' \n'
  fi
)"

env \
  DATABASE_URL="$DATABASE_URL" \
  RUN_MIGRATIONS_ON_START=true \
  PORT="$MIGRATE_PORT" \
  APP_ENV="${APP_ENV:-development}" \
  LOG_LEVEL="${LOG_LEVEL:-info}" \
  ADMIN_SESSION_SECRET="${ADMIN_SESSION_SECRET:-$EPHEMERAL_SECRET}" \
  FRONTEND_URL="${FRONTEND_URL:-http://localhost:4321}" \
  "${BIN_DIR}/server" >"$LOG_FILE" 2>&1 &
SERVER_PID=$!

step 'Waiting for /health (a reply proves startup, and therefore migration, completed)'
deadline=$(( $(date +%s) + TIMEOUT_SECONDS ))
healthy=0
while [ "$(date +%s)" -lt "$deadline" ]; do
  if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    printf '\n%s--- server output ---%s\n' "$C_RED" "$C_RESET" >&2
    cat "$LOG_FILE" >&2
    printf '%s---------------------%s\n' "$C_RED" "$C_RESET" >&2
    fail 'The server exited before becoming healthy. Migrations did NOT complete.' \
         'Read the output above. A connection refused means Postgres is not up - run ./scripts/dev.sh. A syntax error means a migration file is broken.'
  fi
  if curl -fsS -o /dev/null "http://127.0.0.1:${MIGRATE_PORT}/health" 2>/dev/null; then
    healthy=1
    break
  fi
  sleep 0.5
done

if [ "$healthy" -ne 1 ]; then
  printf '\n%s--- server output ---%s\n' "$C_RED" "$C_RESET" >&2
  cat "$LOG_FILE" >&2
  printf '%s---------------------%s\n' "$C_RED" "$C_RESET" >&2
  fail "The server did not answer /health within ${TIMEOUT_SECONDS}s." \
       "If port ${MIGRATE_PORT} is in use, re-run with MIGRATE_PORT=<free port>. Otherwise read the output above."
fi

# Startup log lines from the migration runner are worth seeing: they say which
# versions were applied, or that there was nothing to do.
if [ -s "$LOG_FILE" ]; then
  printf '\n--- server startup log ---\n'
  cat "$LOG_FILE"
  printf -- '--------------------------\n\n'
fi

ok 'Migrations applied. Schema is current.'
step 'Stopping the temporary server'
# cleanup() runs on EXIT and does the stopping, so nothing more is needed here.
exit 0
