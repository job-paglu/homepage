# `scripts/` — local development helpers

What each script does, what it needs, and the three things that will confuse you
on a fresh machine if nobody tells you first. Read the "First run" section
before running anything.

These scripts exist so that "get a working local Job Paglu" is one command
rather than a page of instructions. They do not do anything you could not do by
hand, and each one prints the commands it runs.

---

## The scripts

| File | Purpose |
|---|---|
| `dev.sh` / `dev.ps1` | Check prerequisites, create `.env` on first run, start Postgres in Docker, wait for its healthcheck, apply migrations, optionally seed, then **print** the two commands you run yourself. |
| `migrate.sh` / `migrate.ps1` | Apply pending migrations against `DATABASE_URL` and exit. |
| `seed-dev.sql` | Nine obviously-fake development jobs. Guarded three ways. Never for production. |

The `.sh` and `.ps1` pairs are behavioural twins. Pick whichever matches your
shell; you do not need both.

```bash
# bash / macOS / Linux / WSL / Git Bash
./scripts/dev.sh                 # normal case
./scripts/dev.sh --seed          # + load fake development jobs
./scripts/dev.sh --skip-migrate  # database only
./scripts/dev.sh --recreate      # destroy the data volume first (asks first)
./scripts/migrate.sh
```

```powershell
# Windows PowerShell 5.1 / PowerShell 7+
.\scripts\dev.ps1
.\scripts\dev.ps1 -Seed
.\scripts\dev.ps1 -SkipMigrate
.\scripts\dev.ps1 -Recreate
.\scripts\migrate.ps1
```

### Why `dev.*` stops instead of starting everything

It brings up the database, migrates it, and then **prints** the backend and
frontend commands for you to run in two more terminals. That is deliberate.

A script that owns three long-running processes has to reinvent log
multiplexing, Ctrl-C handling and per-process restart, and the first thing it
costs you is a readable stack trace — the backend's panic ends up interleaved
with Vite's HMR output, and you cannot restart one without restarting all three.
Two terminals is not a limitation being worked around; it is the thing that
keeps a crash legible.

---

## First run on a new machine — do these two things first

Neither can be done from CI, and both will fail loudly if skipped.

### 1. `frontend/package-lock.json` does not exist yet

No Node toolchain was available on the machine where this repository was
authored, so `npm install` has never run and there is no lockfile.

```bash
cd frontend
npm install
git add package-lock.json
git commit -m "chore: add frontend lockfile"
```

**Until that is committed, CI fails at its very first step.** The `frontend` job
in `.github/workflows/ci.yml` runs `npm ci`, and `npm ci` requires a lockfile —
it exits immediately without one. `actions/setup-node`'s npm cache also hashes
`frontend/package-lock.json` and errors when the file is missing.

This is *not* worked around by using `npm install` in CI. `npm ci` installs
exactly the tree the lockfile describes; `npm install` is free to resolve a
newer patch version, which means CI would be testing something subtly different
from what you tested, and a build could go red for a reason absent from the
diff. Swapping the command to get a green tick would trade the entire point of
having a lockfile for a cosmetic improvement.

### 2. `backend/go.sum` does not exist yet

Same cause.

```bash
cd backend
go mod tidy
git add go.mod go.sum
git commit -m "chore: add backend go.sum"
```

Without it: `go mod download` cannot verify module checksums,
`actions/setup-go`'s cache has no file to key on, and CodeQL's Go analysis
cannot build the module. CI also asserts the files stay tidy — it re-runs
`go mod tidy` and fails if that produces a diff, which catches an import added
without a matching commit.

### 3. Fill in `.env`

`dev.sh` / `dev.ps1` copy `.env.example` to `.env` on first run and never
overwrite an existing one. The copy contains **placeholders**. At minimum set:

- `ADMIN_SESSION_SECRET` — use the generator command documented in
  `.env.example`, and read the note there about which PowerShell one-liner is
  safe. `Get-Random` is not a cryptographic RNG and must not be used for this.
- `POSTGRES_PASSWORD` — any local-only value.
- `DATABASE_URL` — must match the `POSTGRES_*` values.

`migrate.*` refuses to run while `DATABASE_URL` still contains a `REPLACE_`
placeholder, so a half-filled `.env` fails with a clear message rather than a
confusing connection error.

---

## Inherited `.gitignore` — why `git status` changes behaviour after the repo split

**Today** this directory lives inside a larger AWS workspace, and that
workspace's `.gitignore` at the repository root ignores `.env*` and `*.env`
globally, with no negation for `.env.example`. Git applies the rules of *every*
`.gitignore` from the repository root down to the file, so while this directory
is a subdirectory:

- `job-paglu/.env.example` and `job-paglu/frontend/.env.example` can appear
  **ignored**, even though `job-paglu/.gitignore` negates them. The parent's
  pattern matches, and a negation in a *lower* file re-includes a file only if
  no parent pattern excluded its directory — the interaction is genuinely
  surprising and it is not a bug in this repository's rules.
- `git check-ignore -v <path>` is the tool that settles it. It prints the exact
  file, line number and pattern that decided, so you can see *which*
  `.gitignore` won:

  ```bash
  git check-ignore -v job-paglu/.env.example
  ```

**After the split** — once this directory is the root of
`github.com/job-paglu/homepage` — only `job-paglu/.gitignore` applies, the two
`.env.example` files are tracked as intended, and `git status` behaves the way
the file says it should. Nothing needs changing at that point; `.gitignore` here
is already written to be correct standalone.

If you need to stage the examples *before* the split, `git add -f` is the
documented workaround:

```bash
git add -f job-paglu/.env.example job-paglu/frontend/.env.example
```

CI has a step that fails if any real `.env` (as opposed to `.env.example`) is
ever committed, so using `-f` here cannot quietly become a habit that leaks a
secret.

---

## Prerequisites

`dev.*` checks all of these and names what to install for each one it cannot
find. It also checks that the Docker **daemon** is reachable, not just that the
CLI exists — "Docker Desktop is installed but not started" is the single most
common local failure on Windows.

| Tool | Version | Needed for |
|---|---|---|
| Docker + Compose **v2** | Docker Desktop 4.x / Engine with `docker-compose-plugin` | the Postgres container |
| Go | 1.24+ | building and running the API, and the migration path |
| Node.js + npm | 20.11+ | the Astro dev server and the frontend build |
| `curl` | any | `migrate.sh` polls `/health` with it |

Retired `docker-compose` v1 is not supported; the scripts probe for
`docker compose` and say so if only v1 is present.

A tool installed *after* you opened your terminal will not be on `PATH` in that
terminal. Open a new one.

---

## How migrations are applied, and why not `psql`

`migrate.sh` / `migrate.ps1` run **the server's own migration path**. They do
not shell out to `psql`.

The migration runner is not a separate tool. Decision D-7 embeds the `.sql`
files in the server binary with `embed.FS` and applies them from
`internal/database/migrate.go` during startup, gated on
`RUN_MIGRATIONS_ON_START`. That runner is also what maintains the
`schema_migrations` bookkeeping table.

So `psql -f backend/migrations/001_init.sql` would *appear* to work and would
leave the database in a state the server disagrees with: the DDL applied, but
`schema_migrations` empty. The next real startup would try to apply migration
001 again and fail on an already-existing table. Using the same code path the
server uses is the only way to keep the schema and its bookkeeping in agreement.

The mechanics:

1. `go build -o backend/bin/server ./cmd/server` — built rather than `go run`,
   because `go run` execs the compiled binary as a *child*, so stopping the `go`
   process can leave the server alive and holding the port.
2. Start it once with `RUN_MIGRATIONS_ON_START=true` on port **8099** —
   deliberately not 8080, so it cannot collide with a backend you already have
   running. Override with `MIGRATE_PORT` / `-Port`.
3. Poll `/health`. Migrations run during startup, before the listener opens, so
   a reply on `/health` proves they finished.
4. Stop it. The temporary server is not left running.

If the server exits before becoming healthy, the script prints its full output —
that is where a broken migration file or an unreachable database shows up.

`ADMIN_SESSION_SECRET` is validated at startup before the migration step, so the
script generates an ephemeral one from the OS CSPRNG when the environment has
none. It is used for a few seconds by a process that serves no request, is never
written to `.env`, and is never printed. It is rendered as hex rather than
base64 on purpose: `internal/config/config.go` rejects any secret containing a
template marker such as `todo` or `sample`, comparing case-insensitively, and a
random base64 string hits one of those by chance roughly once in twenty thousand
runs. The hex alphabet cannot spell them, so that flake is impossible rather than
merely rare.

### The placeholder in `.env.example` is meant to be rejected

`ADMIN_SESSION_SECRET` in `.env.example` deliberately contains the words
`CHANGEME` and `generate`, both of which `internal/config/config.go` treats as
template markers. A forgotten placeholder therefore fails at boot with a clear
message instead of quietly becoming a live session key. If you hit
`ADMIN_SESSION_SECRET still looks like the .env.example placeholder`, that is the
guard working — replace the value, do not lengthen it.

### Manual escape hatch

For reading the SQL rather than running it — or against a database you are about
to throw away:

```bash
docker compose exec -T db psql -U jobpaglu -d jobpaglu < backend/migrations/001_init.sql
```

Remember the bookkeeping caveat above before doing this on anything you want to
keep.

---

## Seed data

`seed-dev.sql` inserts nine jobs. Every one is unmistakably fake: companies
called *Example Systems GmbH*, *Placeholder Labs Ltd*, *Fictional Widgets Oy*;
titles prefixed `SAMPLE`, `DEMO`, `TEST FIXTURE`, `PLACEHOLDER`; and every
`apply_url` on `example.com`, an IANA-reserved domain that can never belong to a
real employer. That is not decoration — if one of these rows ever escaped onto a
public page, a visitor must be able to tell at a glance and must not be able to
click through to a real site.

Five rows are publicly visible. The other four are each hidden by a *different*
clause of the visibility predicate:

```
status = 'published' AND deleted_at IS NULL
    AND (expires_at IS NULL OR expires_at > now())
```

one `draft`, one `unpublished`, one expired, one soft-deleted. If any of them
appears on `/jobs`, or resolves at `/jobs/<slug>` instead of returning 404, that
clause is missing from the query. This is the cheapest way to catch the single
most damaging bug the product can have — a draft becoming publicly readable.

**Three independent guards:**

1. `dev.sh` / `dev.ps1` refuse to seed unless `APP_ENV` is exactly
   `development`.
2. A `DO` block at the top of the SQL aborts the transaction if the database
   already holds *any* job whose `apply_url` is not on `example.com` — that is,
   it refuses to touch a database containing real listings, whatever `APP_ENV`
   claims. This guard survives someone running the file by hand, which the shell
   guard does not.
3. Fixed UUIDs and a `DELETE` scoped to exactly those ids make it idempotent.
   Re-running replaces the seed rows and touches nothing else.

Requires migrations to have been applied first. `dev.*` handles the ordering.

---

## Windows versus bash — the differences that actually matter

Behaviour is the same. These are the mechanical differences.

| | PowerShell | bash |
|---|---|---|
| Flags | `-Seed`, `-SkipMigrate`, `-Recreate`, `-Port 8099` | `--seed`, `--skip-migrate`, `--recreate`, `MIGRATE_PORT=8099` |
| Built binary | `backend\bin\server.exe` | `backend/bin/server` |
| Health polling | `Invoke-WebRequest -UseBasicParsing` | `curl -fsS` |
| Stopping the temp server | `Stop-Process -Force` — Windows has no signal to send a background process; acceptable because it is idle after the DDL commits | `kill`, then `kill -9` after ~3 s |
| Ephemeral secret | `RNGCryptoServiceProvider`, rendered as hex (works on 5.1; the static `RandomNumberGenerator::GetBytes(int)` is .NET 6+ / pwsh 7.2+ only) | `openssl rand -hex 32`, falling back to `/dev/urandom` |
| Reading `.env` | one key at a time via regex | one key at a time via parameter expansion — **never** `source .env`, which executes the file, so a backtick or `$(...)` in a password would run as a command |

**Line endings.** `.gitattributes` stores `*.ps1` as CRLF and everything else,
including `*.sh`, as LF; `.editorconfig` tells your editor the same thing. The
`.sh` rule is a correctness requirement, not a preference: a CRLF after
`#!/usr/bin/env bash` makes the kernel look for an interpreter literally named
`bash\r` and the script dies with `bad interpreter: No such file or directory`.
If you hit that, your checkout predates `.gitattributes` — run
`git add --renormalize .`.

**Execution policy.** If PowerShell refuses to run `dev.ps1`, run it for the
current session only rather than changing the machine default:

```powershell
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass
```

**Git Bash.** Works, with two caveats. Paths beginning `/c/...` are sometimes
mangled when passed to `docker` — prefix the command with `MSYS_NO_PATHCONV=1`
if a bind mount lands somewhere unexpected. And `docker compose exec` may need
`winpty` if it complains about not being a TTY; the scripts pass `-T` to avoid
allocating one, which sidesteps this.

---

## Continuous integration, in one paragraph

`.github/workflows/ci.yml` runs four independent jobs — `frontend`, `backend`,
`backend-integration`, `security` — with no `needs:` between them, so a frontend
typecheck failure does not hide whether `go vet` passed. **All four are required
for merge**, including `backend-integration`: the tests behind
`-tags=integration` are the access-control tests (unauthenticated admin access,
CSRF rejection, draft invisibility, publish transitions, login rate limiting),
and "authorisation is broken but the build is green" is exactly the outcome
worth spending a service container to avoid. `npm audit` gates at **high** and
above rather than `low`, because it has no reachability analysis and would
otherwise be red most weeks over advisories in build tooling that no request can
reach — a gate that cries wolf gets disabled, and then the critical findings
stop being noticed too. `govulncheck` *is* allowed to fail the build, because it
is call-graph aware and its findings are nearly always actionable.
`.github/workflows/codeql.yml` is separate: it is slower, runs on a schedule as
well as on PRs, and needs `security-events: write`, which `ci.yml` deliberately
does not have. **None of these workflows has ever executed** — they are written
from the declared toolchain, and the two bootstrap steps above have to happen
before the first run can pass.
