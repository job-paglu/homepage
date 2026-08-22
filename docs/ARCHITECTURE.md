# Architecture

How Job Paglu is put together, why the shape is what it is, and what each layer
is allowed to do. If you are looking for exact request and response shapes,
[API-CONTRACT.md](API-CONTRACT.md) is normative and this document defers to it.

---

## 1. System diagram

```
                            Visitor (phone / tablet / laptop / desktop)
                                            |
                                       HTTPS + CDN
                                            |
                    +-----------------------------------------+
                    |   VERCEL project: job-paglu-web          |
                    |   Root Directory: frontend               |
                    |   Astro 5 SSR + prerendered static       |
                    |   @astrojs/vercel   (Vercel Functions)   |
                    |   home.jobpaglu.eu.cc                    |
                    |                                          |
                    |   /            prerendered               |
                    |   /jobs        SSR, edge-cached 60s      |
                    |   /jobs/:slug  SSR, edge-cached 300s     |
                    |   /404 /privacy /terms  prerendered      |
                    |   /admin/**    SSR, no-store, no cache   |
                    |   /api/**      REWRITE -> the API project|
                    +-----------------------------------------+
                                  |                      |
                       server-side fetch          browser fetch via the
                       (SSR, public reads,        SITE origin /api/* rewrite
                        API_BASE_URL, direct)     (admin only, w/ cookie)
                                  |                      |
                                  v                      v
                    +-----------------------------------------+
                    |   VERCEL project: job-paglu-api          |
                    |   Root Directory: backend                |
                    |   Go 1.24, net/http ServeMux             |
                    |   Vercel Go runtime, framework: "go"     |
                    |   entrypoint cmd/server/main.go, on PORT |
                    |   <project>.vercel.app  (never in a URL  |
                    |   the browser sees)                      |
                    |                                          |
                    |   /api/jobs           public, cacheable  |
                    |   /api/jobs/:slug     public, cacheable  |
                    |   /api/admin/**       session-gated      |
                    |   /health  /ready                        |
                    +-----------------------------------------+
                                            |
                                    pgx/v5 pool (TLS)
                                            |
                    +-----------------------------------------+
                    |   Neon  serverless PostgreSQL 17        |
                    |   free tier, scale-to-zero              |
                    |   tables: jobs, admin_users, sessions,  |
                    |           schema_migrations             |
                    +-----------------------------------------+
```

Three tiers, **two** provider accounts, one direction of dependency: the browser
knows about the frontend, the frontend knows about the API, the API knows about
the database. Nothing points back up.

Both Vercel projects are built from **one repository and one push**, and are kept
separate only by their Root Directory setting. See
[DECISIONS.md D-20](DECISIONS.md#d-20--vercel-for-both-the-frontend-and-the-backend)
for why two projects rather than one, and [DEPLOYMENT.md](DEPLOYMENT.md) for the
setup.

---

## 2. Rendering model

`frontend/astro.config.mjs` sets `output: 'server'`. **SSR is the default and
static is the exception**, which is the opposite of the usual instinct for a
content site. The reason is a single product requirement:

> A job published in the admin dashboard must be public the instant it is
> published.

With a fully static build, publishing a job produces no visible change until
something rebuilds and redeploys the site. That is a webhook, a build queue, a
minute or two of latency, and a new failure mode ("the deploy failed, so the job
your client is asking about is still invisible"). It also means every publish
consumes build minutes, which are the metered resource on most free tiers.

With SSR at the edge plus a `Cache-Control` header, publishing takes effect as
soon as the relevant cache entry expires — 60 seconds for the list, 300 seconds
for a detail page — with no build step, no webhook and no deploy. The freshness
window is a number in a header rather than a piece of infrastructure.

Routes that genuinely never change opt back out with `export const prerender =
true`, so they cost nothing at request time:

| Route | Mode | Reason |
|---|---|---|
| `/` | prerendered | The home page is a shell: hero, value proposition, links. It renders no live job data, so there is nothing for it to be stale about. If it is ever changed to list live featured jobs, it must be switched to SSR at the same time — a prerendered page that reads the API at build time will silently serve whatever was published on build day. |
| `/jobs` | SSR, `s-maxage=60` | The list changes whenever anything is published or unpublished. |
| `/jobs/:slug` | SSR, `s-maxage=300` | A detail page changes only when that job is edited. |
| `/404`, `/privacy`, `/terms` | prerendered | Static copy. |
| `/sitemap.xml`, `/robots.txt` | SSR | The sitemap enumerates published jobs, so it must be generated from live data. |
| `/admin/**` | SSR, `no-store` | Per-session content. Caching it anywhere would be a data leak. |

`trailingSlash: 'never'` and `build.format: 'file'` are set together so that the
canonical URL a page advertises and the path the file lives at cannot drift
apart. A canonical mismatch is the most common self-inflicted SEO wound on a job
board, because every job page has exactly one URL that must stay stable
forever.

---

## 3. Request lifecycle

### 3.1 A visitor opens a job detail page

```
1. Browser            GET https://home.jobpaglu.eu.cc/jobs/senior-cloud-engineer
2. Vercel edge        cache lookup on the URL.
                      HIT and fresh  -> return the cached HTML. Stop here.
                      MISS or stale  -> invoke the web project's SSR function.
3. SSR function       Astro renders src/pages/jobs/[slug].astro on the server.
                      maxDuration 10s (astro.config.mjs) bounds this whole step.
4. Function -> API    getPublishedJob(slug) in frontend/src/lib/api.ts
                      GET https://<project>.vercel.app/api/jobs/senior-cloud-engineer
                      Direct to the API project via API_BASE_URL - SSR does NOT
                      go through the /api/* rewrite.
                      credentials: 'omit'  (a public read carries no cookie)
                      10s timeout, AbortController
5. API function       middleware chain: recover -> request ID -> logging ->
                      security headers -> CORS -> route match
6. Handler            jobs handler parses the slug, calls the service
7. Service            calls the repository; no SQL of its own
8. Repository         one parameterised query including the visibility
                      predicate. A draft cannot come back, because the
                      predicate is in the SQL, not in the handler.
9. Postgres           returns 0 or 1 row over TLS
10. API response      200 {"data": Job} with
                      Cache-Control: public, max-age=0, s-maxage=300,
                                     stale-while-revalidate=600
                      X-Request-Id: <id>
                      -- or 404 {"error":{"code":"JOB_NOT_FOUND", ...}}
11. Astro             404 -> render the 404 page with a 404 status.
                      200 -> render the page, including JSON-LD JobPosting
                      built from the real row, and the ApplyButton which
                      re-validates apply_url before emitting an href.
12. Function response HTML with the page's own Cache-Control, plus the security
                      headers from frontend/vercel.json (CSP, HSTS, nosniff,
                      Referrer-Policy, X-Frame-Options, Permissions-Policy).
                      Those are scoped to exclude /api/*, so they never
                      duplicate or override the API's own headers.
13. Vercel edge       stores it, serves it, and serves it stale for up to
                      stale-while-revalidate while refreshing in the
                      background
14. Browser           paints. No framework JavaScript is downloaded.
```

Two independent caches are in play and they are set from the same numbers: the
API tells the edge how long the *data* is good for, and the page tells the edge
how long the *HTML* is good for. Keeping them aligned is why the values live in
the contract rather than being invented per route.

### 3.2 An administrator publishes a job

```
1. Browser            Admin is on /admin/jobs, an SSR page with Cache-Control:
                      no-store. The React island is mounted (client:load).
2. Island             POST https://home.jobpaglu.eu.cc/api/admin/jobs/{id}/publish
                      SAME ORIGIN as the page, so no CORS and no preflight.
                      credentials: 'include'      -> sends __Host-jp_session
                      X-CSRF-Token: <token>       -> read from the login or
                                                     /api/admin/me response
                      Origin: https://home.jobpaglu.eu.cc (set by the browser)
3. /api/* rewrite     frontend/vercel.json forwards method, path, query, body
                      and the Cookie / X-CSRF-Token / Origin headers to the API
                      project, and returns every Set-Cookie intact. Handled at
                      Vercel's routing layer, so NO function is invoked here.
                      Origin is passed through unchanged, which is why
                      FRONTEND_URL is the SITE origin, not the API project URL.
                      (src/pages/api/[...path].ts does the same thing in code
                      and is the inert fallback - a rewrite is matched before
                      filesystem routes, so only one of the two is ever live.)
4. Session middleware reads the session cookie by iterating all cookies -
                      two with our name is 401, not a guess - hashes it, looks
                      up sha256(token) in the sessions table, checks idle and
                      absolute expiry. No row, or expired -> 401 UNAUTHORIZED,
                      and the chain stops before any handler runs.
5. CSRF middleware    state-changing method, so: X-CSRF-Token must match the
                      session's csrf_token in constant time, AND Origin must
                      equal FRONTEND_URL. Either failing -> 403 CSRF_INVALID.
6. Handler            admin publish handler; parses the UUID path parameter
7. Service            business rule: refuse to publish a job that would be
                      invalid in public - notably a missing or non-https
                      apply_url. This is the service's decision, not the
                      repository's and not the handler's.
8. Repository         UPDATE ... SET status='published',
                      published_at = COALESCE(published_at, now())
                      -- COALESCE is the whole rule: re-publishing must not
                      -- rewrite the original publication date.
9. API response       200 {"data": Job}, Cache-Control: no-store
10. Island            updates its local state, shows a success message via an
                      aria-live region, and leaves the row in place.
11. Public effect     the next public request that misses the edge cache sees
                      the job. Requests that hit a still-fresh cache entry see
                      it up to s-maxage seconds later. This is expected and is
                      documented in TROUBLESHOOTING.md.
```

The important structural point: **the admin mutation path and the public read
path share the service and repository layers but not a single middleware.** A
public request never enters the session or CSRF middleware, and an admin request
is never cacheable. There is no route on which a missing session check would
still return data.

---

## 4. Backend layering

The rule is one-directional: `handlers -> service -> repository`. Nothing calls
upward, and nothing skips a layer.

| Layer | Package | Responsibility | Forbidden |
|---|---|---|---|
| Middleware | `internal/middleware` | recover, request ID, logging, security headers, CORS, rate limiting, session auth, CSRF | Business rules. Middleware decides *whether* a request proceeds, never *what* it means. |
| Handler | `internal/jobs`, `internal/auth`, `internal/admin` | Parse the request (path parameters, query string, JSON body), call one service method, write the response envelope | Writing SQL. Touching the pgx pool. Making an authorisation decision that middleware already owns. Containing an `if` about business meaning. |
| Service | `internal/jobs`, `internal/auth` | Business rules, validation orchestration, slug derivation and de-duplication, publish and unpublish semantics, transaction boundaries | Reading `http.Request`. Writing `http.ResponseWriter`. Knowing about HTTP status codes at all — it returns typed errors and the handler maps them. |
| Repository | `internal/jobs`, `internal/auth` | Every SQL statement, parameterised with `$n`. Row-to-struct mapping. The visibility predicate. | Business rules. Deciding whether a user may do something. Formatting anything for display. |
| Validation | `internal/validation` | Shared field validators, including the `apply_url` allowlist | Depending on the database or on HTTP. It is a pure function library so it can be unit-tested with no fixtures. |
| Transport helpers | `internal/httpx` | The JSON envelope, the closed error-code set, request IDs | Knowing about jobs, admins or sessions. |

Why this is worth enforcing rather than just describing: the two rules that keep
this application safe are *the visibility predicate is in the SQL* and *the
apply-URL allowlist runs on every write*. Both are only reliable if there is
exactly one place they can be applied. The moment a handler is allowed to build
a query, "the handler forgot the predicate" becomes possible, and it is a bug
that leaks unpublished content while every test still passes.

Errors travel as typed values, not as HTTP statuses, until they reach the
handler. `internal/httpx` owns the mapping from a domain error to a code in the
closed set. That keeps the contract's error table a single switch statement
rather than a scattering of `http.Error` calls.

---

## 5. Frontend component inventory

All components live under `frontend/src/components/`. Public components ship no
framework JavaScript; where interactivity is needed it is a few lines of inline
vanilla TypeScript in the `.astro` file.

| Component | Role | Interactive? |
|---|---|---|
| `Header` | Site header, primary navigation, logo | Contains the mobile menu trigger |
| `MobileMenu` | Slide-in navigation below 900px | Yes — vanilla TS. Focus trap, `Escape` to close, restores focus to the trigger, `aria-expanded` on the button |
| `Footer` | Secondary navigation, legal links | No |
| `Hero` | Home page headline and value proposition | No |
| `JobList` | Layout wrapper for job cards; owns the single-column-to-grid transition | No |
| `JobCard` | One `JobSummary` in a list: title, company, location, job type, salary, featured marker | No |
| `Pagination` | Previous/next and page numbers, driven by `meta` from the API | Links only — vanilla TS at most; it must work with JavaScript disabled |
| `ApplyButton` | The one thing the whole product exists for. Re-validates `apply_url` before emitting `href`; renders a disabled state rather than a link if the check fails. `target="_blank"`, `rel="noopener noreferrer nofollow"` | No |
| `FreeServicePopup` | The "this service is free" modal | Yes — vanilla TS. `aria-modal`, focus trap, `Escape`, restore focus, dismissal persisted client-side so it does not reappear on every page |
| `LoadingState` | Skeleton or spinner for an API-driven region | No |
| `EmptyState` | "No jobs yet" — distinct from an error. An empty list is `200` with `data: []`, never a `404` | No |
| `ErrorState` | API failure, with a retry affordance when `ApiError.isRetryable` | Retry is a link or a form submit |
| `SeoHead` | Title, description, canonical, Open Graph, Twitter card, and the JSON-LD `JobPosting` block on detail pages | No |
| `admin/AdminShell` | Admin chrome: navigation, sign-out, session state | Minimal |
| `admin/JobForm` | **The only React island.** `client:load` | Yes — React 19 |

### Why exactly one React island

`JobForm` earns React because three behaviours interact continuously as the
administrator types: a live slug preview derived from the title, inline
`apply_url` validation that mirrors the server's allowlist, and an
unsaved-changes guard. Expressing that as three independent DOM listeners is how
you end up with a form that shows a stale slug after an undo.

Nothing on the public site has that character. The popup and the menu are open
or closed. Pagination is links. So React is registered in
`astro.config.mjs` — because the admin needs it — and mounted nowhere else,
which is why the public pages contain no framework runtime at all.

### Shared library

| Module | Contents |
|---|---|
| `lib/types.ts` | The TypeScript mirror of the contract: models, envelopes, the error-code set, request bodies, query objects, type guards. `LooseEnum` lets the API add an enum member without turning the frontend into a type error, so an unknown `job_type` renders verbatim instead of crashing |
| `lib/api.ts` | `apiFetch` — the single fetch core. Base-URL resolution, a 10s `AbortController` timeout (see the budget ladder in [DEPLOYMENT.md](DEPLOYMENT.md#9-timeouts-and-the-function-duration-limit) — it is currently equal to the SSR function's `maxDuration`, which a reviewer must resolve), envelope validation, and normalisation of every failure into one `ApiError` class carrying `code`, `status`, `details`, `requestId`, `retryAfterSeconds` and the `isRetryable` / `isNotFound` / `isUnauthorized` predicates the UI branches on |
| `lib/format.ts` | Date humanising, salary and job-type display formatting. All timestamps arrive as RFC 3339 UTC and are humanised only at render time |
| `lib/seo.ts` | Title, description, canonical and JSON-LD construction |
| `lib/slug.ts` | The client-side mirror of the contract's slug rules, for the live preview only. The server remains authoritative |

`resolveBaseUrl()` in `lib/api.ts` prefers `API_BASE_URL` during SSR and
`PUBLIC_API_BASE_URL` in the browser, and **throws** when neither is set rather
than silently issuing requests to a relative path. A misconfigured environment
should fail loudly at the first fetch, not produce a site that renders an empty
job list and looks like a database problem.

---

## 6. Cookies, same-site and CORS

This is the part of the design most likely to be broken by a deployment
decision, so the reasoning matters more than the settings.

Production hostnames:

| Service | Hostname | Seen by the browser? |
|---|---|---|
| Site, and `/api/*` | `home.jobpaglu.eu.cc` (`job-paglu-web`) | yes — the only origin it ever talks to |
| API | `<project>.vercel.app` (`job-paglu-api`) | no — reached server-to-server only |

**One origin, not two sibling subdomains.** The earlier design put the API on
`api.<domain>` and relied on sibling subdomains being same-site. That reasoning
does not apply here: per the Public Suffix List the registrable domain of
`home.jobpaglu.eu.cc` is **`eu.cc`**, which is shared with every other user of
that free-subdomain provider. "Same-site" on this domain includes strangers, so
it guarantees nothing worth building on — see
[SECURITY.md](SECURITY.md#the-eucc-shared-suffix-problem).

Instead, browser calls to `/api/*` are forwarded to the API project by the
`rewrites` entry in `frontend/vercel.json`. The cookies are therefore genuinely
**first-party**, and CORS does not engage for admin traffic at all. SSR still
calls the API project directly via `API_BASE_URL`, so the public, high-traffic
path pays no hop.

`frontend/src/pages/api/[...path].ts` implements the same forwarding in
application code and is kept as the platform-independent fallback. A Vercel
rewrite is matched before filesystem routes, so while the rewrite exists that
route is dead code — which is the intended state, and is why **only one of the
two should ever be configured**. See
[DEPLOYMENT.md](DEPLOYMENT.md#5-the-single-origin-topology).

```
# production
__Host-jp_session=<43-char base64url>; HttpOnly; Secure; SameSite=Lax; Path=/; Max-Age=43200
__Host-jp_csrf=<token>;                          Secure; SameSite=Lax; Path=/; Max-Age=43200

# development (APP_ENV=development): jp_session / jp_csrf, no Secure, no prefix
```

| Attribute | Why |
|---|---|
| `__Host-` name prefix | A browser refuses a `__Host-` cookie that carries a `Domain`, so no other host on the shared `eu.cc` registrable domain can forge or shadow ours. The prefix requires `Secure`, `Path=/` and no `Domain`, so name and attributes are derived from one decision in `config.Config.CookieName` |
| `HttpOnly` on the session cookie | Script cannot read it, so an XSS bug cannot exfiltrate the session |
| **not** `HttpOnly` on the CSRF cookie | The client is required to read it and echo it in `X-CSRF-Token`. That is the entire mechanism; making it `HttpOnly` would break it |
| `Secure` | Never sent over plaintext. Omitted only when `APP_ENV=development`, so `http://localhost` still works without weakening production |
| `SameSite=Lax` | Kept, but **inert on this domain** — a neighbouring `*.eu.cc` host is same-site with us. It costs nothing and becomes a real control again on an owned domain. The CSRF defence is the `X-CSRF-Token` and `Origin` checks |
| `Path=/` | The admin surface spans several paths — and `Path=/` is a `__Host-` precondition, so it is not adjustable |
| no `Domain` | Required by the prefix. Adding one silently breaks login |
| `Max-Age=43200` | A convenience for the browser. Authority is the server-side row: 12h idle, 7 days absolute |

Reading the session cookie iterates every cookie and **rejects a request
carrying two with our name**, because a host-only and a domain-scoped cookie are
indistinguishable on the wire and choosing between them is how a tossed cookie
becomes a fixated session.

`SameSite=None` is not the alternative and never was: it would relax the one
attribute that still helps on an owned domain in exchange for nothing here. See
[DEPLOYMENT.md](DEPLOYMENT.md#5-the-single-origin-topology).

**CORS is deliberately asymmetric.** Any request that carries a cookie is held
to an allowlist of exactly one origin, echoed from `FRONTEND_URL`, with
`Access-Control-Allow-Credentials: true`. `Access-Control-Allow-Origin: *` is
never used for a credentialed request — the specification forbids combining the
two, and even if it did not, `*` plus credentials is precisely the
misconfiguration that turns any website into an admin client. Public
`GET /api/jobs*` may be served to `*` *without* credentials, which keeps the
read API embeddable without weakening anything, because there is no session in
that path to steal.

---

## 7. Caching strategy

The values are fixed by [API-CONTRACT.md](API-CONTRACT.md) so the API and the
pages cannot disagree.

| Response | Header | Effect |
|---|---|---|
| `GET /api/jobs` | `public, max-age=0, s-maxage=60, stale-while-revalidate=300` | Browsers do not cache. The edge serves a cached list for 60s, then serves it stale for up to 5 more minutes while refreshing behind the request |
| `GET /api/jobs/{slug}` | `public, max-age=0, s-maxage=300, stale-while-revalidate=600` | A detail page is good for 5 minutes at the edge, stale-servable for 10 more |
| `/api/admin/*` | `no-store` | Nothing per-session is stored anywhere, by any cache, ever |
| `/health`, `/ready` | not cached | A cached health check is not a health check |

`max-age=0` with a non-zero `s-maxage` is the important combination. The browser
always revalidates, so a visitor never sees yesterday's page from their own
disk, while the shared edge cache absorbs the traffic. `stale-while-revalidate`
means the slow request — the one that has to cold-start a serverless instance
*and* wake a suspended Neon compute, two cold starts in series — is served from
stale cache rather than being made to wait.

Consequences to accept honestly:

- A newly published job can take up to `s-maxage` seconds to appear on a
  cached path. That is a deliberate trade of freshness for cost and speed. If
  instant publication ever becomes a hard requirement, the fix is targeted cache
  purging on publish, not a shorter `s-maxage` for everyone.
- `stale-while-revalidate` means a job unpublished in the admin dashboard can
  still be served from a stale cache entry briefly. The API is already correct
  at that point — `404` — so a visitor who clicks through from a stale list gets
  the 404 page. The list, not the job, is stale.

---

## 8. Observability

| Signal | Where |
|---|---|
| Request log | One structured line per request from `internal/middleware`: method, path, status, duration, request ID. Never a credential, never a full request body |
| `X-Request-Id` | On every response, in the log line, and in `error.request_id`. A user reporting "it said something went wrong" can be traced to one log line |
| Panics | The recover middleware logs the stack trace and returns `500 INTERNAL_ERROR` with a request ID and nothing else. Stack traces never reach a response body |
| Liveness | `GET /health` — does not touch the database, so a database outage does not get instances killed and restarted pointlessly. Probe the **API project's own URL**: a `/health` answered through the site's origin would report on the frontend |
| Readiness | `GET /ready` — `SELECT 1` with a 2s budget, `503 SERVICE_UNAVAILABLE` on failure |

---

## 9. Extension points

Built now, unused now, so the roadmap does not require a rewrite. "Cost to light
up" is engineering effort, not money.

| Extension point | What already exists | What lighting it up costs |
|---|---|---|
| Search | `GET /api/jobs` already parses and applies `q` in SQL | A search input, a form submit to `/jobs?q=`, and an empty-state variant. No API change. At scale, add a `pg_trgm` GIN index — see [DATABASE.md](DATABASE.md#indexes) — because a leading-wildcard `ILIKE` cannot use a btree |
| Filters | `location`, `country`, `job_type`, `remote_type`, `featured` are all parsed and applied | Filter controls plus URL-state plumbing so a filtered list is linkable and cacheable. No API change |
| Categories | `category` column exists and is returned | A category index page and a route. Decide first whether categories are free text or a controlled vocabulary; the column as designed permits either, but a taxonomy wants its own table |
| Pagination | In the response envelope from day one, and the `Pagination` component consumes `meta` | Already live |
| Featured placement | `is_featured` exists, is returned, and is the first term in the public ordering | Visual treatment only |
| Job expiry | `expires_at` exists and is part of the visibility predicate, so an expired job already disappears with no cron job | An admin UI field and a "expiring soon" view |
| Analytics | `view_count` and `apply_click_count` columns exist | Increment paths. Note the tension: a counter write on every page view defeats edge caching and adds a database write to the hot path. Do it asynchronously, or accept sampled counts, or move it to a separate endpoint the client calls after render |
| i18n | All UI copy is centralised in `frontend/src/lib/copy.ts`, so translation is a lookup change rather than a hunt through markup | A locale mechanism, routing, and translated copy. The database side is larger: job content itself is employer-supplied and not translatable |
| A `scheduled` status | `status` is a `CHECK`-constrained text column, not a Postgres enum | An `ALTER ... CHECK` migration, a `publish_at` column, and something to promote due rows. The schema change is additive and the visibility predicate already excludes anything that is not `published` |
| Employer / seeker accounts | Sessions and Argon2id already exist and are role-agnostic | Substantial: a role model, a second registration flow, ownership on `jobs`, and per-row authorisation in the repository layer. The current single-admin model is the honest reason this is out of scope rather than nearly done |

`view_count`, `apply_click_count`, `is_featured`, `expires_at`, `category`,
`experience_level` and `remote_type` are all nullable or defaulted, so none of
them required a backfill and none is on the required-field list in the contract.
That is the whole point of adding them now: an additive column is free, whereas
adding a required column to a live table is a migration with a data problem
attached.

---

## 10. What is deliberately absent

Naming these stops a future reader assuming they were forgotten.

| Absent | Why | If you need it |
|---|---|---|
| Client-side router / SPA shell | Every public page is a document. Navigation is the browser's job and it does it faster than a router that must first download itself | Astro view transitions, if the benefit is demonstrated |
| A cache layer (Redis) | The edge cache is the cache. Adding a second one adds a stateful service to a stateless design | Only after the edge cache is measured as insufficient |
| A message queue | Nothing in the request path is deferrable yet | Needed the moment email alerts exist |
| An ORM | Hand-written SQL is reviewable and its query plan is visible. See [DECISIONS.md](DECISIONS.md#d-6--pgxv5-and-hand-written-sql-no-orm) | Not planned |
| A third-party router | Go 1.22 put method and wildcard patterns in the standard library | Not planned |
| Dark mode | See [DECISIONS.md](DECISIONS.md#d-15--no-dark-mode-in-this-release). The tokens are structured for it | One `@media (prefers-color-scheme: dark)` block redefining the semantic layer, plus a full contrast and responsive re-verification |
| An admin action audit log | Not built. Flagged in [SECURITY.md](SECURITY.md#not-yet-done) as the most valuable next security addition | An append-only table plus a write in the service layer |
