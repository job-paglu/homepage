# Job Paglu

Job Paglu is an international jobs discovery platform. A visitor lands on the
site, browses jobs published from a real database, opens one, reads it, presses
**Apply Now**, and is sent to the exact URL the employer or administrator
supplied — unmodified, un-proxied, un-shortened. There is no account to create,
no CV to upload and no gate between a person and the job they want to apply for.
Everything that a visitor can do requires no login. Everything that changes what
a visitor sees happens in a separate, session-protected admin dashboard.

The public site is Astro 5 with TypeScript, server-rendered at the edge and
shipping no framework JavaScript. The API is a Go 1.24 service with two direct
dependencies. The data lives in PostgreSQL. All three tiers are deployable on
free-tier hosting.

---

## Contents

| Section |
|---|
| [Stack](#stack) |
| [Prerequisites](#prerequisites) |
| [Local development](#local-development) |
| [Creating the first admin user](#creating-the-first-admin-user) |
| [Project layout](#project-layout) |
| [Scripts](#scripts) |
| [Documentation](#documentation) |
| [Status](#status) |

---

## Stack

| Layer | Choice | Notes |
|---|---|---|
| Frontend framework | Astro 5 + TypeScript | SSR by default, `prerender` per route |
| Interactivity | Vanilla TypeScript on public pages; React 19 island in `/admin` only | Public pages ship 0 KB of framework JS |
| Styling | CSS custom properties + CSS Modules | Tokens in `frontend/src/styles/tokens.css` |
| Backend | Go 1.24, `net/http.ServeMux` | Method + wildcard patterns from the standard library |
| Database driver | `pgx/v5`, hand-written SQL, no ORM | Parameterised `$n` placeholders only |
| Database | PostgreSQL 17 | Neon in production, Docker locally |
| Migrations | Plain `.sql` files, `embed.FS` runner | `backend/migrations/`, forward-only |
| Auth | Server-side sessions in Postgres | Instantly revocable |
| Password hashing | Argon2id | 64 MiB, t=3, p=2, 32-byte key, 16-byte salt |
| Frontend host | Vercel project `job-paglu-web` (`@astrojs/vercel`) | Root Directory `frontend` |
| Backend host | Vercel project `job-paglu-api`, Go runtime | Root Directory `backend`, entrypoint `cmd/server/main.go` |
| Deploys | Vercel Git integration | Push to deploy. **No API token exists** |
| CI | GitHub Actions | Lint, typecheck, build, `go vet`, `go test`, dependency and secret scanning |

Every one of those choices, the alternative it beat, and what the alternative
would have been better at, is recorded in **[docs/DECISIONS.md](docs/DECISIONS.md)**.

---

## Prerequisites

| Requirement | Version | Required? |
|---|---|---|
| Node.js | >= 20.11.0 (the `engines` floor in `frontend/package.json`; 22 LTS is a fine choice) | yes, for the frontend |
| npm | ships with Node | yes, for the frontend |
| Go | 1.24 | yes, for the backend |
| Docker + Docker Compose | any current version | optional — only to run PostgreSQL locally |
| PostgreSQL | 17 | only if you prefer a native install to Docker |

Two things about the Go build are worth knowing before you start:

- **`go.sum` is not committed.** This repository was authored on a machine with
  no Go toolchain, so the checksum file could not be generated. Run `go mod
  tidy` once inside `backend/` before the first build; it resolves the module
  graph and writes `go.sum`. Commit the result. Until then `go build` and
  `go test` fail with `missing go.sum entry` — see
  [docs/TROUBLESHOOTING.md](docs/TROUBLESHOOTING.md).
- **Nothing in this repository has been executed.** The code was written and
  reviewed by reading. See [Status](#status).

---

## Local development

### 1. Clone and create your environment file

```bash
git clone https://github.com/job-paglu/homepage.git
cd homepage
cp .env.example .env
```

On PowerShell the last line is:

```powershell
Copy-Item .env.example .env
```

`.env.example` lists every variable by name with a placeholder value and is the
authoritative list. `.env` is git-ignored and must never be committed. Fill in at
least `DATABASE_URL`, `ADMIN_SESSION_SECRET`, `FRONTEND_URL` and
`PUBLIC_API_BASE_URL`. Full reference table:
[docs/DEPLOYMENT.md](docs/DEPLOYMENT.md#7-environment-variables).

Generate a session secret rather than typing one:

```bash
# bash
openssl rand -base64 32
```

```powershell
# PowerShell
[Convert]::ToBase64String((1..32 | ForEach-Object { Get-Random -Maximum 256 }))
```

### 2. Start PostgreSQL

```bash
docker compose up -d db
```

That starts only the database service, which is usually what you want: the
frontend and backend are more pleasant to run natively with hot reload.
`docker compose up -d` with no service name brings up the whole stack
(database + backend + frontend) if you would rather containerise everything.

If you use a native PostgreSQL install instead, create a database and point
`DATABASE_URL` at it. The connection string format is
`postgres://USER:PASSWORD@HOST:5432/DBNAME?sslmode=disable` locally and
`?sslmode=require` anywhere else.

### 3. Apply migrations

There are two ways, and they do the same thing:

```bash
# Option A - the server applies them on start.
# Set RUN_MIGRATIONS_ON_START=true in .env, then run the server as in step 4.

# Option B - apply them explicitly, without starting a server.
./scripts/migrate.sh          # bash
```

```powershell
.\scripts\migrate.ps1         # PowerShell
```

The embedded runner reads `backend/migrations/*.sql` in filename order, runs
each unapplied file in a transaction, and records it in `schema_migrations`. It
takes a Postgres advisory lock first, so two server instances starting
simultaneously cannot both try to apply `002_indexes.sql`. Running it twice is a
no-op. Details: [docs/DATABASE.md](docs/DATABASE.md#migrations).

`RUN_MIGRATIONS_ON_START=true` is convenient in development. In production it is
a deliberate choice with a tradeoff — see
[docs/DEPLOYMENT.md](docs/DEPLOYMENT.md#33-migrations-are-a-deliberate-action-never-a-cold-start).

### 4. Run the backend

```bash
cd backend
go mod tidy          # once, to generate go.sum
go run ./cmd/server
```

The server binds `PORT` (default `8080`). Confirm it is alive:

```bash
curl -s http://localhost:8080/health
curl -s http://localhost:8080/ready
```

`/health` answers whenever the process is alive and deliberately does not touch
the database. `/ready` runs `SELECT 1` and returns `503 SERVICE_UNAVAILABLE` if
Postgres is unreachable.

### 5. Run the frontend

In a second terminal:

```bash
cd frontend
npm install
npm run dev
```

Astro serves on `http://localhost:4321` by default and prints the URL. The
frontend reaches the API via `PUBLIC_API_BASE_URL`, which locally is
`http://localhost:8080`.

Both services on localhost are same-site by definition, so the admin session
cookie works without any extra configuration in development. That stops being
true the moment you deploy — read
[docs/DEPLOYMENT.md](docs/DEPLOYMENT.md#5-the-single-origin-topology) before your
first deploy, because it is the single most likely thing to break.

### 6. Optional: development seed data

```bash
./scripts/seed-dev.sh
```

`seed-dev` inserts obviously-fake rows and refuses to run unless
`APP_ENV=development`. No seeded job is ever intended to reach production, and
no seeded job is used to satisfy the structured-data requirement.

---

## Creating the first admin user

There is no self-service registration. Admin accounts are created with a
separate binary that has no HTTP surface at all:

```bash
cd backend
go run ./cmd/adminctl create --email admin@example.com --name "Site Admin"
```

The password is **never** passed as a command-line flag. Supply it one of two
ways:

| Method | How | When to use it |
|---|---|---|
| Interactive prompt | omit the password entirely; `adminctl` prompts with echo disabled | interactive use, the default |
| Environment variable | set `ADMIN_BOOTSTRAP_PASSWORD` before invoking | scripted or non-interactive bootstrap |

```bash
# non-interactive, without putting the value in your history:
read -rs ADMIN_BOOTSTRAP_PASSWORD && export ADMIN_BOOTSTRAP_PASSWORD
go run ./cmd/adminctl create --email admin@example.com --name "Site Admin"
unset ADMIN_BOOTSTRAP_PASSWORD
```

A flag would be visible to every other user on the machine via `ps`, would land
in your shell history file, and would be captured by any CI log — three
different disclosure paths for one convenience. The variable is read once,
hashed with Argon2id, and discarded.

`adminctl` also resets a password for an existing account; run it with no
arguments to see the subcommands it supports. Password storage rules are in
[docs/SECURITY.md](docs/SECURITY.md#5-password-storage).

Then sign in at `/admin/login` on the frontend.

---

## Project layout

```
job-paglu/
  frontend/                     Astro 5 + TypeScript
    src/
      components/               Header Footer Hero JobCard JobList ApplyButton
                                FreeServicePopup MobileMenu Pagination
                                LoadingState EmptyState ErrorState SeoHead
        admin/                  AdminShell + the one React island (JobForm)
      layouts/                  BaseLayout.astro  AdminLayout.astro
      pages/                    index, jobs/, jobs/[slug], admin/**, 404,
                                privacy, terms, sitemap.xml.ts, robots.txt.ts
      lib/                      types.ts  api.ts  format.ts  seo.ts  slug.ts
      styles/                   tokens.css  global.css  *.module.css
  backend/
    cmd/server/                 main.go  (the only binary that serves traffic)
    cmd/adminctl/               create/reset an admin user; no HTTP surface
    internal/
      config/                   env parsing, fail-fast validation
      database/                 pgx pool, embedded migration runner
      httpx/                    JSON envelope, error codes, request IDs
      middleware/               recover, logging, security headers, CORS,
                                rate limit, session auth, CSRF
      jobs/                     model, repository, service, validation, handlers
      auth/                     argon2id, sessions, login handlers
      admin/                    admin-scoped handlers
      validation/               shared field validators (incl. apply-URL)
    migrations/                 001_init.sql, 002_...
    tests/                      integration tests against a real Postgres
  docs/                         ARCHITECTURE DEPLOYMENT SECURITY API DATABASE
                                DECISIONS TROUBLESHOOTING
  scripts/                      dev.sh/ps1, migrate, seed-dev (dev data only)
  .github/workflows/ci.yml
  docker-compose.yml  Dockerfile  .env.example  .gitignore  README.md
```

`docs/` also contains `API-CONTRACT.md` (the normative API specification) and
`CONTRIBUTING.md`.

---

## Scripts

### Frontend (`cd frontend`)

| Command | What it does |
|---|---|
| `npm run dev` | Astro dev server with hot reload |
| `npm run build` | `astro check` then `astro build` — a type error fails the build |
| `npm run build:nocheck` | build without the type pass, for debugging a build issue only |
| `npm run preview` | serve the built output locally |
| `npm run check` | `astro check` — types and Astro diagnostics, no build |
| `npm run typecheck` | `tsc --noEmit` |
| `npm run test` | `vitest run` |
| `npm run test:watch` | `vitest` in watch mode |

### Backend (`cd backend`)

| Command | What it does |
|---|---|
| `go mod tidy` | resolve modules and write `go.sum` — run this first |
| `go run ./cmd/server` | run the API |
| `go run ./cmd/adminctl` | admin user management |
| `go build ./...` | compile everything |
| `go vet ./...` | standard static analysis |
| `go test ./...` | unit tests plus integration tests that need a reachable Postgres |
| `go test ./internal/...` | unit tests only |
| `gofmt -l .` | list files that are not gofmt-clean; should print nothing |

### Repository scripts (`scripts/`)

| Script | What it does |
|---|---|
| `dev.sh` / `dev.ps1` | bring up the database and start both services |
| `migrate.sh` / `migrate.ps1` | apply pending migrations without starting the server |
| `seed-dev.sh` | insert fake development rows; refuses unless `APP_ENV=development` |

---

## Documentation

| Document | Read it when |
|---|---|
| [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) | you want to know how a request actually flows, and what each layer may and may not do |
| [docs/API-CONTRACT.md](docs/API-CONTRACT.md) | **normative.** The exact shapes, status codes and rules. Change this before changing either implementation |
| [docs/API.md](docs/API.md) | you want a tutorial with runnable `curl` examples |
| [docs/DATABASE.md](docs/DATABASE.md) | you are touching the schema, an index, or a migration |
| [docs/DEPLOYMENT.md](docs/DEPLOYMENT.md) | you are deploying, rolling back, or tearing down |
| [docs/SECURITY.md](docs/SECURITY.md) | you are touching auth, sessions, CSRF, `apply_url`, or anything that handles a secret |
| [docs/DECISIONS.md](docs/DECISIONS.md) | you are about to argue with a technology choice — the reasoning and the rejected alternatives are all here |
| [docs/TROUBLESHOOTING.md](docs/TROUBLESHOOTING.md) | something is broken and you want the symptom looked up rather than debugged from first principles |
| [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md) | you are opening a pull request |

---

## Status

**Built and reviewed. Not yet executed.**

| Area | State |
|---|---|
| API contract | frozen — `docs/API-CONTRACT.md` |
| Design system | complete — `frontend/src/styles/tokens.css`, with hand-verified contrast notes |
| Public site | home, jobs list, job detail, 404, legal stubs, sitemap, robots |
| Admin dashboard | login, list, create, edit, publish, unpublish, soft delete |
| Backend API | public read endpoints, admin CRUD, publish/unpublish, health and readiness |
| Database | `jobs`, `admin_users`, `sessions`, `schema_migrations` with forward-only migrations |
| CI | configured in `.github/workflows/ci.yml` |
| Documentation | this file plus the nine documents above |

**The automated suites have not been executed on the authoring machine.** There
is no Node.js and no Go toolchain there, so `npm run test`, `npm run build`,
`go test ./...` and `go vet ./...` have never run, and no Lighthouse, bundle
size, latency or cost figure in this repository is a measurement. Every such
number is either a documented free-tier allowance or an explicit target.

The first task for anyone with a toolchain is therefore:

```bash
cd frontend && npm install && npm run check && npm run build && npm run test
cd ../backend && go mod tidy && go vet ./... && go test ./...
```

Record what actually happens. A test whose result was not written down did not
happen, and a test that was never run is not a pass. The same suites run in
GitHub Actions on every pull request, so CI can establish the first real result
without anyone installing anything locally.

---

## Licence

Not yet chosen. Add a `LICENSE` file before making the repository public.
