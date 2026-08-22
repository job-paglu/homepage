#!/usr/bin/env bash
# =============================================================================
# Job Paglu - local development bring-up (bash / macOS / Linux / WSL / Git Bash)
# -----------------------------------------------------------------------------
# What this script DOES:
#   1. Checks prerequisites and names what to install if one is missing.
#   2. Creates .env from .env.example on first run (never overwrites).
#   3. Starts only the `db` service from docker-compose.yml.
#   4. Waits for Postgres's own healthcheck - not a fixed sleep.
#   5. Applies migrations by delegating to scripts/migrate.sh.
#   6. Optionally loads scripts/seed-dev.sql (--seed), refusing unless
#      APP_ENV=development.
#   7. Prints the two long-running commands for you to run yourself.
#
# What it deliberately does NOT do: orchestrate three long-running processes. A
# wrapper that owns the backend and the Astro dev server has to reinvent log
# multiplexing, Ctrl-C handling and per-process restart, and the first thing it
# costs you is a readable stack trace. Two terminals is the feature.
#
# Behavioural twin of scripts/dev.ps1. See scripts/README.md for the
# differences that could not be avoided.
#
# Usage:
#   ./scripts/dev.sh [--seed] [--skip-migrate] [--recreate]
# =============================================================================
set -euo pipefail

# Repo root is the parent of scripts/, resolved from this file's location, so the
# script works from any working directory (rule R11: no absolute paths).
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

SEED=0
SKIP_MIGRATE=0
RECREATE=0

while [ $# -gt 0 ]; do
  case "$1" in
    --seed)          SEED=1 ;;
    --skip-migrate)  SKIP_MIGRATE=1 ;;
    --recreate)      RECREATE=1 ;;
    -h|--help)
      sed -n '2,26p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
      exit 0 ;;
    *)
      echo "Unknown option: $1" >&2
      echo "Usage: ./scripts/dev.sh [--seed] [--skip-migrate] [--recreate]" >&2
      exit 2 ;;
  esac
  shift
done

# Colour only when stdout is a terminal, so piping to a file or a CI log stays
# readable.
if [ -t 1 ]; then
  C_RESET=$'\033[0m'; C_CYAN=$'\033[36m'; C_GREEN=$'\033[32m'
  C_YELLOW=$'\033[33m'; C_RED=$'\033[31m'; C_DIM=$'\033[2m'
else
  C_RESET=''; C_CYAN=''; C_GREEN=''; C_YELLOW=''; C_RED=''; C_DIM=''
fi

step()  { printf '%s==> %s%s\n' "$C_CYAN"   "$1" "$C_RESET"; }
ok()    { printf '%s    %s%s\n' "$C_GREEN"  "$1" "$C_RESET"; }
warn()  { printf '%s    %s%s\n' "$C_YELLOW" "$1" "$C_RESET"; }

fail() {
  printf '\n%sERROR: %s%s\n' "$C_RED" "$1" "$C_RESET" >&2
  if [ "${2:-}" != "" ]; then
    printf '\n%s  Fix: %s%s\n' "$C_YELLOW" "$2" "$C_RESET" >&2
  fi
  printf '\n' >&2
  exit 1
}

# ---------------------------------------------------------------------------
# 1. Prerequisites - fail fast, and say what to install.
# ---------------------------------------------------------------------------
step 'Checking prerequisites'

missing=0
need() {
  # $1 = command, $2 = how to get it
  if ! command -v "$1" >/dev/null 2>&1; then
    printf '%s  missing: %-8s -> %s%s\n' "$C_YELLOW" "$1" "$2" "$C_RESET" >&2
    missing=1
  fi
}

need docker 'Docker Engine or Docker Desktop: https://docs.docker.com/get-docker/'
need go     'Go 1.24 or newer: https://go.dev/dl/'
need node   'Node.js 20.11 LTS or newer: https://nodejs.org/ (or use nvm)'
need npm    'ships with Node.js; if node exists but npm does not, reinstall Node'
need curl   'curl - used to poll the health endpoint during migration'

if [ "$missing" -ne 0 ]; then
  fail 'One or more required tools are not on PATH (listed above).' \
       'Install them, then open a new shell so PATH is re-read, and run this script again.'
fi

# `docker compose` (v2 plugin), not `docker-compose` (v1, end of life). Probed
# rather than assumed, because otherwise the failure surfaces much later as an
# unhelpful "unknown command".
if ! docker compose version >/dev/null 2>&1; then
  fail 'Docker is installed but `docker compose` is not available.' \
       'Install the Compose v2 plugin (bundled with Docker Desktop 4.x, or the docker-compose-plugin package). The retired docker-compose v1 binary is not supported by this repo.'
fi

# The daemon, not just the CLI.
if ! docker info >/dev/null 2>&1; then
  fail 'The Docker CLI works but the Docker daemon is not reachable.' \
       'Start Docker (Docker Desktop, or `sudo systemctl start docker`) and try again. On Linux, also check you are in the `docker` group.'
fi

ok "docker  $(docker --version | sed 's/Docker version //')"
ok "go      $(go version | awk '{print $3}')"
ok "node    $(node --version)"

# ---------------------------------------------------------------------------
# 2. .env
# ---------------------------------------------------------------------------
step 'Checking .env'

if [ ! -f "${REPO_ROOT}/.env" ]; then
  cp "${REPO_ROOT}/.env.example" "${REPO_ROOT}/.env"
  warn 'Created .env from .env.example.'
  warn 'It contains PLACEHOLDERS. You must fill in at least:'
  warn '  ADMIN_SESSION_SECRET  (see the generator command in .env.example)'
  warn '  POSTGRES_PASSWORD     (any local-only value)'
  warn '  DATABASE_URL          (must match the POSTGRES_* values)'
else
  ok '.env exists (not modified).'
fi

if [ ! -f "${REPO_ROOT}/frontend/.env" ]; then
  cp "${REPO_ROOT}/frontend/.env.example" "${REPO_ROOT}/frontend/.env"
  warn 'Created frontend/.env from frontend/.env.example (no secrets in it).'
else
  ok 'frontend/.env exists (not modified).'
fi

# Read ONE named key out of .env. The file is never echoed, so a credential on
# an adjacent line cannot reach the terminal. Last occurrence wins, matching how
# dotenv loaders behave. Deliberately not `source .env`: that executes the file,
# so a stray backtick or $(...) in a password would run as a command.
env_value() {
  # $1 = key, $2 = value to use when the key is absent or empty
  local key="$1" default="$2" value='' line k v
  while IFS= read -r line || [ -n "$line" ]; do
    line="${line#"${line%%[![:space:]]*}"}"          # trim leading space
    case "$line" in '#'*|'') continue ;; esac        # skip comments and blanks
    case "$line" in *=*) ;; *) continue ;; esac      # skip lines with no '='
    k="${line%%=*}"
    k="${k%"${k##*[![:space:]]}"}"                   # trim trailing space in key
    [ "$k" = "$key" ] || continue
    v="${line#*=}"
    v="${v#"${v%%[![:space:]]*}"}"                   # trim leading space in value
    v="${v%"${v##*[![:space:]]}"}"                   # trim trailing space in value
    case "$v" in
      '"'*'"') v="${v#\"}"; v="${v%\"}" ;;
      "'"*"'") v="${v#\'}"; v="${v%\'}" ;;
    esac
    [ -n "$v" ] && value="$v"                        # last non-empty wins
  done < "${REPO_ROOT}/.env"
  if [ -z "$value" ]; then printf '%s' "$default"; else printf '%s' "$value"; fi
}

APP_ENV_VALUE="$(env_value APP_ENV production)"
# These two must match the defaults in docker-compose.yml, because that is what
# actually created the role and the database inside the container.
PG_USER="$(env_value POSTGRES_USER jobpaglu)"
PG_DB="$(env_value POSTGRES_DB jobpaglu)"

ok "APP_ENV = ${APP_ENV_VALUE}"

# ---------------------------------------------------------------------------
# 3. Database
# ---------------------------------------------------------------------------
cd "$REPO_ROOT"

if [ "$RECREATE" -eq 1 ]; then
  step 'Destroying the existing database volume (--recreate)'
  printf 'This deletes all local database data. Type "yes" to continue: '
  read -r answer
  [ "$answer" = 'yes' ] || fail 'Aborted by user.' ''
  docker compose down -v
fi

step 'Starting Postgres (docker compose up -d db)'
docker compose up -d db \
  || fail 'docker compose failed to start the db service.' 'Read the output above; `docker compose logs db` has more.'

step 'Waiting for the Postgres healthcheck'
# Poll the container's own healthcheck rather than sleeping. A fixed sleep is
# either too short (initdb has not finished, the backend dies on connect) or
# wastefully long, and it is wrong on a different machine either way.
CONTAINER_ID="$(docker compose ps -q db | head -n1)"
[ -n "$CONTAINER_ID" ] || fail 'Could not find the db container after starting it.' 'Run `docker compose ps` and `docker compose logs db`.'

deadline=$(( $(date +%s) + 120 ))
status=''
while [ "$(date +%s)" -lt "$deadline" ]; do
  status="$(docker inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$CONTAINER_ID" 2>/dev/null || echo unknown)"
  [ "$status" = 'healthy' ] && break
  if [ "$status" = 'none' ]; then
    fail 'The db container has no healthcheck.' 'docker-compose.yml defines one; you may be running a modified or stale compose file.'
  fi
  sleep 1.5
done

[ "$status" = 'healthy' ] || fail \
  "Postgres did not become healthy within 120s (last status: '${status}')." \
  'Run `docker compose logs db`. A port clash on 5432 is the usual cause - change POSTGRES_PORT in .env.'
ok 'Postgres is healthy.'

# ---------------------------------------------------------------------------
# 4. Migrations
# ---------------------------------------------------------------------------
if [ "$SKIP_MIGRATE" -eq 1 ]; then
  step 'Skipping migrations (--skip-migrate)'
else
  step 'Applying migrations'
  "${SCRIPT_DIR}/migrate.sh" || fail 'Migrations failed. Nothing further was run.' 'See the migrate output above.'
fi

# ---------------------------------------------------------------------------
# 5. Seed data (opt-in, guarded)
# ---------------------------------------------------------------------------
if [ "$SEED" -eq 1 ]; then
  step 'Loading development seed data'

  # Two independent guards, because one is not enough for a script that writes
  # rows: this one, and a SQL-level guard inside seed-dev.sql that aborts if the
  # database already holds any job whose apply_url is not on example.com.
  if [ "$APP_ENV_VALUE" != 'development' ]; then
    fail "Refusing to seed: APP_ENV is '${APP_ENV_VALUE}', not 'development'." \
         'Seed data is obviously-fake development content and must never reach a real database. Set APP_ENV=development in .env if this really is a local database.'
  fi

  [ -f "${SCRIPT_DIR}/seed-dev.sql" ] || fail "Seed file not found: ${SCRIPT_DIR}/seed-dev.sql" ''

  # Piped through the container's own psql, so no local PostgreSQL client is
  # needed. ON_ERROR_STOP=1 makes the SQL-level guard fatal instead of a warning
  # buried in the output.
  docker compose exec -T db psql -v ON_ERROR_STOP=1 -U "$PG_USER" -d "$PG_DB" \
    < "${SCRIPT_DIR}/seed-dev.sql" \
    || fail 'Seeding failed.' 'Read the psql output above - the guard in seed-dev.sql refuses to touch a database that already holds non-seed jobs.'

  ok 'Seed data loaded.'
fi

# ---------------------------------------------------------------------------
# 6. Hand over
# ---------------------------------------------------------------------------
# Literal rather than generated: `seq` is not guaranteed to exist everywhere
# this script is expected to run.
SEP='--------------------------------------------------------------------------'
cat <<EOF

${C_DIM}${SEP}${C_RESET}
 The database is up and migrated. Now run these in TWO separate terminals.
${C_DIM}${SEP}${C_RESET}

 ${C_CYAN}Terminal 1 - Go API  (http://localhost:8080)${C_RESET}
   cd backend
   go run ./cmd/server

 ${C_CYAN}Terminal 2 - Astro dev server  (http://localhost:4321)${C_RESET}
   cd frontend
   ${C_DIM}npm install      # first run only, and commit package-lock.json${C_RESET}
   npm run dev

 ${C_CYAN}First admin user (once, then delete ADMIN_BOOTSTRAP_PASSWORD from .env):${C_RESET}
   cd backend
   go run ./cmd/adminctl create

${C_DIM}${SEP}${C_RESET}
 Useful:
   ${C_DIM}docker compose logs -f db          database logs${C_RESET}
   ${C_DIM}docker compose down                stop, keep the data${C_RESET}
   ${C_DIM}docker compose down -v             stop and delete the data${C_RESET}
   ${C_DIM}./scripts/migrate.sh               re-apply migrations${C_RESET}
   ${C_DIM}./scripts/dev.sh --seed            load fake development jobs${C_RESET}
${C_DIM}${SEP}${C_RESET}

EOF
