# Architecture decision records

Every significant technical choice in Job Paglu, with the context that forced
it, the alternatives, and the consequences.

**Each record names what the rejected option would have been better at.** A
decision record that lists only the winner's virtues is worthless six months
later, because the reason you are reading it is that the trade-off has started to
hurt and you need to know whether it was understood at the time.

Status of every record below: **Accepted, 2026-08-21**, except where a record
says otherwise. None has been validated by running the system — nothing in this
repository has been executed yet, so no performance or cost claim here is a
measurement.

**Superseded records are left in place, unedited, with an amendment note at the
top.** Deleting or rewriting one would destroy the only evidence of what was
known at the time, which is the entire value of the file. [D-10](#d-10--cloudflare-pages-for-the-frontend)
and [D-11](#d-11--google-cloud-run-for-the-backend) are superseded by
[D-20](#d-20--vercel-for-both-the-frontend-and-the-backend); read all three
together.

| # | Area | Chosen | Rejected | Status |
|---|---|---|---|---|
| [D-1](#d-1--astro-5--typescript-for-the-frontend) | Frontend framework | Astro 5 + TypeScript | Next.js | |
| [D-2](#d-2--ssr-by-default-with-per-route-prerender) | Rendering mode | SSR with per-route `prerender` | Fully static (SSG) |
| [D-3](#d-3--css-custom-properties--css-modules) | Styling | CSS custom properties + CSS Modules | Tailwind CSS |
| [D-4](#d-4--react-only-inside-admin) | Interactivity | React only in `/admin` | React everywhere |
| [D-5](#d-5--go-124-and-nethttpservemux) | Backend language and router | Go 1.24, `net/http.ServeMux` | chi / gin / echo |
| [D-6](#d-6--pgxv5-and-hand-written-sql-no-orm) | Database access | `pgx/v5`, hand-written SQL | GORM / sqlc |
| [D-7](#d-7--plain-sql-files-and-an-embedded-runner) | Migrations | `.sql` files + `embed.FS` runner | golang-migrate / Atlas |
| [D-8](#d-8--server-side-sessions-in-postgres) | Auth mechanism | Server-side sessions | Stateless JWT |
| [D-9](#d-9--argon2id-for-password-hashing) | Password hashing | Argon2id | bcrypt |
| [D-10](#d-10--cloudflare-pages-for-the-frontend) | Frontend host | Cloudflare Pages | Vercel | **SUPERSEDED by D-20** |
| [D-11](#d-11--google-cloud-run-for-the-backend) | Backend host | Google Cloud Run | Render / Fly.io / Koyeb | **SUPERSEDED by D-20** |
| [D-12](#d-12--neon-for-postgres) | Database host | Neon | Supabase |
| [D-13](#d-13--github-actions-for-ci) | CI | GitHub Actions | no CI |
| [D-14](#d-14--docker-for-local-dev-and-the-cloud-run-image-only) | Docker | Local dev + Cloud Run image only | Docker in production for the frontend too |
| [D-15](#d-15--no-dark-mode-in-this-release) | Theming | No dark mode in this release | Ship both palettes |
| [D-16](#d-16--system-font-stack-no-web-font) | Typography | System font stack | A web font |
| [D-17](#d-17--css-modules-over-tailwind-the-longer-answer) | Styling, in detail | CSS Modules | Tailwind |
| [D-18](#d-18--soft-delete-rather-than-hard-delete) | Deletion | Soft delete | Hard delete |
| [D-19](#d-19--sessions-in-postgres-rather-than-a-jwt) | Session storage, in detail | Postgres | JWT |
| [D-20](#d-20--vercel-for-both-the-frontend-and-the-backend) | Frontend **and** backend host | Vercel (+ Neon) | Staying on Cloudflare Pages + Cloud Run | **supersedes D-10, D-11** |

---

## D-1 — Astro 5 + TypeScript for the frontend

### Context

Job Paglu is a content site. Its entire interactive surface is: a dismissible
popup, a mobile menu, pagination links, and one admin form. Everything a visitor
reads is text that came from a database. It must be fast on a mid-range phone on
a slow connection, and it must be indexable, because organic search is how a job
board is found.

### Decision

Astro 5 with TypeScript. Islands architecture: zero JavaScript by default,
interactivity added explicitly where it is needed.

### Alternatives considered

**Next.js (App Router).** The default choice for this shape of project and a
genuinely excellent framework. It would have been better at: a much larger
ecosystem of examples and hiring pool; React Server Components, which are a more
elegant model than "islands" once a page has many interactive regions; a single
mental model for the public site and the admin dashboard instead of two (Astro
components plus a React island); `next/image` and font optimisation out of the
box; and a smoother path if this ever grows into an application rather than a
content site with an admin panel.

**SvelteKit.** Smaller runtime than React, excellent ergonomics. Rejected on
ecosystem breadth relative to the React island we do need, not on merit.

**Plain Go templates, server-rendered by the API.** Fewest moving parts of all —
one deployable, one language, no Node in the toolchain at all. Rejected because
it gives up the CDN edge rendering, and because the admin form's interactivity
would then be hand-written DOM code.

### Consequences

- **Cost:** $0. Astro is open source and builds on the free Pages plan.
- **Performance:** the public site ships **no framework JavaScript**. Not "a
  small amount" — none. That is the single largest lever on a content site's
  performance, and it is Astro's default rather than something to maintain.
- **Operational:** two component models in one repository. An `.astro` file and a
  `.tsx` file are not interchangeable, and a contributor has to know which is
  which and why. That is a real cost, paid once during onboarding.
- **Risk accepted:** if the product grows a genuinely interactive public surface —
  live search-as-you-type, saved-job state, a messaging feature — the island model
  starts fighting us, and Next.js becomes the better fit. The rewrite would be
  substantial. This decision is a bet that Job Paglu stays a content site, and
  that bet is explicit rather than assumed.

---

## D-2 — SSR by default, with per-route `prerender`

### Context

One product requirement dominates: **a job published in the admin dashboard must
be public immediately.** The site also has to be fast and cost nothing.

### Decision

`output: 'server'` in `astro.config.mjs`. SSR is the default; routes that never
change opt out with `export const prerender = true`. Freshness is controlled by
`Cache-Control` (`s-maxage=60` on the list, `s-maxage=300` on a detail page).

### Alternatives considered

**Fully static (SSG).** Would have been better at: absolute best-case latency
(a file from a CDN beats any computation); zero runtime cost and zero runtime
failure modes; and total immunity to an API outage, since the HTML already
exists. Rejected because publishing a job would require a rebuild and redeploy.
That means a webhook from the admin action to the build system, build minutes
consumed per publish, a build queue between "publish" and "public", and a new
class of incident: the deploy failed, so the job is invisible and the admin
dashboard says it is published.

**Incremental Static Regeneration.** Exactly the right model conceptually, and
it is Vercel's strongest single feature. Rejected because it is not available in
this form on Cloudflare Pages; `s-maxage` plus `stale-while-revalidate` is the
closest equivalent, and it is close enough.

**Client-side rendering (fetch on mount).** Rejected outright. A job board that
renders its content in JavaScript hands a crawler an empty page.

### Consequences

- **Cost:** SSR requests consume Workers invocations from the free-plan
  allowance. Static assets do not. The edge cache is therefore also the cost
  control, not just the speed control.
- **Performance:** a cache hit is as fast as static. A cache miss costs one
  render plus one API call, and after idle it also costs two cold starts (Cloud
  Run and Neon) in series.
- **Operational:** freshness is a header value, changeable in one place with no
  infrastructure. But a published job can lag by up to `s-maxage` on a cached
  path, which generates the support question "why isn't my job showing yet?" —
  documented in [TROUBLESHOOTING.md](TROUBLESHOOTING.md).
- **Correctness trap, recorded because it is easy to walk into:** `/` is
  prerendered on the assumption that it renders no live job data. If it is ever
  changed to list featured jobs, it must become SSR in the same commit.
  Otherwise it will serve whatever was published on build day, forever, silently.

---

## D-3 — CSS custom properties + CSS Modules

### Context

The design system has to be maintainable by one person, must not leak styles
between components, and must make a brand change cheap — the repository had no
existing brand assets, so a real logo or colour arriving later was a certainty
rather than a possibility.

### Decision

All design tokens as CSS custom properties in `frontend/src/styles/tokens.css`.
Component styles in `*.module.css`. No utility framework. No raw hex colour, raw
pixel font size or magic radius anywhere outside `tokens.css`.

### Alternatives considered

See [D-17](#d-17--css-modules-over-tailwind-the-longer-answer) for the full
Tailwind comparison.

**Styled-components / Emotion.** Rejected: runtime CSS-in-JS requires
JavaScript, and the whole point of D-1 and D-4 is that the public site has none.

**One global stylesheet.** Rejected: no scoping, so every change is a global
change, and the file grows without bound. The failure is gradual and irreversible.

### Consequences

- **Cost:** $0, no dependency, no build plugin.
- **Performance:** tokens resolve at runtime with no build step. Astro inlines
  small stylesheets automatically (`inlineStylesheets: 'auto'`), so critical CSS
  needs no manual extraction.
- **Operational:** the tokens are a genuine runtime API. A brand colour swap is
  one line in one file, and it propagates everywhere including to a dark palette
  later. The verified contrast ratios are documented as comments next to the
  values they describe, which is the only place anyone will read them.
- **Accepted cost:** more files than a utility approach, and more discipline
  required — nothing mechanically prevents a contributor writing `color: #1f5f4b`
  in a module. That is a review rule, not a compiler error, and review rules get
  forgotten. A stylelint rule banning raw hex outside `tokens.css` would make it
  mechanical; it is not configured yet.

---

## D-4 — React only inside `/admin`

### Context

D-1 gives us zero JavaScript by default. The question is where to spend some.

### Decision

React 19 is registered in `astro.config.mjs` and mounted in exactly one place:
`JobForm`, a `client:load` island in the admin dashboard. The popup, mobile menu
and pagination are Astro components with a few lines of inline vanilla
TypeScript. No public page mounts an island.

### Alternatives considered

**React everywhere.** Would have been better at: one component model instead of
two; a single testing approach; reusable form and state primitives across the
whole site; and a much easier path to a richer public surface later. Rejected
because it puts a framework runtime on every page a visitor loads, in exchange
for interactivity three components need and eleven do not.

**No React at all; vanilla TypeScript in the admin form too.** Genuinely
considered. It would have been better at: zero framework JavaScript anywhere,
one fewer dependency, one less mental model. Rejected because `JobForm` has three
behaviours that interact continuously as the user types — a live slug preview
derived from the title, inline `apply_url` validation mirroring the server's
allowlist, and an unsaved-changes guard. Hand-written DOM code for interacting
state is how you get a form that shows a stale slug after an undo, and that class
of bug is not worth the saved bundle on a page three people ever load.

**A web component or a small signals library (Preact, Solid).** Preact in
particular would give most of React's model at a fraction of the size. Rejected
on familiarity, and because the bundle size of the admin dashboard is not a
constraint worth optimising — it is an authenticated internal tool.

### Consequences

- **Performance:** the public bundle contains no framework JavaScript. The admin
  bundle contains React and nobody cares.
- **Operational:** two models to learn. The rule that keeps it sane is
  mechanical: **if it is under `components/admin/`, it may be React; otherwise it
  may not.** A reviewer can check that without judgement.
- **Risk:** a contributor adds `client:load` to a public component "just for
  this one thing", and the public site quietly starts shipping React. Worth a CI
  check that greps public pages for `client:` directives; not configured yet.

---

## D-5 — Go 1.24 and `net/http.ServeMux`

### Context

The API is small: two public read endpoints, nine admin endpoints, two health
endpoints. It must cold-start fast, because it scales to zero, and it must be
cheap to keep secure — which mostly means having few dependencies to audit.

### Decision

Go 1.24 with the standard library's `net/http.ServeMux`. Two direct dependencies
in total: `pgx/v5` and `golang.org/x/crypto`.

### Alternatives considered

**chi.** The idiomatic choice for this kind of service, and a genuinely small,
well-designed library. It would have been better at: route grouping (`r.Route`
with nested middleware is more readable than applying middleware per-handler);
mature sub-routers; and a large set of ready-made middleware. Rejected because Go
1.22 moved method and wildcard patterns into the standard library — `mux.Handle
("POST /api/admin/jobs/{id}/publish", h)` is now a stdlib call — so chi's
remaining advantage is ergonomic sugar over eleven routes.

**gin / echo.** Faster in benchmarks, larger surface, their own context type
threaded through every handler. Would have been better at: batteries-included
binding, validation and rendering helpers. Rejected because the custom context
type is invasive — it appears in every function signature and makes handlers
harder to test with plain `net/http` primitives — and because the routing
performance difference is irrelevant next to one database query.

**Node/TypeScript for the backend too**, sharing types with the frontend
directly. Would have been better at: one language, one toolchain, no duplicated
type definitions — the contract could be a shared package instead of a document
plus two mirrors. Rejected on cold start and memory: a static Go binary starts in
a fraction of the time, which matters specifically because the service scales to
zero.

### Consequences

- **Cost:** a ~15 MB static binary in a minimal container. Small images pull
  fast, which is part of cold-start time.
- **Performance:** cold start is dominated by process start and pool creation,
  not by framework initialisation.
- **Operational:** two dependencies means `govulncheck` output is short and
  actionable, and an upgrade is rarely a project. The cost is that anything a
  framework would have given us — request binding, structured validation errors,
  a router with groups — is written here. That is roughly the `internal/httpx`
  and `internal/middleware` packages: real code that must be tested, in exchange
  for code we can read.
- **Risk:** `ServeMux` pattern matching has precedence rules that are subtle in
  corner cases. Route registration deserves a test, which is what T-5 is for.

---

## D-6 — `pgx/v5` and hand-written SQL, no ORM

### Context

Eleven endpoints, four tables, and two queries that must be exactly right: the
public visibility predicate and the public list ordering.

### Decision

`pgx/v5` with hand-written SQL, `$n` placeholders throughout, no ORM and no
query builder.

### Alternatives considered

**GORM.** Would have been better at: speed of writing CRUD — associations,
automatic timestamps, soft-delete support built in (GORM's `DeletedAt` is
literally [D-18](#d-18--soft-delete-rather-than-hard-delete) as a library
feature); and migrations from struct definitions. Rejected because it hides the
query. When the public list query has to produce
`is_featured DESC, published_at DESC NULLS LAST, created_at DESC` against a
partial index, the SQL is the thing you need to see, and reading a struct tag to
infer what was sent is a worse experience than reading the statement.

**sqlc** — generate type-safe Go from `.sql` files. This was the closest call in
this document. It would have been better at: the SQL stays visible *and* the
scanning code is generated, which eliminates the single most common bug in this
design (a column missing from one `SELECT`, so a field is null on exactly one
endpoint). Rejected only because it adds a code-generation step to the build and
to CI, and generated code must be committed and kept in sync. **If this project
grows past a couple of dozen queries, sqlc is the first thing to revisit.**

**`database/sql` with `lib/pq`.** Rejected: `pgx` is the actively developed
driver, and its native interface is faster and supports Postgres types
`database/sql` flattens.

### Consequences

- **Performance:** the query plan is knowable by reading the code. `EXPLAIN` on a
  statement copied from the repository is exactly what runs in production.
- **Operational:** parameterised queries only, everywhere, which is the SQL
  injection control. It is easy to review — a grep for string concatenation near
  `Query` finds any violation.
- **Accepted cost:** row-scanning code is written by hand and is boring, and the
  failure mode is quiet. Adding a column means touching every `SELECT` list,
  the `INSERT`, the `UPDATE` and the scan. This is why
  [API.md](API.md#9-walkthrough-adding-a-field-to-job) enumerates all of them,
  and why sqlc remains the named escape hatch.

---

## D-7 — Plain `.sql` files and an embedded runner

### Context

Migrations must run in CI, locally, and at container start on Cloud Run, with no
extra binary to install in any of those places.

### Decision

Numbered `.sql` files in `backend/migrations/`, embedded with `embed.FS`, applied
by a roughly sixty-line runner in `backend/internal/database`. Forward-only, one
transaction per file, serialised by a Postgres advisory lock.

### Alternatives considered

**golang-migrate.** The standard answer. Would have been better at: down
migrations; a proven CLI; support for many databases; and a large body of
existing knowledge about its edge cases. Rejected because it means either a
binary in the deploy image and in CI, or importing a library that brings a driver
we already have — and because its down migrations are a feature this project
deliberately does not want (see
[DATABASE.md](DATABASE.md#why-forward-only)).

**Atlas.** Far more capable: schema diffing, linting, drift detection. Would have
been better at catching exactly the class of mistake this project is most
exposed to — a hand-edited production schema. Rejected as too much machinery for
four tables, and it is a service-shaped dependency for a project whose whole
premise is $0 and few moving parts. Worth revisiting if the schema ever gets
complicated enough that drift is plausible.

**Manual `psql` runs.** Rejected. That is the thing [DATABASE.md](DATABASE.md#rules)
forbids by name.

### Consequences

- **Cost:** zero dependencies, zero binaries, nothing to install anywhere.
- **Operational:** migrations are embedded in the binary, so the code and the
  schema it expects ship together and cannot get separated in transit.
  `RUN_MIGRATIONS_ON_START=true` makes deploys one step.
- **Accepted cost:** the runner is our code, so its bugs are our bugs — and a
  migration runner is a piece of code where a bug is expensive. The advisory lock
  is the part that most needs to be right, and it is what makes
  migrate-on-start safe with several instances starting at once.
- **Accepted cost:** no down migrations. Reversing a change means writing a new
  forward migration, which forces staged, additive changes for anything
  non-trivial. Slower, and the only way a code rollback does not require a schema
  rollback.

---

## D-8 — Server-side sessions in Postgres

Summary here; the full argument is [D-19](#d-19--sessions-in-postgres-rather-than-a-jwt),
which was written out at length because "why not a JWT?" is the question this
design gets asked most.

### Decision

Opaque 32-byte session tokens in an `HttpOnly` cookie. Only `sha256(token)` is
stored. Idle expiry 12h, absolute expiry 7 days, both enforced server-side.

### Consequences

- One indexed lookup per admin request, on a table with a handful of rows.
- Revocation is a `DELETE` and is immediate.
- The session store is a dependency: if Postgres is down, nobody can use the
  admin dashboard. Since the admin dashboard exists to edit rows in Postgres,
  this costs nothing real.

---

## D-9 — Argon2id for password hashing

### Context

One to a few admin accounts, each with full content control. The password is the
only thing protecting them, since there is no 2FA
([SECURITY.md](SECURITY.md#not-yet-done)).

### Decision

Argon2id via `golang.org/x/crypto/argon2`: 64 MiB memory, t=3, p=2, 32-byte key,
16-byte salt, PHC-encoded.

### Alternatives considered

**bcrypt.** Would have been better at: age and scrutiny — it has been deployed
and attacked since 1999; a simpler parameter story (one cost factor, not three);
lower and more predictable memory use, which matters on a memory-capped
container; and `golang.org/x/crypto/bcrypt` is equally available. It remains a
perfectly acceptable choice. Rejected because bcrypt is not memory-hard: it is
cheap to attack in parallel on a GPU. Argon2id is OWASP's current first
recommendation and resists exactly that.

**scrypt.** Also memory-hard and well regarded. Argon2id is the newer design and
the current recommendation; there was no reason to prefer scrypt.

**PBKDF2.** Rejected: weakest of the four against parallel hardware, chosen
normally only when a compliance regime names it.

### Consequences

- **Performance:** a login costs ~64 MiB and a few milliseconds of CPU **by
  design**. That is the defence. It also means `--memory 512Mi` on Cloud Run is a
  floor rather than generosity — concurrent logins each want their 64 MiB, and
  dropping the container's memory to save cents makes concurrent logins fail.
- **Operational:** PHC encoding stores the parameters with the hash, so they can
  be raised later and old hashes still verify. The upgrade path is a re-hash on
  next successful login, which needs no migration and no password reset.
- **Risk:** the parameters are a denial-of-service surface — an attacker who can
  reach the login endpoint can make the server allocate 64 MiB per attempt. The
  rate limiter is what bounds that, which is why it is per-IP *and* per-account.

---

## D-10 — Cloudflare Pages for the frontend

> **SUPERSEDED, 2026-08-22, by [D-20](#d-20--vercel-for-both-the-frontend-and-the-backend).**
> The frontend now deploys to Vercel. The text below is the original record and
> is left exactly as written, because it is the only account of what was known
> when the choice was made — including the Vercel objection that D-20 had to
> answer. It is history, not instruction.

### Context

Free hosting for an SSR Astro site with a custom domain, a global CDN, and a
bandwidth story that does not turn traffic growth into a bill.

### Decision

Cloudflare Pages with the `@astrojs/cloudflare` adapter, SSR on the Workers
runtime.

### Alternatives considered

**Vercel.** Would have been better at almost everything developer-facing: it is
Astro's most-tested target after Netlify; preview deployments and their DX are
the best in the category; ISR is available and is conceptually the right
rendering model for this site (see [D-2](#d-2--ssr-by-default-with-per-route-prerender));
its edge/Node runtime distinction is better documented; and image optimisation is
built in. Rejected on the free tier's terms: it is hobby-licensed with **metered**
bandwidth. For a project whose entire premise is $0 and whose upside case is a
traffic spike from a popular listing, metered bandwidth is the wrong shape of
risk.

**Netlify.** Similar position to Vercel, similar metering.

**Cloudflare Workers directly, without Pages.** More control, more configuration.
Pages is Workers with the build pipeline and git integration attached, so there
was nothing to gain.

**GitHub Pages.** Static only. Incompatible with [D-2](#d-2--ssr-by-default-with-per-route-prerender).

### Consequences

- **Cost:** $0. Unmetered static bandwidth, 500 builds/month, 100 custom domains.
  SSR routes consume Workers requests from the free-plan daily allowance.
- **Performance:** Cloudflare's edge network is among the largest, and SSR runs at
  the edge rather than in one region.
- **Operational, and this is the real cost:** the Workers runtime is **not
  Node.js.** It is a V8 isolate with a Web-standards API surface. Node built-ins
  are not available unless explicitly enabled, and a library that reaches for
  `fs`, `crypto`'s Node interface, or a native module fails at build or at
  runtime. `frontend/src/lib/api.ts` is written against `fetch`,
  `AbortController` and `AbortSignal` — deliberately, and with a hand-written
  bridge instead of `AbortSignal.any` precisely because the runtime targets
  differ. **Check runtime compatibility before adding any frontend dependency.**
- **Risk:** if a future dependency genuinely needs Node, the options are a
  Node-compat flag, a rewrite, or moving the frontend host. Recorded so nobody
  is surprised.

---

## D-11 — Google Cloud Run for the backend

> **SUPERSEDED, 2026-08-22, by [D-20](#d-20--vercel-for-both-the-frontend-and-the-backend).**
> The backend now deploys to Vercel's Go runtime. The text below is the original
> record, unedited. `backend/Dockerfile` is deliberately kept, so the container
> path this record describes remains available and this decision remains
> reversible. It is history, not instruction.

### Context

Host a Go container, publicly reachable over HTTPS on a custom domain, free at
low traffic, without a machine to maintain.

### Decision

Cloud Run, `--min-instances=0`, `--max-instances=4`, secrets from Secret Manager,
custom domain by domain mapping.

### Alternatives considered

**Render (free web service).** Would have been better at: simplicity — connect a
repository and it deploys, no `gcloud`, no project, no IAM, no Secret Manager,
and a far gentler learning curve than any hyperscaler. Rejected on one specific
behaviour: the free tier **sleeps**, and cold-starting it takes tens of seconds.
A visitor arriving at a job link does not wait that long. Cloud Run's scale-to-zero
resume for a small static binary is a fraction of that.

**Fly.io.** Would have been better at: genuine multi-region placement close to
users, and a very good CLI. Rejected because the free allowance has been reduced
and restructured over time, and it now generally expects a payment method — which
conflicts with the "$0, no card required to keep running" premise.

**Koyeb / Railway / Deta.** Small free tiers, less predictable longevity.

**Google App Engine / AWS Lambda + API Gateway.** Would have been better at:
per-request billing granularity. Rejected because both push you into a
platform-specific handler shape, whereas Cloud Run runs a container that listens
on a port — which means the same image runs locally under Docker Compose, in CI,
and in production, with no code path that exists only in the cloud.

**AWS anything.** Recorded explicitly because this workspace is otherwise
AWS-centric: AWS has no always-free tier comparable to Cloud Run's for a
container-hosted API, and no free managed Postgres. Fargate and RDS are both
metered from the first hour after any introductory period. That is the whole
reason this project provisions **zero** AWS resources.

### Consequences

- **Cost:** $0 within the always-free monthly allowance, and an idle service
  consumes none of it.
- **Performance:** cold start after idle, compounding with Neon's own resume
  (see [D-12](#d-12--neon-for-postgres)). `stale-while-revalidate` is what keeps
  a visitor from being the one who waits.
- **Operational:** a container is a portable unit — the same image runs
  everywhere. Secret Manager keeps credentials out of revision metadata.
  `--max-instances` is both a spend guard and a database-connection guard.
- **Accepted cost:** GCP is a large, complex platform, and the first deploy
  involves enabling APIs, IAM roles for the runtime service account, and a
  domain mapping. That is real onboarding friction compared with Render, and it
  is the price of the cold-start difference.

---

## D-12 — Neon for Postgres

### Context

Managed PostgreSQL, free, reachable from Cloud Run, that stays available for a
site with low and irregular traffic.

### Decision

Neon serverless Postgres 17, free tier, pooled connection string for the
application and the direct string for migrations.

### Alternatives considered

**Supabase.** Would have been better at: it is a whole backend platform — auth,
storage, realtime, row-level security, an auto-generated REST and GraphQL API,
and a genuinely good table editor. If this project needed any of that, Supabase
would win outright. Rejected on one operational behaviour: a free Supabase
project **pauses after about a week of inactivity** and requires a manual restore
to come back. That is precisely the failure mode a brand-new, low-traffic job
board hits — and it hits it silently, on a weekend, and the site is down until
somebody notices. Neon's compute suspends and **resumes on demand**, automatically, in under
a second.

**Railway / Render Postgres.** Time-limited or credit-based free tiers rather
than a persistent free plan.

**Self-hosted Postgres on a VM.** Cheapest at scale and full control. Rejected:
it is a machine to patch, back up and monitor, which is the opposite of this
project's premise.

**PlanetScale / a MySQL-family option.** Rejected on features: partial unique
indexes ([DATABASE.md](DATABASE.md#5-slug-uniqueness)), `timestamptz`, and
`pg_trgm` for the future search path are all Postgres capabilities this design
uses or plans on.

### Consequences

- **Cost:** $0 within the free project's storage and compute allowance.
- **Performance:** scale-to-zero means the first query after idle pays a resume
  cost. Combined with [D-11](#d-11--google-cloud-run-for-the-backend), the first
  request after a quiet period pays **two** cold starts in series. This is the
  single clearest cost of the whole $0 architecture, and it is documented in
  [DEPLOYMENT.md](DEPLOYMENT.md#34-two-cold-starts-in-series) rather than hidden.
- **Operational:** the pooled/direct split is a real trap. Migrations through the
  pooler can fail intermittently because DDL in a transaction and an advisory
  lock both need session affinity that a transaction-mode pooler does not
  guarantee. Documented in
  [DEPLOYMENT.md](DEPLOYMENT.md#32-two-connection-strings-and-which-goes-where).
- **Operational upside:** Neon's branching makes a pre-migration snapshot cheap
  and instant, which is the mitigation for forward-only migrations
  ([D-7](#d-7--plain-sql-files-and-an-embedded-runner)).

---

## D-13 — GitHub Actions for CI

### Context

Nothing in this repository has been executed. There is no Node and no Go
toolchain on the authoring machine, so **CI is not a nice-to-have here — it is
the only way anything gets verified at all** without someone installing a
toolchain first.

### Decision

GitHub Actions on every pull request: frontend lint/typecheck/build, backend
`go vet`/`go test`/`go build`, plus `gosec`, `govulncheck`, `npm audit` and secret
scanning.

### Alternatives considered

**No CI.** Would have been better at: nothing. For a repository whose code has
never been run, no CI means no verification exists anywhere.

**GitLab CI / CircleCI / Woodpecker.** All capable. Rejected on locality: the
code is on GitHub, and Actions needs no second account or integration.

**Pre-commit hooks only.** Would have been better at: catching problems before a
push, and faster feedback. Rejected as a *replacement* — hooks are local,
skippable with `--no-verify`, and prove nothing about a clean checkout. Good as
an addition.

### Consequences

- **Cost:** free minutes for a public repository.
- **Operational:** the build gate is `npm run build`, which is `astro check &&
  astro build`, so a type error fails the deploy rather than shipping.
- **Honest status:** the workflow is **configured and has never run.** The first
  push will very likely surface something — a missing `go.sum`, a lint rule, a
  version mismatch. That is CI doing its job, not a regression.
- **Gap:** integration tests need a real Postgres. A service container in the
  workflow provides one, but this has not been exercised, so treat the first
  integration-test run as an unknown.

---

## D-14 — Docker for local dev and the Cloud Run image only

> **AMENDED, 2026-08-22, by [D-20](#d-20--vercel-for-both-the-frontend-and-the-backend).**
> `backend/Dockerfile` is still the only container in the repository and is still
> what `docker compose` runs locally, so the substance of this record holds. What
> changed is that no production deployment builds it any more: Vercel's Go runtime
> compiles from `go.mod` directly. The image is now kept for local development and
> as the documented portable path — which is precisely what keeps D-20 reversible.
> Read "the Cloud Run image" below as "a container image for any container host".

### Context

Local development needs Postgres. Cloud Run needs a container. Cloudflare Pages
does not want one.

### Decision

`docker-compose.yml` runs Postgres (and optionally the backend and frontend) for
local work. `backend/Dockerfile` builds the Cloud Run image. **No container is
built for the frontend.**

### Alternatives considered

**Containerise the frontend too, for symmetry.** Would have been better at:
one deployment model instead of two, and a dev environment identical to
production. Rejected because Cloudflare Pages does not run containers — it runs
Workers isolates. A frontend container would be an artefact nothing deploys.

**No Docker at all; require a native Postgres install.** Would have been better
at: one fewer prerequisite, and lower resource use on a laptop. Rejected because
"install and configure Postgres 17" is a much worse first-run experience than
`docker compose up -d db`.

**Dev containers / Nix.** Would have been better at reproducibility of the whole
toolchain. Rejected as more machinery than a two-service project needs.

### Consequences

- **Operational:** `docker compose up -d db` is the only Docker command most
  contributors will run. Docker is therefore listed as **optional** in the
  README, because a native Postgres works identically.
- **Asymmetry to remember:** the backend's production artefact is a container
  built from a `Dockerfile`; the frontend's is a build output uploaded to Pages.
  Two deployment models, deliberately, because the two platforms are different.
- **Accepted cost:** the frontend's production runtime (a Workers isolate) is
  *not* what runs in local dev (Node). That gap is where runtime-compatibility
  surprises live — see [D-10](#d-10--cloudflare-pages-for-the-frontend). `npm run
  build` and a Pages preview deployment are the way to catch them.

---

## D-15 — No dark mode in this release

### Context

`frontend/src/styles/tokens.css` is structured in two layers: a raw palette, and
a semantic layer that components reference. A dark theme is therefore *one*
`@media (prefers-color-scheme: dark)` block redefining the semantic layer — the
structure is already there and the file says so.

Nobody asked for dark mode.

### Decision

Ship light only. Keep the token structure that makes dark mode a small change
later, and document the omission in the token file itself so the next person does
not think it was an oversight.

### Alternatives considered

**Ship both.** Would have been better at: a nicer experience for the substantial
share of users who set a dark OS preference, and it is much cheaper to do while
the design is fresh than to retrofit against dozens of components.

Rejected on verification cost, which is where the real work is:

| Surface | Doubled by a second palette |
|---|---|
| Contrast verification | Every colour pair re-checked against the WCAG 2.1 AA target. `tokens.css` documents ratios *by hand* — 19.6:1, 6.4:1, 7.6:1, and the explicit warning that `--jp-accent` at 2.1:1 **fails as text**. Every one of those needs a dark-mode counterpart, verified, not assumed |
| Focus rings | `--jp-focus` and `--jp-shadow-focus` must remain visible against a dark surface, at 3:1 |
| Shadows | The elevation tokens are tinted toward the ink colour. On a dark background shadows read as nothing; dark mode needs borders or lighter surfaces instead, which is a *design* change and not a token swap |
| Responsive sweep | Twenty widths, re-checked, because a palette change can alter perceived density and thus wrapping decisions |
| The amber constraint | `--jp-accent` is background-only and always carries `--jp-ink` on top. That relationship has to hold in dark mode too, and the obvious dark-mode instinct — light text on amber — would break it |

Two untested palettes are worse than one verified palette. Shipping a dark mode
whose focus ring is invisible or whose amber badge has 2:1 contrast is an
accessibility regression dressed as a feature.

**A manual theme toggle instead of following the OS.** Same verification cost,
plus state to persist. Rejected for the same reason.

### Consequences

- **Cost:** $0 now; a bounded, well-understood piece of work later.
- **Operational:** the light palette is verified and the ratios are recorded next
  to the values. There is exactly one visual truth to maintain.
- **When it is added:** redefine only the semantic tokens under
  `:root` in a `prefers-color-scheme: dark` block, never a component-level
  override; re-verify every documented ratio and write the new numbers into the
  same comment block; and re-run the full responsive sweep. Treat it as a release
  with its own test pass, not a patch.

---

## D-16 — System font stack, no web font

### Context

A "premium" feel usually reaches for a typeface. The performance budget and the
accessibility targets have opinions about that.

### Decision

```css
--jp-font-sans:
  ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto,
  'Helvetica Neue', Arial, 'Noto Sans', sans-serif,
  'Apple Color Emoji', 'Segoe UI Emoji';
```

No `@font-face`, no font CDN, no self-hosted `.woff2`.

### Alternatives considered

**A self-hosted variable web font** (Inter, Geist, or similar). Would have been
better at: brand distinctiveness — a typeface is the single strongest carrier of
visual identity; and typographic consistency, since the same glyphs render on
every platform, so a heading that is perfectly balanced on macOS is not slightly
loose on Windows.

Rejected on cost, all of which lands on the visitor:

| Cost | Detail |
|---|---|
| Network | Even self-hosted and subset, a variable font is tens of kilobytes on the critical path, on a site whose entire premise is that it ships no JavaScript |
| Layout shift | `font-display: swap` produces a visible reflow; `optional` produces an inconsistent first paint; `block` produces invisible text. Every option is a compromise, and the metric it damages is the one users actually feel |
| Complexity | Subsetting, preloading, `unicode-range` — and an **international** job board has genuinely international content. A Latin subset breaks on a company name in Cyrillic, Greek or CJK, and the fallback then has to be good anyway |

**A font from Google Fonts by CDN.** Additionally rejected on privacy (a
third-party request per visitor) and on CSP — it means allowlisting an external
`font-src`/`style-src` host, which is a hole in an otherwise strict policy for a
typeface.

### Consequences

- **Performance:** zero font requests, zero font bytes, zero font-related layout
  shift. Text paints with the first paint.
- **Accessibility:** the OS font is the one the user's system has already tuned
  for legibility, and it respects any font-related accessibility settings they
  have set.
- **Internationalisation:** the stack ends in `sans-serif` plus the emoji
  families, so non-Latin content renders in whatever the platform provides —
  which is better than a subset web font can manage.
- **Accepted cost:** less brand distinctiveness, and the site looks slightly
  different per platform. The identity is carried instead by colour (deep emerald
  as the trust colour, warm amber as the single highlight, near-black ink on warm
  off-white paper), by the fluid `clamp()` type scale, by generous measure limits
  (`--jp-measure: 68ch`) and by layout. That is a defensible way to look premium,
  and it costs the visitor nothing.
- **If a brand typeface is ever mandated:** self-host it, subset it, preload one
  weight, keep the system stack as the fallback, and measure the layout shift
  before and after. Do not add it because a mock-up looked nicer.

---

## D-17 — CSS Modules over Tailwind: the longer answer

[D-3](#d-3--css-custom-properties--css-modules) records the decision. This record
exists because "why not Tailwind?" is a question that deserves a real answer
rather than a preference.

### What Tailwind would have been better at

Genuinely, and this list is not grudging:

| Tailwind advantage | Why it is real |
|---|---|
| No naming | Naming CSS classes is a permanent tax. Tailwind removes it entirely |
| Colocated styles | You read the markup and know exactly what it looks like. No jumping between two files, no wondering whether a class is still used |
| Dead style elimination | Unused utilities never reach the output. A CSS Modules file accumulates rules for markup that has changed, and nothing tells you |
| Enforced design constraints | Spacing and colour come from a fixed scale, so nobody writes `margin-top: 13px`. This is exactly the discipline D-3 has to enforce by review instead |
| Responsive ergonomics | `md:grid-cols-2` next to the element beats a media query in another file, especially in a mobile-first design with five breakpoints |
| Dark mode | `dark:` variants make [D-15](#d-15--no-dark-mode-in-this-release) mechanical rather than a redesign |
| Ecosystem | Enormous. Components, plugins, and a large pool of developers who already know it |

That is a strong case, and for a team of five building a product with dozens of
screens, Tailwind is very probably the right answer.

### Why not here

| Reason | Detail |
|---|---|
| Build coupling | Tailwind scans source files to generate CSS. That is a build-time dependency on a content-glob configuration, and a class name assembled dynamically is a class name the scanner misses. The failure is a style that works in dev and vanishes in production |
| The tokens want to be runtime | This design's premise is that a brand colour is a **one-line change in one file** — because the repository had no brand assets and a real colour arriving later was certain. CSS custom properties are live values in the browser: change one and everything downstream updates, including future themes. Tailwind's design tokens are build-time configuration; changing one requires a rebuild, and mapping a Tailwind theme onto CSS variables to get the runtime behaviour means running both systems |
| Markup legibility | An accessible job card carries semantic elements, ARIA attributes and real content. Adding fifteen utility classes to each element makes the markup harder to audit for the thing that matters here — heading order, label association, focus targets |
| Scale mismatch | Fourteen public components and one admin form. Tailwind's ergonomic wins compound with surface area, and there is not much surface area to compound over |
| One less dependency | A project with two Go dependencies and five npm dependencies is cheap to audit and cheap to upgrade. Tailwind plus its PostCSS toolchain is a meaningful addition to that count |

### Consequences

- Style scoping is guaranteed by CSS Modules' generated class names, so no
  component can leak into another.
- Tokens are a runtime API; a brand swap is one line and a dark palette is one
  block.
- **The discipline is not mechanical.** Nothing stops a contributor writing a raw
  hex value in a module file. Tailwind would have made that impossible. Here it is
  a review rule — and the honest mitigation is a stylelint rule banning raw
  colours and raw font sizes outside `tokens.css`, which is **not configured
  yet** and should be.
- **Reversal cost:** high in both directions. Migrating fourteen components from
  modules to utilities, or the reverse, is a rewrite of every stylesheet. This is
  not a decision to revisit casually.

---

## D-18 — Soft delete rather than hard delete

### Context

`DELETE /api/admin/jobs/{id}` is one click behind one confirmation dialog, and
the thing being deleted is a job posting someone spent time writing, that may
have inbound links and search rankings.

### Decision

`DELETE` sets `deleted_at = now()`. The row survives. Public visibility ends
immediately because `deleted_at IS NULL` is part of the visibility predicate.

### Alternatives considered

**Hard delete (`DELETE FROM jobs`).** Would have been better at: simplicity —
every query gets shorter, and there is no way to forget a filter; a table that
does not grow forever; unambiguous semantics, since a deleted row is gone and
nobody has to reason about a second kind of existence; a plain `UNIQUE (slug)`
constraint instead of a partial index; and trivially correct data-protection
compliance, because "delete my data" means what it says.

Rejected because it is irreversible against a live database with no undo. The
recovery story for a mis-click would be "restore a database backup", which for a
single job row is absurd, and on a free-tier database with no configured backup
policy is "the content is gone".

**A separate `deleted_jobs` archive table.** Would have been better at: keeping
the live table clean and the queries simple, with no `deleted_at` filter anywhere.
Rejected because moving a row between tables in a transaction, and moving it back
on restore, is more machinery and more failure modes than one nullable timestamp
— and any future foreign key to `jobs` would break on the move.

**Soft delete plus a restore endpoint in the API.** Deliberately not built.
Recovery is a documented SQL procedure
([DATABASE.md](DATABASE.md#recovering-a-row)) because restoring a job should be
rare, deliberate, and involve checking whether its slug has since been taken. An
API endpoint invites a "Trash" UI, which is a feature nobody asked for.

### Consequences

- **Cost:** the table grows monotonically. Irrelevant at this scale; a retention
  policy is listed in [DATABASE.md](DATABASE.md#12-future-migrations).
- **Operational:** recovery is `UPDATE jobs SET deleted_at = NULL`, after
  checking the slug is free.
- **The real cost — every query must filter on `deleted_at`.** A forgotten filter
  resurrects deleted content, and no error is raised. The mitigation is
  structural rather than a convention: the filter is inside the visibility
  predicate in the repository layer, so the public queries cannot omit it. A
  future admin query is where the risk actually lives.
- **Second-order effect:** because a soft-deleted row keeps its slug, slug
  uniqueness has to be a **partial** unique index (`WHERE deleted_at IS NULL`),
  or a deleted job would reserve its URL forever. That is
  [DATABASE.md](DATABASE.md#5-slug-uniqueness), and it is a consequence of this
  decision rather than an independent one.
- **Data protection:** "delete" here means "hidden and recoverable", not "erased".
  If a genuine erasure request ever applies, hard-deleting the row is the
  correct response and soft delete is not sufficient. Worth knowing before
  someone assumes the button is enough.

---

## D-19 — Sessions in Postgres rather than a JWT

### Context

Authenticate a small number of administrators from a browser.

> **Amended after the domain was chosen.** This decision was written assuming
> requests from `jobpaglu.com` to `api.jobpaglu.com` — cross-origin but
> same-site. The deployed domain is `home.jobpaglu.eu.cc`, whose registrable
> domain per the Public Suffix List is `eu.cc`, shared with strangers. The
> topology is therefore **single-origin** (browser → site's own `/api/*` proxy →
> Cloud Run), and cookies are **first-party** with a `__Host-` name prefix.
> Nothing below about session storage changes; only the origin story does. See
> [SECURITY.md](SECURITY.md#the-eucc-shared-suffix-problem) and
> [DEPLOYMENT.md](DEPLOYMENT.md#5-the-single-origin-topology).

### Decision

Opaque 32-byte random session tokens in an `HttpOnly; Secure; SameSite=Lax`
cookie. The server stores only `sha256(token)` in a `sessions` table. Idle expiry
12h, absolute expiry 7 days, both enforced server-side. Rotated on login.

### Alternatives considered

**A stateless JWT.** The default modern instinct, and it would have been better
at:

| JWT advantage | Why it is real |
|---|---|
| No database read for auth | Signature verification is local. On a scale-to-zero backend, one fewer query in the request path is a genuine latency win, especially on the first request after a resume |
| Horizontal scaling | Any instance validates any token with no shared state. Not a concern at four instances, but it is the reason JWTs exist |
| No session table | One less table, no expiry sweep, no cleanup job |
| Portability | The same token works across services without a shared session store — the right answer for a multi-service architecture |

**Rejected on one property that outweighs all of the above here: revocation.**

A JWT is valid until it expires, because validity is a mathematical property of
the token rather than a fact in a database. There is no "sign this person out
now". The standard fix is a denylist of revoked tokens — checked on every
request — which reintroduces exactly the per-request state lookup the JWT was
adopted to avoid, *plus* a second mechanism to maintain. You end up with the
statelessness of a session store and the ergonomics of a JWT, which is the wrong
way round.

For a single-admin content system, revocation is the security control that
matters most. If an admin's laptop is stolen, the required response is "invalidate
that session, immediately, with certainty". With a session table that is
`DELETE FROM sessions WHERE ...`. With a JWT it is "wait up to the token's
lifetime", or "shorten the lifetime and accept re-authentication churn", or
"build the denylist".

The secondary advantages of the session table are not small either:

| Property | Session table | JWT |
|---|---|---|
| Immediate revocation | `DELETE` | Not possible without a denylist |
| Sign out everywhere | `DELETE WHERE admin_user_id = ...` | Rotate a global secret, signing out everyone |
| Idle **and** absolute expiry | Two columns, both checked server-side | Needs re-issuance logic, or a refresh-token pair — which is a session store with extra steps |
| Value of a database leak | Only `sha256(token)`. Nothing usable | The signing secret, if it leaks, mints valid tokens for every account |
| Visibility | `SELECT * FROM sessions` shows who is signed in | No server-side view of active sessions exists |
| Size on the wire | 43 characters | Hundreds of bytes on every request |

**Session data in a signed cookie (no server state).** A middle path. Rejected
for the same revocation reason, and because it puts session state in a place the
client holds.

**A third-party auth provider (Auth0, Clerk, Supabase Auth).** Would have been
better at: 2FA, password reset, and magic links out of the box — and 2FA is a
named gap in [SECURITY.md](SECURITY.md#not-yet-done). Rejected because it is a
paid dependency and a third party in the auth path for **one admin account**, and
because the free tiers of these services have historically been the first thing
to change.

### Consequences

- **Performance:** one indexed lookup per admin request, on a table with a
  handful of rows. It is free in any measurable sense, and it happens only on
  `/api/admin/*` — public traffic, which is all the traffic, never touches it.
- **Availability:** if Postgres is down, admins cannot sign in. Since the admin
  dashboard exists to edit rows in Postgres, this costs nothing real.
- **Operational:** expired rows need occasional cleanup
  (`DELETE FROM sessions WHERE expires_at < now()`). Not automated today; the
  rows are already refused by the middleware, so it is housekeeping.
- **Security upside:** storing only the hash means a database dump yields no
  usable session. There is nothing to reverse.
- **When to revisit:** if Job Paglu ever adds job-seeker accounts with hundreds
  of thousands of sessions, or a second service that needs to validate the same
  credential. Both change the arithmetic. Neither is on the roadmap.

---

## D-20 — Vercel for both the frontend and the backend

**Accepted 2026-08-22. Supersedes [D-10](#d-10--cloudflare-pages-for-the-frontend)
(frontend host) and [D-11](#d-11--google-cloud-run-for-the-backend) (backend
host). Amends [D-14](#d-14--docker-for-local-dev-and-the-cloud-run-image-only).
Does not affect [D-12](#d-12--neon-for-postgres): the database stays on Neon.**

### Context

D-10 and D-11 gave the best free-tier fit available at the time, and they were
right on their own terms. What they also gave was **three provider accounts,
three dashboards, two deployment mechanisms and two deploy credentials** for a
project whose entire deliverable is a job board with one administrator.

Three things changed the weighting:

1. **The user asked for free hosting on Vercel, as directly as possible: one
   platform, minimal moving parts, no paid infrastructure.** That is a stated
   requirement, not a preference to be optimised away.
2. **Two API tokens had already leaked into a chat transcript** during this
   project. That is a factual, repeated failure of a design that requires a
   deploy credential to exist at all.
3. **Vercel supports Go natively.** A `go.mod` at the project root plus
   `cmd/server/main.go` and `"framework": "go"` runs the existing server
   unchanged. The backend needed no restructuring — which removes D-11's
   argument for a container ("the same image runs everywhere") from the cost
   column, because the same *source* now runs everywhere.

### Decision

Two Vercel projects from one repository, plus Neon.

| Project | Root Directory | Framework | Serves |
|---|---|---|---|
| `job-paglu-web` | `frontend` | Astro (auto-detected) | `home.jobpaglu.eu.cc` |
| `job-paglu-api` | `backend` | `go` (via `backend/vercel.json`) | an internal `*.vercel.app` URL |

Deployed by Vercel's **Git integration**, so no API token exists anywhere.
`/api/*` is a `vercel.json` rewrite from the web project to the API project,
which preserves the single-origin cookie design exactly. `@astrojs/cloudflare`
is replaced by `@astrojs/vercel`; `frontend/wrangler.toml` is deleted;
`backend/Dockerfile` is kept for local development and portability.

Two projects rather than one because combining a Go server and a frontend
framework in a single project requires Vercel **Services**, whose free-tier
status is unverified. A deployment that depends on an unverified platform
feature fails on someone else's roadmap.

### Alternatives considered

**Staying on Cloudflare Pages + Cloud Run.** This is the real alternative, and
it was genuinely better at four things — none of them cosmetic:

| The old stack was better at | Why it matters |
|---|---|
| **Unmetered bandwidth** on Pages | The upside case for a job board is a listing going viral. On Pages that is a good day; on a metered plan it is a billing event. This is the strongest single argument against the change |
| **No non-commercial licensing restriction** | Vercel's Hobby plan is for non-commercial use. Job Paglu's own popup says the service may become paid in future, so monetisation forces a paid plan. Pages had no such term. See [DEPLOYMENT.md section 10](DEPLOYMENT.md#10-the-hobby-plan-licensing-caveat) |
| **A long-lived process** on Cloud Run | Two controls here were written for one and are weaker without it: the in-process login rate limiter (per instance, so the effective limit multiplies by an instance count nothing here sets) and `RUN_MIGRATIONS_ON_START` (safe under the advisory lock, but wrong on every cold start). The background session sweeper is in the same category, though it is only housekeeping |
| **A bounded instance count** (`--max-instances 4`) | That flag was simultaneously a spend guard and a *database connection* guard. Serverless has no equivalent, so the pool default drops to 2 and Neon's pooled endpoint becomes mandatory rather than merely advisable |

**Vercel Services (one project).** Would be better: one project, one URL, no
rewrite. Rejected because its free-tier availability is unverified.

**Frontend on Vercel, backend left on Cloud Run.** Would keep the long-lived
process and its two controls. Rejected because it keeps two platforms, two
dashboards and the `gcloud` onboarding — most of what the user asked to remove
— while adding a cross-provider hop to every SSR read.

**Fly.io / Render for the backend.** Already rejected in D-11, and nothing has
changed to reopen them.

### Consequences

**What improves**

- **One platform, one dashboard, one mental model.** Both deployments come from
  the same push, and preview deployments exist for both.
- **No deploy credential.** Nothing to create, store, rotate, or leak. Given
  this project's actual history, that is the single largest security improvement
  in the change.
- **Instant rollback**, per project, as a button.
- **Go is a first-class runtime**, so the API needed no code restructuring and
  no container build in the deploy path.
- **The frontend finally sends security headers.** It sent none before —
  `frontend/vercel.json` adds CSP, HSTS, `nosniff`, `Referrer-Policy`,
  `X-Frame-Options` and `Permissions-Policy`, scoped to exclude `/api/*` so they
  do not duplicate the API's own.
- **Fewer moving parts in the request path.** The `/api/*` rewrite is handled at
  the routing layer and costs no function invocation. The Astro proxy route did.

**What gets worse, stated plainly**

- **Bandwidth is metered.** Traffic growth is now a billing consideration.
- **The Hobby plan is non-commercial.** Monetisation requires a paid plan.
- **The login rate limiter is weaker.** Per-instance counters with an unbounded
  instance count. Documented in [SECURITY.md section 6](SECURITY.md#6-login-hardening);
  the honest fix is a shared store.
- **Function duration is shorter than Cloud Run's 30 s.** Four budgets were
  re-tuned to 8 s / 9 s / 10 s / 10 s so a slow request produces a documented
  envelope rather than a platform error page. See
  [DEPLOYMENT.md section 9](DEPLOYMENT.md#9-timeouts-and-the-function-duration-limit).
- **Migrations became a separate step.** `adminctl migrate` plus
  `.github/workflows/migrate.yml`. Better practice, and one more thing to
  remember at deploy time.
- **CI is no longer even nominally a deploy gate.** Vercel builds on push. The
  fix is branch protection or an Ignored Build Step, and it has to be configured
  deliberately — D-13's "deployment only after checks pass" is not automatic.

**Reversibility.** Deliberately preserved. `backend/Dockerfile` still builds the
same image, the Astro `/api/*` proxy route is still in the tree as the
platform-independent fallback, and no Go package moved. Returning to a container
host is a configuration change, not a rewrite. That is the mitigation for every
"what gets worse" row above.

**When to revisit.** The moment Job Paglu monetises (licensing), or the moment
bandwidth or login-attempt volume stops being negligible (metering, and the rate
limiter). Both are foreseeable and neither is silent.

---

## D-21 — Cloudflare only: Astro API routes + D1 + Cloudflare Access

**Status:** ACCEPTED 2026-08-22 (branch `cloudflare-only`). Supersedes **D-20**
(Vercel), which superseded D-10/D-11 (Cloudflare Pages + Cloud Run). Both earlier
records are kept verbatim above.

### Context

The requirement was "free forever, commercially usable, as few platforms as
possible". Research settled two facts that decided it:

- **Vercel's Hobby plan prohibits commercial use.** Ads, payments, affiliate
  links and client work all require Pro. A job board monetises through exactly
  those routes, and the product's own popup says the service may become paid. So
  D-20 was a licensing landmine, not a free tier.
- **Railway has no free tier** since July 2023 — a $5 trial credit that expires,
  then $1/month of credit, which cannot keep anything always-on.

Cloudflare Pages, by contrast, permits commercial use on the free plan and has
unmetered bandwidth.

### Decision

One Cloudflare Pages project holds everything: the Astro site, the API as Astro
routes under `src/pages/api/`, and D1 as the database. Admin authentication is
Cloudflare Access.

### Alternatives considered

| Option | Why not |
|---|---|
| Keep Go on Cloud Run + Pages + Neon | Works and needs no rewrite, but it is three providers and three dashboards. Genuinely better at: keeping the Go implementation, a long-lived process where an in-process rate limiter and start-up migrations both work, and no non-commercial licensing clause anywhere. |
| Vercel (D-20) | Commercial use prohibited on the free plan. |
| Oracle Always Free VM | Genuinely free and runs everything, but the ARM allowance was **halved** on 2026-06-15 and instances over the new limit were terminated from 2026-08-18, and Oracle reclaims instances idling under 10% CPU/network for 7 days — which is precisely a new job board's profile. |
| Cloudflare + keep Go via TinyGo/WASM | Not viable. `pgx` needs raw TCP and the full stdlib; neither survives the Workers runtime. |

### Consequences

**Cost.** $0 on the free tier, commercially usable. Workers free: 10 ms CPU per
invocation, 100k requests/day, 50 subrequests/request. D1 free: 5 GB, 5M
row-reads/day, 100k row-writes/day. Pages static asset requests are unmetered
and do not count against the 100k.

**The Go backend is not deployed.** 49 files retained in `backend/` as the
reference implementation. This contradicts the original spec §2, which mandated
Go — an explicit, user-approved trade. Two things made the rewrite far smaller
than it sounds: the apply-URL allowlist and slug derivation already existed in
TypeScript with passing tests, so the security-critical logic was already ported.

**Password auth is gone, and it had to be.** The Workers free plan allows 10 ms
CPU per invocation; Argon2id at 64 MiB/t=3 needs 50-150 ms. A password hash
cannot run there. Access authenticates at the edge instead, which also deletes
the sessions table, session rotation, CSRF token issuance and login rate
limiting. Zero Trust is free for 50 users.

**CSRF is still required.** Access authenticates with the `CF_Authorization`
cookie, which browsers send cross-site, so `security.checkOrigin: true` is now
the CSRF control. An earlier draft of this design wrongly concluded CSRF was
inapplicable "because we set no cookie" — that reasoning is recorded here because
it is an easy and dangerous mistake to repeat.

**Postgres to SQLite.** Timestamps become RFC 3339 `Z` TEXT (the contract already
mandated that format, so the wire output is unchanged); booleans become
`INTEGER` with a `CHECK`, converted at the repository boundary so 0/1 never
reaches JSON; `pg_trgm` is dropped for `LIKE`. The partial unique index
`WHERE deleted_at IS NULL` survives intact, so a soft-deleted job still does not
reserve its slug.

**Rate limiting on login no longer exists** because login no longer exists. Access
owns that surface now.

### Verification

Tested on the real `workerd` runtime with real local D1, on a disposable EC2 host
(`projects/2026-08-22-cloudflare-only-e2e`): **`astro check` 0 errors / 84 files,
272 unit tests, and 76/76 end-to-end checks across two configurations** — 57
behavioural, and 19 security checks confirming every admin route 401s and that
`alg:none` and HS256-downgrade forgeries are rejected.
