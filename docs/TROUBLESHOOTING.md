# Troubleshooting

Organised by **symptom**, because that is what you have when you arrive here.
Each entry: what you see, the likely cause, how to confirm it, and the fix.

Two habits that shorten every investigation below:

- **Quote the `X-Request-Id`.** Every API response carries it, it is in the
  server log line, and it is in `error.request_id`. One header turns "something
  went wrong" into one log line.
- **Switch on the error `code`, not the message.** `code` is a closed set from
  [API-CONTRACT.md](API-CONTRACT.md) section 6. The message is prose and may be
  reworded.

---

## Index

**Build and startup**

- [`missing go.sum entry`](#missing-gosum-entry-or-go-mod-file-indicates-a-missing-dependency)
- [A Vercel build fails on a missing lockfile](#a-vercel-build-fails-on-a-missing-lockfile)
- [The Go entrypoint is not detected](#the-go-entrypoint-is-not-detected)
- [The server exits at boot naming a missing variable](#the-server-exits-immediately-at-boot-naming-an-environment-variable)
- [`PUBLIC_API_BASE_URL is not set`](#public_api_base_url-is-not-set)
- [The frontend builds locally and fails on the platform](#the-frontend-builds-locally-and-fails-on-the-platform)

**Auth and admin**

- [Login returns 200, then everything is 401](#admin-login-returns-200-and-then-every-subsequent-admin-call-returns-401)
- [The `__Host-` cookie is silently not set](#the-__host--cookie-is-silently-not-set)
- [`/api/*` returns the site's 404 instead of the API](#api-returns-the-sites-404-instead-of-reaching-the-api)
- [The API proxy returns 503](#the-api-proxy-returns-503-service_unavailable)
- [`Set-Cookie` lost through the proxy](#set-cookie-is-lost-through-the-api-proxy)
- [`403 CSRF_INVALID` on every mutation](#403-csrf_invalid-on-every-mutation)
- [`429` while testing logins](#429-rate_limited-while-testing-logins)
- [CORS preflight failure](#cors-preflight-fails-in-the-browser)

**Content**

- [A published job still 404s publicly](#a-published-job-still-returns-404-publicly)
- [A newly published job takes a minute to appear](#a-newly-published-job-does-not-appear-for-up-to-a-minute)
- [The Apply button renders disabled](#the-apply-now-button-renders-as-a-disabled-element-instead-of-a-link)
- [A slug did not change when the title did](#the-slug-did-not-change-when-i-changed-the-title)
- [`409 SLUG_CONFLICT` on a slug nothing is using](#409-slug_conflict-for-a-slug-that-appears-to-be-free)

**Runtime and performance**

- [The first request after idle is slow](#the-first-request-after-a-quiet-period-takes-several-seconds)
- [`/health` is fine but `/ready` returns 503](#health-returns-200-but-ready-returns-503)
- [`too many connections`](#too-many-connections-from-postgres)
- [A request dies at the function-duration limit](#a-request-dies-at-the-function-duration-limit)
- [Migrations fail or hang against Neon](#migrations-fail-or-hang-when-run-against-neon)

**Domain**

- [The custom domain is stuck unverified](#the-custom-domain-is-stuck-unverified)

**Windows**

- [PowerShell here-strings and UTF-8 BOM](#windows-a-file-written-from-powershell-breaks-a-tool-that-parses-it)
- [Other Windows shell traps](#windows-other-shell-traps)

---

## `missing go.sum entry`, or `go: mod file indicates a missing dependency`

**What you see**

```
missing go.sum entry for module providing package github.com/jackc/pgx/v5
        (imported by job-paglu/backend/internal/database);
        to add: go mod download github.com/jackc/pgx/v5
```

Or, on `go build` / `go test` / `go vet`, a wall of similar lines.

**Cause**

`go.sum` is not committed. This repository was authored on a machine with no Go
toolchain, so the checksum file could never be generated. `go.mod` declares the
dependencies; `go.sum` records their expected hashes, and Go refuses to build
without it.

**Confirm**

```bash
ls backend/go.sum        # bash - "No such file" confirms it
```

```powershell
Test-Path .\backend\go.sum    # False confirms it
```

**Fix**

```bash
cd backend
go mod tidy
```

Then **commit `go.sum`**. It is a lockfile: committing it is what makes every
later build and every CI run reproducible. Leaving it out means every fresh
checkout hits this same error.

If `go mod tidy` itself fails, it is a network or proxy problem, not a
dependency problem — check that `proxy.golang.org` is reachable, or set `GOPROXY`
to a mirror you can reach.

---

## The server exits immediately at boot, naming an environment variable

**What you see**

The process starts and exits non-zero, with a single line naming what is
missing — for example a message that `DATABASE_URL` is required, or that
`FRONTEND_URL` must be an absolute URL.

**Cause**

`backend/internal/config` validates configuration at startup and **fails fast**.
This is deliberate. The alternative — starting anyway and failing on the first
request that needs the value — turns a five-second config mistake into a
production incident with a confusing symptom.

**Confirm**

Read the message. It names the variable. That is the whole diagnosis.

**Fix**

Set it. Required in every environment:

| Variable | Example placeholder |
|---|---|
| `DATABASE_URL` | `postgres://USER:PASSWORD@HOST:5432/DB?sslmode=require` |
| `ADMIN_SESSION_SECRET` | `changeme-generate-a-32-byte-random-string` |
| `FRONTEND_URL` | `https://jobpaglu.com` — scheme included, **no trailing slash** |
| `APP_ENV` | `development` or `production` |

Optional, with defaults: `PORT` (8080), `RUN_MIGRATIONS_ON_START` (false),
`ALLOW_HTTP_APPLY_URLS` (false). Full table:
[DEPLOYMENT.md](DEPLOYMENT.md#7-environment-variables), and
`.env.example` is the authoritative list of names.

Locally:

```bash
cp .env.example .env      # then fill it in
```

```powershell
Copy-Item .env.example .env
```

On a hosting platform, a variable that is *set but empty* fails the same check.
Two platform-specific traps: a variable added **after** the last build is not in
the deployment that is serving traffic — redeploy; and a variable set only for the
Preview environment is absent in Production, which produces this error on the
production deployment while the preview works perfectly.

---

## `PUBLIC_API_BASE_URL is not set`

**What you see**

At build time, or when a page renders:

```
PUBLIC_API_BASE_URL is not set. Copy .env.example to .env and set it to the API
origin, e.g. http://localhost:8080
```

**Cause**

`resolveBaseUrl()` in `frontend/src/lib/api.ts` throws rather than guessing. It
prefers `API_BASE_URL` during SSR and `PUBLIC_API_BASE_URL` in the browser, and
if neither is set it raises this error instead of issuing requests to a relative
path — which would produce an empty job list that looks like a database problem.

**Confirm**

```bash
grep -c PUBLIC_API_BASE_URL .env      # 0 means it is absent
```

```powershell
Select-String -Path .\.env -Pattern 'PUBLIC_API_BASE_URL'
```

**Fix**

| Where | How |
|---|---|
| Local | `PUBLIC_API_BASE_URL=http://localhost:8080` in `frontend/.env` (or the repository-root `.env`, depending on how you run the dev server) |
| Vercel | Project → Settings → Environment Variables on `job-paglu-web`, for **both** Production and Preview. Then redeploy — Vite substitutes it at build time, so an existing deployment does not pick it up |

The `PUBLIC_` prefix means Vite exposes it to browser code. That is required, not
an oversight: the admin dashboard runs in the browser and must know where to send
fetches. It is a URL, not a credential. Never put a secret behind a `PUBLIC_`
prefix.

If it is set on the project and the error still appears, the variable was
probably added after the last build. Vite substitutes it at build time —
**redeploy**.

---

## A Vercel build fails on a missing lockfile

**What you see**

The web project's build log ends at `npm ci`:

```
npm error The `npm ci` command can only install with an existing package-lock.json
```

Or the API project's build log ends at module download:

```
missing go.sum entry; to add it: go mod download <module>
```

**Cause**

Neither `frontend/package-lock.json` nor `backend/go.sum` exists in this
repository yet — it was authored on a machine with no Node and no Go toolchain.
Vercel's build fails on exactly the same thing CI fails on, and for the same
reason: `npm ci` installs the tree a lockfile pins and refuses to re-resolve, and
`go mod download` cannot verify checksums without `go.sum`.

This is not a Vercel problem and there is no Vercel setting for it. Both projects
fail until the files are committed.

**Fix**

Run **`.github/workflows/bootstrap-lockfiles.yml`** once: Actions → Bootstrap
lockfiles → Run workflow. It resolves both trees on a runner and commits the
result. After that, run `CI` manually — a push made with `GITHUB_TOKEN` does not
trigger workflows.

Do **not** "fix" this by changing `npm ci` to `npm install` in a build command.
That gets a green tick by discarding the guarantee that production installs what
someone tested.

---

## The Go entrypoint is not detected

**What you see**

The API project's build succeeds but deploys nothing runnable, or the log says no
framework or no entrypoint was found, or every request to the deployment returns
`404`.

**Cause**

One of four things, in the order worth checking:

| Check | Requirement |
|---|---|
| **Root Directory** | Must be `backend` on the `job-paglu-api` project. Vercel's Go runtime looks for `go.mod` at the *project root*, and `backend/go.mod` is only "at the root" because Root Directory says so. Leave it unset and the runtime looks in the repository root, finds no `go.mod`, and detects nothing |
| **`framework`** | `backend/vercel.json` must contain `"framework": "go"`. Without it the runtime looks for file-based functions in `/api` instead of running a server, and this project has none |
| **Entrypoint path** | Must be one of `main.go`, `cmd/api/main.go`, or `cmd/server/main.go`. This project uses the third. Moving or renaming it silently turns "a Go server" into "no framework detected" |
| **`PORT`** | The process must listen on `$PORT`, on all interfaces. `config.Load` does this. A hard-coded port, or binding `127.0.0.1`, makes the deployment unreachable with an error that mentions neither |

**Confirm**

Read the build log's first lines: it names the detected framework and the
entrypoint. If it names neither, the problem is Root Directory or `framework`,
not your code.

**Fix**

Correct the setting, then **redeploy** — Root Directory is applied at build time,
so changing it does not affect the deployment already serving traffic.

---

## The frontend builds locally and fails on the platform

**What you see**

`npm run build` succeeds on your machine; the platform build fails, or a page
throws at runtime.

**Cause and fix**

Two distinct causes, and they need different answers:

| Cause | How to tell | Fix |
|---|---|---|
| **Wrong or missing adapter** | The error mentions no adapter, or an adapter that is not installed | `astro.config.mjs` must import `@astrojs/vercel` and `frontend/package.json` must list it. `@astrojs/cloudflare` and `frontend/wrangler.toml` were removed in [D-20](DECISIONS.md#d-20--vercel-for-both-the-frontend-and-the-backend); a stale reference to either is a build failure |
| **Node version** | A syntax or API error on a feature your local Node has and the build image does not | `frontend/package.json` declares `engines.node >= 20.11.0`. Vercel selects its Node version from the project settings, so raise it there rather than adding a version file |

Vercel's build runs on Node, not on a Web-standards-only isolate, so the whole
class of "missing Node built-in" failures that the previous host produced is
gone. `src/lib/api.ts` is nevertheless still written against `fetch`,
`AbortController` and `AbortSignal` rather than Node APIs, and it should stay that
way: that is what keeps the `/api/*` fallback route portable and keeps a future
host change from being a rewrite.

Prevention: run `npm run build` before every push. `npm run build` is
`astro check && astro build`, so a type error fails the deploy on purpose.

---

## Admin login returns 200 and then every subsequent admin call returns 401

**This is the single most likely deployment failure on this project.**

**What you see**

Login succeeds — `200`, the dashboard begins to render — and then every
`/api/admin/*` request returns `401 UNAUTHORIZED`. Or the page bounces
immediately back to the login form. The server log shows a successful login
followed by requests with no session, and nothing looks wrong.

**Cause, under the single-origin topology**

The browser is talking to the **backend's** origin instead of the site's, so the
cookie is set on one host and never sent to the other. Almost always this is one
wrong variable:

```
PUBLIC_API_BASE_URL = https://REPLACE-API-PROJECT.vercel.app   # WRONG
PUBLIC_API_BASE_URL = https://home.jobpaglu.eu.cc              # right
```

`PUBLIC_API_BASE_URL` is what the **browser** uses. In this deployment it must be
the site's own origin, so admin calls land on the site's `/api/*` route and the
cookie stays first-party. Set it to the API project's URL and every admin fetch is
cross-origin again — a different registrable domain, so `SameSite=Lax` withholds
the cookie and the request arrives sessionless.

Two things that look like this cause and are not:

- **A wrong `API_BASE_URL`** does not do this. That variable is SSR-only; getting
  it wrong breaks public job pages, not the dashboard.
- **A missing `Set-Cookie` through the proxy** produces the same symptom from a
  different place. See
  [`Set-Cookie` lost through the proxy](#set-cookie-is-lost-through-the-api-proxy).

**Confirm** — three checks, under a minute:

1. Network tab, any `/api/admin/*` request, look at the **request URL**. Does it
   start with `https://home.jobpaglu.eu.cc/api/`? If it starts with a
   `*.vercel.app` host, that is the whole bug and `PUBLIC_API_BASE_URL` is wrong.
2. DevTools, Application/Storage, Cookies. Is `__Host-jp_session` present under
   `home.jobpaglu.eu.cc`? If it is present under a `*.vercel.app` host instead, same
   conclusion. If it is absent entirely, go to
   [the `__Host-` entry](#the-__host--cookie-is-silently-not-set).
3. The failing request's **Request Headers**: is there a `Cookie:` header
   carrying the session cookie? Absent means the browser withheld it.

**Fix**

Set `PUBLIC_API_BASE_URL` to the site origin **for the build** — Astro inlines it
at build time, so setting it only in the Pages dashboard does not change an
already-built artefact — then rebuild and redeploy. Full procedure in
[DEPLOYMENT.md](DEPLOYMENT.md#5-the-single-origin-topology).

Do **not** "fix" it with `SameSite=None`, and do not move the token into
`localStorage`. Both trade a configuration problem for a permanent weakening —
see [SECURITY.md](SECURITY.md#4-csrf).

**Also 401, but for a different reason:** a request carrying **two** cookies
named `__Host-jp_session` is rejected deliberately, because the server cannot
tell which is ours and guessing is how a tossed cookie becomes a fixated session
([SECURITY.md](SECURITY.md#the-eucc-shared-suffix-problem)). It should be
impossible with the `__Host-` prefix in place; the server logs
`rejecting a request with duplicate session cookies` when it happens. The user
fix is to clear cookies for the site.

**Related but different:** if login worked and then stopped after 12 hours, that
is the idle expiry doing its job. Sign in again. If it stopped after 7 days,
that is the absolute expiry.

---

## The `__Host-` cookie is silently not set

**What you see**

Login returns `200` with a `Set-Cookie` header visible in the Network tab, and
**no cookie appears** in Application → Cookies. Every following admin call is
`401`. Nothing is logged anywhere, because nothing failed — the browser simply
declined to store it.

**Cause**

A browser stores a cookie whose name begins with `__Host-` only if **all three**
of these hold. Any one missing means the cookie is discarded without a warning:

| Requirement | How it gets broken |
|---|---|
| `Secure` is set | `APP_ENV` is not `production` on the backend, so `Secure` is omitted while the name is still prefixed — or the reverse. The two are derived from one expression in `config.Config.CookieName` precisely so they cannot disagree; if they have, something bypassed it |
| `Path=/` | Someone narrowed the cookie's `Path`, e.g. to `/api` |
| **No `Domain` attribute at all** | Someone added `Domain=` to make the cookie "work across subdomains". This is the most likely cause, and it is exactly what the prefix exists to forbid |
| Served over HTTPS | A `Secure` cookie is dropped on a plain-`http` origin. Only relevant if you are testing production settings locally |

**Confirm**

Read the raw header rather than the cookie jar — Chrome and Firefox both show a
warning icon on a rejected `Set-Cookie` in the Network tab, with the reason:

```bash
curl -si -X POST https://home.jobpaglu.eu.cc/api/admin/login \
  -H 'Content-Type: application/json' \
  -H 'Origin: https://home.jobpaglu.eu.cc' \
  --data '{"email":"REPLACE_ADMIN_EMAIL","password":"REPLACE_PASSWORD"}' \
  | grep -i '^set-cookie'
```

You want, exactly:

```
Set-Cookie: __Host-jp_session=...; Path=/; Max-Age=43200; HttpOnly; Secure; SameSite=Lax
Set-Cookie: __Host-jp_csrf=...; Path=/; Max-Age=43200; Secure; SameSite=Lax
```

Any `Domain=` in either line, any missing `Secure`, or any `Path` other than `/`
is the fault.

**Fix**

- Missing `Secure`, or an unprefixed name on production: set `APP_ENV=production`
  on the **API** project and redeploy. An unset `APP_ENV` already defaults to production,
  so an explicit wrong value is the usual culprit.
- A `Domain` attribute: remove it. Do not remove the prefix instead — on a shared
  registrable domain the prefix is the control that stops a neighbouring host
  shadowing our cookie
  ([SECURITY.md](SECURITY.md#the-eucc-shared-suffix-problem)).
- Testing locally over plain http: use `APP_ENV=development`, which drops both
  the prefix and `Secure` together.

---

## `/api/*` returns the site's 404 instead of reaching the API

**What you see**

`curl -s https://home.jobpaglu.eu.cc/api/jobs` returns the site's HTML 404 page,
or an Astro `NOT_FOUND` envelope, rather than the API's job envelope. Every admin
call fails the same way. The rest of the site works perfectly.

**Cause**

The `/api/:path*` rewrite in `frontend/vercel.json` is not in effect. Four
candidates, in the order worth checking:

| Check | Detail |
|---|---|
| **`vercel.json` is in the right place** | It must be inside the project's **Root Directory**, i.e. `frontend/vercel.json`. A `vercel.json` at the repository root is not read by a project whose Root Directory is `frontend` |
| **The destination is real** | If it still says `REPLACE-API-PROJECT`, that is the bug. Paste the API project's actual URL |
| **The path shape matches** | `"source": "/api/:path*"` and `"destination": ".../api/:path*"`. Dropping `/api` from the destination sends `/jobs` to a backend that only serves `/api/jobs` |
| **The API project is deployed and public** | `curl -si https://<api-project>.vercel.app/health` from your own machine. If that fails, the rewrite is fine and the API is not |

**A specific trap.** If the rewrite is *absent*, the Astro catch-all route in
`src/pages/api/[...path].ts` takes over — and it will return
`500 INTERNAL_ERROR` naming `API_PROXY_TARGET` if that variable is unset, because
that route reads a different variable. So "the rewrite is missing" and "the
fallback is misconfigured" produce two different errors. Read which one you got:
an HTML 404 means neither mechanism matched; a JSON envelope naming
`API_PROXY_TARGET` means the fallback matched and has nothing to forward to.

**Fix**

Set the destination correctly and redeploy. **Do not configure both mechanisms.**
A rewrite is matched before filesystem routes, so with both present the Astro
route is dead code you will still be debugging. See
[DEPLOYMENT.md](DEPLOYMENT.md#5-the-single-origin-topology).

---

## The API proxy returns `503 SERVICE_UNAVAILABLE`

**What you see**

An admin call fails with the documented envelope, and the failing request is on
the site's own origin rather than the backend's:

```json
{ "error": { "code": "SERVICE_UNAVAILABLE", "message": "The API did not respond in time. It may be starting up — please try again." } }
```

**Cause**

The Astro fallback route in `frontend/src/pages/api/[...path].ts` has its own
request timeout (8 s) and translates it into this envelope rather than hanging a
function. Serverless instances start cold, so the **first** admin request after a
quiet period pays a function cold start *and* a Neon compute resume, in series.
If that exceeds the budget you get this.

Note which mechanism you are on: **with the `vercel.json` rewrite active this
envelope cannot be produced**, because no application code is in the path. On the
rewrite you get either the API's own response or the platform's error page — see
[A request dies at the function-duration limit](#a-request-dies-at-the-function-duration-limit).

The message distinguishes the two cases: "did not respond in time" is the
timeout; "could not be reached" means the fetch failed outright — wrong
`API_PROXY_TARGET`, a deleted service, or DNS.

**Confirm**

```bash
# Is the backend itself slow, or unreachable? Time it directly.
time curl -s -o /dev/null -w '%{http_code}\n' "${SERVICE_URL}/health"

# Warm it, then retry through the proxy. If the second attempt succeeds, it was
# a cold start, not a fault.
curl -s -o /dev/null "${SERVICE_URL}/ready"
curl -si https://home.jobpaglu.eu.cc/api/jobs | head -1
```

**Fix**

| Cause | Fix |
|---|---|
| Cold start, occasionally | Nothing. Retry. This is the accepted cost of a $0 bill; see [DEPLOYMENT.md](DEPLOYMENT.md#34-two-cold-starts-in-series) |
| Cold start, every time and annoyingly | Keeping an instance warm is a paid-plan feature, and a warm instance ends the $0 bill. The free mitigation is the one already in place: `stale-while-revalidate`, so a visitor is served from cache while the cold path runs behind them |
| "Could not be reached" | Check `API_PROXY_TARGET` was set **for the build**, and that it is the current API project URL. A `curl` to that URL from your own machine settles it |
| Timeout on a request that genuinely takes longer | Raise `PROXY_TIMEOUT_MS`, but keep the ladder intact: it must stay **below** `ADMIN_TIMEOUT_MS` in `lib/adminApi.ts`, which must stay below `maxDuration` in `astro.config.mjs`, which must stay within your plan's ceiling. Raising one number alone just moves which layer produces the ugly error. See [DEPLOYMENT.md](DEPLOYMENT.md#9-timeouts-and-the-function-duration-limit) |

Do **not** remove the timeout. Without it a cold backend can hold a function open
until the platform kills it, and then the caller gets a platform error page
instead of this envelope.

---

## `Set-Cookie` is lost through the API proxy

**What you see**

`POST /api/admin/login` returns `200` and a correct body, and **no cookie is
stored**. Same visible symptom as the `__Host-` entry above, but the raw
`Set-Cookie` header is missing or malformed rather than being rejected.

**Cause**

Login sends **two** `Set-Cookie` headers. Most HTTP header APIs collapse repeated
headers into one comma-joined string, and a cookie's `Expires` attribute
*contains a comma*, so anything that joins and re-splits them corrupts both. The
proxy therefore reads them with `Headers.getSetCookie()` and re-emits them with
`append`, never `get`/`set` and never `forEach`.

If the proxy has been edited to use `headers.get('set-cookie')`, or to copy
response headers with a single `forEach` loop that does not skip `set-cookie`,
this is what happens.

**Confirm**

Compare the backend directly against the same call through the proxy:

```bash
# Straight to the API project: how many Set-Cookie lines?
curl -si -X POST "${SERVICE_URL}/api/admin/login" \
  -H 'Content-Type: application/json' \
  -H 'Origin: https://home.jobpaglu.eu.cc' \
  --data '{"email":"REPLACE_ADMIN_EMAIL","password":"REPLACE_PASSWORD"}' \
  | grep -ci '^set-cookie'          # expect 2

# Through the proxy: the same count, and the same two names.
curl -si -X POST https://home.jobpaglu.eu.cc/api/admin/login \
  -H 'Content-Type: application/json' \
  -H 'Origin: https://home.jobpaglu.eu.cc' \
  --data '{"email":"REPLACE_ADMIN_EMAIL","password":"REPLACE_PASSWORD"}' \
  | grep -ci '^set-cookie'          # 2 = correct, 1 or 0 = this bug
```

One line where the backend sent two means the headers were joined. Zero means
they were dropped.

**Fix**

Restore the two rules in the proxy's response handling:

1. Skip `set-cookie` in the header-copy loop.
2. Read it with `getSetCookie()` and `append` each value separately.

Both are commented in `frontend/src/pages/api/[...path].ts`. Never split a
joined `set-cookie` string on commas — there is no correct way to do it.

---

## `403 CSRF_INVALID` on every mutation

**What you see**

Reads work. Every `POST`, `PUT` and `DELETE` under `/api/admin` returns:

```json
{ "error": { "code": "CSRF_INVALID", "message": "..." } }
```

**Cause**

One of the three CSRF conditions failed. In practice it is almost always the
first or the third:

1. `X-CSRF-Token` is missing, or does not match the session's token.
2. The token belongs to an older session — you logged in again, which **rotates**
   the token, and the client kept the old one.
3. `Origin` (or `Referer`) does not equal the backend's `FRONTEND_URL`.

**Confirm**

```bash
# Does the request carry the header?
curl -si -b "$JAR" -X DELETE "$API/api/admin/jobs/$JOB_ID" \
  -H "Origin: http://localhost:4321" | head -1        # no header at all -> 403

# Is your token current?
curl -s -b "$JAR" "$API/api/admin/me" | jq -r '.data.csrf_token'
```

In the browser: Network tab, the failing request, Request Headers. Check both
`X-CSRF-Token` and `Origin`. Then compare `Origin` **character for character**
against `FRONTEND_URL` on the backend.

**Fix**

| Cause | Fix |
|---|---|
| Missing header | Send `X-CSRF-Token` on every `POST`/`PUT`/`DELETE` under `/api/admin`. `apiFetch` sets it when you pass `csrfToken` |
| Stale token | Re-read it from `GET /api/admin/me` and retry once. A client should do this automatically, exactly once |
| `Origin` mismatch | Correct `FRONTEND_URL`. The traps below |

`Origin` comparison is exact. All four of these are mismatches:

| `FRONTEND_URL` | Browser `Origin` | Why it fails |
|---|---|---|
| `https://jobpaglu.com/` | `https://jobpaglu.com` | **Trailing slash.** An `Origin` header never has one |
| `http://jobpaglu.com` | `https://jobpaglu.com` | Scheme |
| `https://www.jobpaglu.com` | `https://jobpaglu.com` | `www` is a different host |
| `http://localhost:4321` | `http://127.0.0.1:4321` | `localhost` and `127.0.0.1` are different origins to a browser, even though they resolve to the same machine |

The trailing slash is the one that catches people, because it looks correct in a
browser address bar and is invisible in a config file.

Note that a **missing** `Origin` on a state-changing request is also rejected, by
design. That is why the `curl` examples in [API.md](API.md) all pass `-H "Origin:
..."` — a raw `curl` sends no `Origin` at all.

---

## `429 RATE_LIMITED` while testing logins

**What you see**

```
HTTP/1.1 429
Retry-After: 743
{"error":{"code":"RATE_LIMITED","message":"Too many attempts..."}}
```

**Cause**

You tripped the login limiter, which is exactly what it is for: 5 failed
attempts from one IP in 15 minutes, or 10 against one email in 15 minutes.

**Confirm**

Read `Retry-After`. If it is there, this is the limiter and not a bug.

**Fix**

Any of:

1. Wait `Retry-After` seconds.
2. **Log in successfully** — a successful login clears the counters.
3. **Restart the server.** The limiter is in-process, so a restart clears every
   counter. Perfectly fine locally and the fastest option while testing. On the
   deployed API this happens by itself, constantly, as instances come and go.

Do not raise the limits to make a test pass. Two consequences of the in-process
design worth knowing, both recorded in
[SECURITY.md](SECURITY.md#6-login-hardening): a production deploy resets an
attacker's budget, and each instance keeps its own counters — so the effective
limit is the configured limit multiplied by an instance count that, on serverless,
nothing in this repository bounds. The honest fix is a shared store, not a
different number.

---

## CORS preflight fails in the browser

**What you see**

In the console:

```
Access to fetch at 'https://api.jobpaglu.com/api/admin/jobs' from origin
'https://jobpaglu.com' has been blocked by CORS policy: Response to preflight
request doesn't pass access control check.
```

The Network tab shows an `OPTIONS` request that failed or returned no
`Access-Control-Allow-Origin`. The actual request never happened.

**First question: why is there a preflight at all?**

In the single-origin topology there should not be one. The browser calls
`/api/*` on the site's own origin, and a same-origin fetch is not a CORS request.
Seeing this error at all usually means `PUBLIC_API_BASE_URL` points at the
backend host instead of the site — the same root cause as
[the 401 entry](#admin-login-returns-200-and-then-every-subsequent-admin-call-returns-401),
surfacing as a CORS message. Fix that first.

The rest of this entry applies if you are deliberately running a two-origin
deployment, which is still supported.

**Cause**

`FRONTEND_URL` on the backend does not exactly match the browser's origin. The
CORS middleware echoes **exactly one** configured origin — never `*` for a
credentialed request — so any difference is a refusal.

**Confirm** — reproduce the preflight directly, which removes the browser from
the investigation:

```bash
curl -si -X OPTIONS "$API/api/admin/jobs" \
  -H "Origin: https://jobpaglu.com" \
  -H "Access-Control-Request-Method: POST" \
  -H "Access-Control-Request-Headers: content-type,x-csrf-token" | head -20
```

Expected: `204` (or `200`) with `Access-Control-Allow-Origin` echoing your exact
origin, `Access-Control-Allow-Credentials: true`, and `X-CSRF-Token` present in
`Access-Control-Allow-Headers`. If `Access-Control-Allow-Origin` is absent, the
origin was not recognised.

**Fix**

Set `FRONTEND_URL` to the browser's origin, exactly. The traps are the same four
as the CSRF `Origin` check above — trailing slash, scheme, `www`, and
`localhost` versus `127.0.0.1` — and the trailing slash is again the most common.

Then **redeploy**. `FRONTEND_URL` is read at startup; changing it in the console
without deploying a new revision changes nothing.

Two things that are *not* the fix:

- `Access-Control-Allow-Origin: *`. The spec forbids combining it with
  credentials, browsers reject it, and if it worked it would let any site act as
  your admin client.
- Adding a second allowlisted origin "temporarily". If you need staging to work,
  point staging's `FRONTEND_URL` at staging.

Note that CORS is not the same problem as the cookie problem above. CORS can pass
while the cookie is still withheld — they are independent browser mechanisms, and
fixing one does not fix the other. If preflight succeeds and requests are still
`401`, go back to [the 401
entry](#admin-login-returns-200-and-then-every-subsequent-admin-call-returns-401).

---

## A published job still returns 404 publicly

**What you see**

The admin list shows `status: published`. `GET /api/jobs/{slug}` returns
`404 JOB_NOT_FOUND`, and the job is absent from `GET /api/jobs`.

**Cause**

The visibility predicate has **three** conditions, and `status` is only one:

```sql
status = 'published' AND deleted_at IS NULL AND (expires_at IS NULL OR expires_at > now())
```

So the job is soft-deleted, or expired, or its status is not what you think.

**Confirm** — fetch the admin view of the same row, which shows all three:

```bash
curl -s -b "$JAR" "$API/api/admin/jobs?include_deleted=true&per_page=100" \
  | jq '.data[] | select(.slug=="THE-SLUG") | {slug, status, expires_at, published_at}'
```

Or directly in SQL:

```sql
SELECT slug, status, deleted_at, expires_at, published_at, now()
FROM jobs
WHERE slug = 'THE-SLUG';
```

**Fix**

| Finding | Fix |
|---|---|
| `status` is not `published` | `POST /api/admin/jobs/{id}/publish` |
| `deleted_at` is not null | The job was soft-deleted. Restore per [DATABASE.md](DATABASE.md#recovering-a-row) — and check first that nothing live has taken the slug |
| `expires_at` is in the past | Clear it, or set it to a future instant. Expiry needs no scheduler: it takes effect the moment the clock passes it, which is why a job can vanish with nobody having touched it |
| The row does not exist at all | Check the slug. Slugs are normalised — lowercased, non-alphanumeric runs collapsed to `-` — so what you typed may not be what was stored |

**Also check whether it is only the cache.** If the API returns `200` and the
*page* 404s, that is the next entry.

Note that this endpoint deliberately cannot tell you *which* condition failed. A
draft returning `403` instead of `404` would make unpublished slugs discoverable
by guessing.

---

## A newly published job does not appear for up to a minute

**What you see**

Publish succeeds. `GET /api/jobs` called directly shows the job. The `/jobs`
page does not, for up to about a minute — or a detail page shows old content for
up to five minutes.

**Cause**

The edge cache. Working exactly as designed:

| Path | `s-maxage` | `stale-while-revalidate` |
|---|---|---|
| `GET /api/jobs` and `/jobs` | 60s | 300s |
| `GET /api/jobs/{slug}` and `/jobs/:slug` | 300s | 600s |

`stale-while-revalidate` means the edge may serve a stale response *beyond*
`s-maxage` while it refreshes behind the request. So the worst case is longer
than `s-maxage` alone.

**Confirm**

```bash
# Is the API itself current? If yes, it is purely a cache question.
curl -s "$API/api/jobs" | jq '.data[] | select(.slug=="THE-SLUG") | .slug'

# What did the edge do with the page?
curl -sD - -o /dev/null https://jobpaglu.com/jobs | grep -i -E 'cache|age|cf-'
```

```powershell
curl.exe -sD - -o NUL https://jobpaglu.com/jobs | Select-String -Pattern 'cache|age|cf-'
```

Look at your CDN's cache-status header and at `Age`. A non-zero `Age` with a
cache-status of `HIT` is a cached response, and `Age` tells you how old it is. A
hard reload (`Ctrl`/`Cmd` + `Shift` + `R`) sends `Cache-Control: no-cache` and
usually bypasses it.

**Fix**

Usually: wait. This is the freshness-for-cost trade described in
[DECISIONS.md](DECISIONS.md#d-2--ssr-by-default-with-per-route-prerender).

If instant publication becomes a hard requirement, the right fix is **targeted
cache purging on publish** — not a shorter `s-maxage` for every visitor, which
throws away the caching for everyone to serve one editor.

The reverse case has the same cause: an **unpublished** job may still be served
from a stale cache briefly. The API is already correct (`404`), so a visitor who
clicks through from a stale list lands on the 404 page. The list is stale, not
the job.

---

## The **Apply Now** button renders as a disabled element instead of a link

**What you see**

The job page renders, but Apply Now is greyed out and not clickable.

**Cause**

`ApplyButton` **re-validates** `apply_url` at render time and, if the check
fails, renders a disabled state rather than an `href`. It fails closed. So the
stored value did not pass the scheme check.

This is the belt-and-braces half of the `apply_url` control
([SECURITY.md](SECURITY.md#2-the-apply_url-allowlist)): validation on write does
not cover rows changed by `psql`, a data-fix script, a restore from an older
backup, or a bulk import that skipped the validator.

**Confirm**

```sql
SELECT slug, apply_url FROM jobs WHERE slug = 'THE-SLUG';
```

Check against the allowlist. It must be an absolute URL with scheme `https`
(`http` only when `ALLOW_HTTP_APPLY_URLS=true`), a host containing a `.`, no
userinfo (`https://user:pass@host` is rejected), at most 2048 characters, and no
control characters or whitespace. Common real causes:

| Stored value | Problem |
|---|---|
| `example.com/careers` | No scheme — not absolute |
| `http://example.com/careers` | `http` while `ALLOW_HTTP_APPLY_URLS=false` |
| ` https://example.com/x` | Leading whitespace |
| `https://example.com/x\n` | Trailing newline, usually from a paste |
| `javascript:...` | Rejected — and the disabled button is the control working, not a bug |

**Fix**

Update the job through the admin form, which validates on write and gives you a
per-field error instead of a silent bad row. If the value looks correct to the
eye, suspect an invisible character: paste it into an editor that shows
whitespace, or check its length.

If it is `http://` and you genuinely need plaintext apply URLs, that is
`ALLOW_HTTP_APPLY_URLS=true` — but it should stay `false` in production, since a
plaintext apply link exposes the applicant's request to the network.

---

## The slug did not change when I changed the title

**What you see**

You renamed a job from "Senior Cloud Engineer" to "Staff Cloud Engineer" and the
URL is still `/jobs/senior-cloud-engineer`.

**Cause**

Not a bug. Contract section 4: **the slug is stable.** If `slug` is absent from
the `PUT` body, the existing slug is kept even when `title` changes.

That is deliberate. A live URL must not change because somebody fixed a heading.
A changed slug breaks every inbound link, every share, and every search result
pointing at the job.

**Fix**

If you genuinely want a new URL, send `"slug": "staff-cloud-engineer"` explicitly.
Understand what you are choosing: the old URL stops working — there is no
redirect table — so anyone arriving from an existing link gets a 404. For a job
that has been public for a while, keeping the old slug is almost always the right
call.

---

## `409 SLUG_CONFLICT` for a slug that appears to be free

**What you see**

Creating or updating a job with an explicit `slug` returns `409`, but no visible
job uses that slug.

**Cause**

Most likely a **soft-deleted** row still holds it. Uniqueness is enforced by a
partial index (`WHERE deleted_at IS NULL`), so a deleted row does **not** reserve
its slug — which means if you are getting a conflict, something live has it.

**Confirm**

```sql
SELECT id, slug, status, deleted_at FROM jobs WHERE slug = 'THE-SLUG';
```

```bash
curl -s -b "$JAR" "$API/api/admin/jobs?include_deleted=true&per_page=100" \
  | jq '.data[] | select(.slug=="THE-SLUG") | {id, slug, status}'
```

**Fix**

| Finding | Fix |
|---|---|
| A live row has it | Choose a different slug, or free it by changing the other job's slug first |
| A deleted row has it, and you still get `409` | The unique index is not partial. Check `001_init.sql` against [DATABASE.md](DATABASE.md#5-slug-uniqueness) — this is a schema bug, not a data problem |
| You want the server to sort it out | **Omit `slug` entirely.** A derived slug is de-duplicated automatically (`-2`, `-3`, …) and never returns `409`. Only an explicit slug can conflict |

Also check the reserved list — `new`, `admin`, `api`, `jobs`, `login`, `logout`,
`health`, `ready`, `sitemap`, `robots`. A derived slug matching one gets `-job`
appended; an explicit one is rejected by validation with `422`, not `409`.

---

## The first request after a quiet period takes several seconds

**What you see**

The first page load after minutes or hours of no traffic is slow. Everything
after it is fast.

**Cause**

**Two** cold starts in series, both by design:

| Layer | Behaviour |
|---|---|
| The API function | No instance is running. One must be cold-started, and the pgx pool created |
| Neon | Compute is suspended. It resumes on the first query |

Neither alone is dramatic; together they compound, and the first request pays
both.

**Confirm**

```bash
# Warm the API, then compare.
curl -s -o /dev/null -w 'cold: %{time_total}s\n' "$API/api/jobs"
curl -s -o /dev/null -w 'warm: %{time_total}s\n' "$API/api/jobs"
curl -s -o /dev/null -w 'warm: %{time_total}s\n' "$API/api/jobs"
```

A large first number followed by small ones is cold start, not a performance
problem. `/health` does not touch the database, so if `/health` is fast and
`/api/jobs` is slow on the first call, the database resume is the larger half.

**Fix**

This is the accepted cost of a $0 bill
([DECISIONS.md](DECISIONS.md#d-20--vercel-for-both-the-frontend-and-the-backend)).
Mitigations in increasing order of expense:

1. **Nothing.** `stale-while-revalidate` already means a visitor usually gets a
   cached page while the refresh pays the cold start behind the scenes. Warming
   the caches is what protects real users.
2. A periodic external ping to `/health` on the **API project's own URL** keeps an
   instance warm. Note that `/health` deliberately does not touch the database, so
   it does **not** keep Neon awake; a ping to `/ready` does, at the cost of a
   query on a schedule. Both consume invocations from your free allowance, so a
   ping every few seconds trades one cost for another.
3. A paid plan feature that keeps an instance warm. Removes the function half,
   and ends the $0 bill.
4. A Neon plan that does not suspend. Removes the other half, same caveat.

---

## `/health` returns 200 but `/ready` returns 503

**What you see**

```bash
curl -s "$API/health"   # {"data":{"status":"ok"}}
curl -si "$API/ready"   # HTTP/1.1 503 ... SERVICE_UNAVAILABLE
```

**Cause**

The process is alive; the database is not reachable. This split is intentional —
`/health` does not touch the database, so a dependency outage cannot get every
instance killed and restarted into a crash loop.

Candidates, in the order worth checking:

1. `DATABASE_URL` is wrong, or resolves to the wrong host.
2. Missing `sslmode=require`. Neon requires TLS.
3. The Neon project is suspended and the resume exceeded the 2s readiness
   budget — in which case the *next* call succeeds.
4. Connection limit exhausted. See the next entry.
5. The Neon project is paused, deleted, or its credentials were rotated.

**Confirm**

```bash
# Same credential, outside the app, if psql is available:
psql "$DATABASE_URL" -c 'SELECT 1'
```

Retry `/ready` a couple of times. If the second or third call is `200`, it was a
Neon resume, not a failure. If every call fails, it is credentials, networking,
or connections.

**Fix**

Correct the connection string, confirm the Neon project is active, and confirm
the stored environment variable holds what you think — a trailing newline or a
copied-in space is a classic, and it produces a parse error that reads like a
typo. See [DEPLOYMENT.md](DEPLOYMENT.md#7-environment-variables).

---

## `too many connections` from Postgres

**What you see**

Intermittent `503 SERVICE_UNAVAILABLE`, with a Postgres connection-limit error in
the logs. It appears under load, which is the worst time.

**Cause**

Each instance holds its own pgx pool, so the number of connections Postgres sees
is **pool size x live instance count**. On a serverless host that instance count
is not something the application sets and can rise sharply under a traffic spike,
which is exactly why this appears under load. The previous host had
`--max-instances 4`, which bounded the multiplier; there is no equivalent now, and
that is a documented consequence of the hosting choice
([D-20](DECISIONS.md#d-20--vercel-for-both-the-frontend-and-the-backend)).

**Confirm**

```sql
SELECT count(*), state FROM pg_stat_activity GROUP BY state;
```

Compare against `SHOW max_connections;` and your Neon plan's limit.

**Fix**

| Fix | Detail |
|---|---|
| Use the **pooled** Neon connection string for the application | The first thing to check, and on serverless it is **mandatory**, not advisory. `-pooler` in the hostname. See [DEPLOYMENT.md](DEPLOYMENT.md#32-two-connection-strings-and-which-goes-where) |
| Lower `DB_MAX_CONNS` | The per-instance ceiling. Default **2**. Since the multiplier is unbounded, this number is the only side of the multiplication you control — `1` is a legitimate value for a read-mostly workload |
| Check what is actually connected | `pg_stat_activity` will show whether the connections come from the pooler (few, reused) or directly (many, one set per instance). Many direct ones means `DATABASE_URL` is the wrong string |
| Do not raise `DB_MAX_CONNS` to "give it more room" | It multiplies the problem by definition. The config layer refuses anything above 64 for this reason |

Note that migrations must still use the **direct** string, even while the
application uses the pooled one.

---

## A request dies at the function-duration limit

**What you see**

A request — usually the first admin call after a quiet period, or a page render
during a Neon resume — fails with **Vercel's own error page** rather than a JSON
envelope. The runtime log for that invocation ends abruptly with a
task-timed-out message and no application error.

**Cause**

The platform terminated the function at its duration limit. That happens *before*
any application timeout fires, so none of the documented error envelopes gets a
chance to be produced. It is the single reason the budgets in this repository
have the values they do.

The ladder, and every step must be strictly below the next:

| # | Budget | Value | Where |
|---|---|---|---|
| 1 | Proxy upstream fetch | 8 s | `PROXY_TIMEOUT_MS`, `src/pages/api/[...path].ts` |
| 2 | Browser admin fetch | 9 s | `ADMIN_TIMEOUT_MS`, `src/lib/adminApi.ts` |
| 3 | Public SSR fetch | 10 s | `DEFAULT_TIMEOUT_MS`, `src/lib/api.ts` |
| 4 | SSR function duration | 10 s | `maxDuration`, `astro.config.mjs` |

**Confirm**

Check how long the invocation ran. If it stopped at almost exactly the plan's
limit, this is the cause. If it stopped at 8 s or 9 s with a JSON body, an
application budget fired and this is not your problem.

**Fix**

| Situation | Fix |
|---|---|
| The build rejected `maxDuration: 10` | Your plan's ceiling is lower. Lower all four numbers, keeping `1 < 2 <= 3 <= 4` |
| A page render is genuinely slow | #3 currently **equals** #4, which leaves no time to render after a full-length fetch. Lower `DEFAULT_TIMEOUT_MS` to 8 s, or raise `maxDuration` if the plan allows |
| The backend is the slow one | It is a separate function with its own limit. Look at the API project's logs, not the web project's |

Do not raise a single number in isolation. The ordering is the mechanism; a
budget above the platform's ceiling is not a budget.

---

## Migrations fail or hang when run against Neon

**What you see**

The runner hangs, or fails with an error about a transaction, an advisory lock,
or a prepared statement — and the same files applied cleanly against a local
Postgres.

**Cause**

You are migrating through the **pooled** connection string. A transaction-mode
pooler multiplexes clients onto shared backend connections and does not guarantee
that two statements land on the same session. Migrations need session affinity
for two reasons: DDL runs inside a transaction, and the runner takes a
`pg_advisory_lock`. A lock taken on one backend connection and released from
another does not work.

**Confirm**

Look at the host in `DATABASE_URL`. If it contains `-pooler`, that is the
problem.

**Fix**

Run migrations against the **direct** (non-pooled) URL, and leave the pooled one
for the application:

```bash
export DATABASE_URL='postgres://USER:PASSWORD@ep-EXAMPLE.REGION.aws.neon.tech/DBNAME?sslmode=require'
./scripts/migrate.sh
```

Then verify:

```sql
SELECT version, applied_at FROM schema_migrations ORDER BY version;
```

If a migration failed **partway**, remember that each file runs in one
transaction, so a failed file left nothing behind and is safe to re-run once you
have fixed the SQL. Do not edit an already-applied file — add a new one.
[DATABASE.md](DATABASE.md#rules) has the rules.

---

## The custom domain is stuck unverified

**What you see**

`home.jobpaglu.eu.cc` was added to the web project but the dashboard shows it as
invalid, pending, or awaiting configuration, for longer than feels reasonable.
The project's `*.vercel.app` URL works fine.

**Cause**

One of four, and they are worth separating because only two are your fault:

| Cause | How to tell |
|---|---|
| The `CNAME` is wrong or absent | `dig +short home.jobpaglu.eu.cc CNAME` returns nothing, or something other than the value the dashboard asked for |
| The `CNAME` value is stale | The standard target is `cname.vercel-dns.com`, but **the dashboard is authoritative** and the value has changed before. Use what it shows, not what a document says |
| Slow propagation at a free provider | The record is right and `dig` agrees, but the dashboard has not caught up. Free-subdomain providers are routinely slower than their TTL suggests |
| Certificate issuance is queued behind strangers | The domain validates but HTTPS is not yet available. Let's Encrypt rate limits are **per registrable domain**, and ours is `eu.cc` — shared with every other user of the provider. The budget is not yours and you cannot see it |

**Confirm**

```bash
dig +short home.jobpaglu.eu.cc CNAME
curl -sI https://home.jobpaglu.eu.cc | head -1
```

If `dig` shows the right target and the dashboard still disagrees, the record is
correct and you are waiting.

**Fix**

Fix the record if `dig` disproves it. Otherwise **wait and retry rather than
reconfiguring** — repeatedly removing and re-adding the domain does not speed
anything up and can consume issuance attempts. The failure mode here is "not yet
issued", not "issued to someone else": it is a delay problem, not a security
problem. The reliable fix for the whole class is owning a real registrable domain
([SECURITY.md](SECURITY.md#the-eucc-shared-suffix-problem)).

---

## Windows: a file written from PowerShell breaks a tool that parses it

**What you see**

A file you wrote from PowerShell is rejected by something that reads it. Symptoms
vary by tool and none of them mention the real cause:

| Tool | Symptom |
|---|---|
| `go` | A `.go` or `.sql` file with an "illegal character" or unexpected token at line 1, column 1 |
| `psql -f` | A syntax error at or near the very first statement |
| Node / Vite | `Unexpected token` in a `.json` or `.ts` file that looks perfectly fine |
| `git diff` | A phantom change on the first line only |

**Cause**

Two separate PowerShell behaviours:

1. **A UTF-8 byte-order mark.** `Set-Content` and `Out-File` in **Windows
   PowerShell 5.1** write a BOM (`EF BB BF`) with `-Encoding utf8`. Many parsers
   treat those three bytes as content, and they are invisible in every editor.
   `Set-Content` without `-Encoding` is worse: it writes the system ANSI
   codepage, which mangles any non-ASCII character.
2. **CRLF line endings**, which break a shell script or a heredoc that expects
   `\n`.

**Confirm** — look at the first bytes:

```powershell
Format-Hex -Path .\suspect.sql -Count 8
```

`EF BB BF` at offset 0 is a BOM. In bash:

```bash
head -c 3 suspect.sql | xxd     # efbbbf means BOM
file suspect.sql                # may say "UTF-8 Unicode (with BOM) text"
```

**Fix** — write UTF-8 **without** a BOM:

```powershell
# Works on Windows PowerShell 5.1 and on PowerShell 7+.
$content = @'
SELECT 1;
'@
[System.IO.File]::WriteAllText(
  (Join-Path $PWD 'out.sql'),
  $content,
  (New-Object System.Text.UTF8Encoding($false))    # $false = no BOM
)
```

On **PowerShell 7+** only, `Set-Content -Encoding utf8NoBOM` is available and is
simpler. On 5.1 it is not, so use the .NET call above — it behaves identically on
both, which is why it is the one to remember.

Here-string rules that bite people:

- The closing `'@` or `"@` **must be at column 0**, on its own line. Indenting it
  is a parse error.
- Use `@'...'@` (single-quoted, literal) unless you *want* `$variable` and
  backtick expansion. `@"..."@` interpolates, which will silently eat `$n` in a
  SQL placeholder or `$PORT` in a script.

For line endings, add a `.gitattributes` entry (`* text=auto`, and `*.sh
text eol=lf`) so shell scripts and SQL stay LF regardless of the checkout
platform.

---

## Windows: other shell traps

| Trap | Detail |
|---|---|
| `&&` and `\|\|` in Windows PowerShell 5.1 | Not supported — they are a parser error. Use `A; if ($?) { B }` |
| `cp`, `head`, `tail`, `which`, `touch` | Not PowerShell commands. Use `Copy-Item`, `Get-Content -TotalCount`, `Get-Content -Tail`, `(Get-Command x).Source`, `New-Item -ItemType File` |
| `export VAR=value` | Not PowerShell. Use `$env:VAR = 'value'` |
| `2>/dev/null` | Use `2>$null`. Avoid redirecting a native executable's stderr in 5.1 at all — it wraps each line in an error record and sets `$?` to `$false` even on exit code 0 |
| `curl` | In PowerShell, `curl` may be an alias for `Invoke-WebRequest`, whose flags are completely different. Call `curl.exe` explicitly when you want the real thing |
| Quoting JSON for `curl.exe` | PowerShell strips the outer quotes. Escape the inner ones (`-d '{\"k\":\"v\"}'`) or write the body to a file and use `-d "@body.json"`. The file approach is far less error-prone |
| Path separators | Go, Node and Docker all accept forward slashes on Windows. Prefer them; a backslash in a string is an escape character in most of these languages |
| Long paths | `node_modules` can exceed the legacy 260-character limit. Enable long-path support, or keep the repository near the drive root |
