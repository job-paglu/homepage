# Deployment — Cloudflare only

**Target:** `https://home.jobpaglu.eu.cc`
**Decision record:** [DECISIONS.md](DECISIONS.md) **D-21**
**Status when this was written:** nothing is deployed. `home.jobpaglu.eu.cc` is
NXDOMAIN. The code is verified on the real Workers runtime (see *Verification
already done*) but has never been deployed.

---

## What this deploys

**One** Cloudflare Pages project holding the whole product:

```
ONE Pages project
├── Astro SSR site            /, /jobs, /jobs/:slug, /404, /privacy, /terms
├── API as Astro routes       /api/**           (same origin — no proxy, no CORS)
├── D1                        locals.runtime.env.DB
└── Cloudflare Access         guards /admin*    (at the edge, before our code)
```

No second service, no second provider, no database host to sign up for.

**There is no deploy credential in the recommended path.** Pages builds from Git.
The one-off `wrangler` commands use `wrangler login` (browser OAuth). Nothing in
this document asks you to create, paste or store an API token — and after two
tokens were disclosed into a chat transcript during this project, that is a
feature, not an aesthetic preference.

---

## Prerequisites

| | |
|---|---|
| A Cloudflare account | free plan is sufficient |
| The `jobpaglu.eu.cc` zone | **already delegated to Cloudflare** — `nina.ns.cloudflare.com`, `trey.ns.cloudflare.com`. Confirm it appears in your dashboard; if it does, step 7 is two clicks. |
| A GitHub repo | `job-paglu/homepage` |
| Node 22+ locally | only for the two one-off `wrangler` commands |

**Note:** `https://jobpaglu.eu.cc` already serves something (HTTP 200, ~10 KB).
Deploying to the `home.` subdomain does not touch the apex.

---

## 1. Push the code to GitHub

`job-paglu/` is self-contained — it has its own `.gitignore`, `.gitattributes`
and `.github/` — so it can be a repository root as-is.

```bash
cd job-paglu
git init -b main
git add -A
git commit -m "Job Paglu: Cloudflare-only (Astro + D1 + Access)"
git remote add origin git@github.com:job-paglu/homepage.git
git push -u origin main
```

Verify before pushing that no secret is included. `.gitignore` already excludes
`.dev.vars` and `.env`; `.dev.vars.example` and `.env.example` hold placeholders
only and are meant to be tracked.

---

## 2. Create the D1 database

```bash
cd frontend
npx wrangler login                  # browser OAuth — no token
npx wrangler d1 create job-paglu
```

It prints a `database_id`. Put it into **`frontend/wrangler.toml`**, replacing
**both** placeholders:

- line ~38 — `[[d1_databases]]` (production)
- line ~46 — `[[env.preview.d1_databases]]` (preview)

```toml
database_id = "your-real-id-here"
```

Use a **second** database for preview if you want isolation. Sharing one is the
classic way a preview branch's migration lands on live data — the config is split
for that reason.

---

## 3. Apply migrations to the real database

```bash
npx wrangler d1 migrations apply job-paglu --remote
```

Applies `0001_init.sql` then `0002_seed_dev.sql`.

**`0002` will report as applied and insert zero rows.** Every statement in it is
gated on an `app_flags` row that only exists if you deliberately opt in. That is
intended: `wrangler` applies every file in `migrations/` and SQL cannot read
`APP_ENV`, so the guard is in the SQL rather than in a comment nobody enforces.
Do not "fix" it.

Confirm the schema landed:

```bash
npx wrangler d1 execute job-paglu --remote --command "SELECT name FROM sqlite_master WHERE type='table'"
```

Expect `jobs`, `app_flags`, `d1_migrations`.

---

## 4. Create the Pages project

Dashboard → **Workers & Pages** → **Create** → **Pages** → **Connect to Git** →
select `job-paglu/homepage`.

| Setting | Value |
|---|---|
| Production branch | `main` |
| Framework preset | Astro |
| Build command | `npm run build` |
| Build output directory | `dist` |
| **Root directory** | **`frontend`** |
| Environment variable | **`NODE_VERSION` = `22`** |

Two of those are the ones people miss, and neither fails in an obvious way:

- **Root directory `frontend`.** The repo is a monorepo; `backend/` is the
  retained Go reference implementation and is not deployed. Without this, the
  build finds no `package.json`.
- **`NODE_VERSION=22`.** `package.json` declares `>=22.19.0` (a transitive
  dependency requires it) and Pages defaults to an older Node.

**Do not add the D1 binding in the dashboard.** It comes from `wrangler.toml`,
which is the single source of truth. Two places declaring one binding is how they
drift.

---

## 5. Create the Access application

Dashboard → **Zero Trust** → **Access** → **Applications** → **Add an
application** → **Self-hosted**.

| Setting | Value |
|---|---|
| Application name | `Job Paglu admin` |
| Session duration | your preference |
| Application domain | `home.jobpaglu.eu.cc` |
| **Path** | **`admin*`** |
| Policy | Action **Allow** → include **Emails** → your address |

### Protect `admin*` only — not `/api/*`

This is deliberate and it matters.

Once you pass Access on `/admin`, Cloudflare sets the `CF_Authorization` cookie
for the whole hostname. `src/lib/server/access.ts` reads the
`Cf-Access-Jwt-Assertion` header **and falls back to that cookie**, so
`/api/admin/*` authenticates correctly without being behind Access itself.

If you also protected `/api/*`, an unauthenticated API call would receive
Access's **HTML login challenge** instead of the application's JSON `401`. The
admin dashboard's `fetch` cannot render that, so a signed-out state would look
like a broken app rather than "please sign in".

Then collect two values:

| Value | Where |
|---|---|
| **AUD tag** | the application's **Overview** tab |
| **Team domain** | Zero Trust → **Settings** → e.g. `yourteam.cloudflareaccess.com` |

Neither is a secret — the JWKS endpoint is public and the AUD is a public
application identifier. Both must nonetheless be **correct**: `aud` is what stops
a token minted for a *different* application in the same Access team from
authenticating here.

**Free tier: 50 users**, indefinitely. Past 50 active seats, further users are
blocked until you upgrade.

---

## 6. Fill in the remaining placeholders

In `frontend/wrangler.toml`, replace all four:

```toml
[vars]
ACCESS_TEAM_DOMAIN = "yourteam.cloudflareaccess.com"
ACCESS_AUD         = "<your AUD tag>"

[env.preview.vars]
ACCESS_TEAM_DOMAIN = "yourteam.cloudflareaccess.com"
ACCESS_AUD         = "<preview app AUD, or the same one>"
```

Leave `APP_ENV = "production"` and `ALLOW_HTTP_APPLY_URLS = "false"` alone.

`ACCESS_DEV_BYPASS_EMAIL` is **deliberately absent** from `wrangler.toml`. The
bypass is gated on `APP_ENV === 'development'` *and* that variable, so with
`APP_ENV=production` it is never even read. Its absence is documentation: nobody
should read its presence as "the bypass is available in production". This is
verified by test — see *Verification already done*.

```bash
git add frontend/wrangler.toml
git commit -m "Add real D1 id and Access configuration"
git push
```

Pages rebuilds on push.

---

## 7. Attach the custom domain

Because `jobpaglu.eu.cc` is **already on Cloudflare nameservers**, this is
simple: Pages project → **Custom domains** → **Set up a custom domain** →
`home.jobpaglu.eu.cc` → **Activate**.

Cloudflare creates the DNS record itself and issues the certificate. No record to
add by hand, no CNAME validation, and no Let's Encrypt rate-limit exposure (which
*would* have applied on `eu.cc` had the zone been hosted elsewhere — see
SECURITY.md on the shared-suffix problem, which still affects cookie scope
regardless of who hosts DNS).

---

## 8. Verify — the end-to-end acceptance test

```bash
# 1. the site responds
curl -sI https://home.jobpaglu.eu.cc | head -1                    # 200

# 2. the API is same-origin and reachable
curl -s https://home.jobpaglu.eu.cc/api/health                    # {"data":{"status":"ok"}}
curl -s https://home.jobpaglu.eu.cc/api/ready                     # {"data":{"status":"ready"}}  <- proves D1 is bound
curl -s https://home.jobpaglu.eu.cc/api/jobs                      # {"data":[],"meta":{...}}

# 3. the admin API is sealed
curl -so /dev/null -w '%{http_code}\n' https://home.jobpaglu.eu.cc/api/admin/me   # 401

# 4. a forged token is refused
curl -so /dev/null -w '%{http_code}\n' \
  -H 'Cf-Access-Jwt-Assertion: not-a-jwt' \
  https://home.jobpaglu.eu.cc/api/admin/me                        # 401
```

`/api/ready` returning `200` is the one that proves the D1 binding resolved —
`/health` deliberately does not touch the database, so it passes even with D1
misconfigured.

Then in a browser:

1. Open `/admin/jobs` → Access challenges you → sign in → the dashboard loads.
2. **Create a job.** Title `Senior Cloud Engineer`, a real `https://` apply URL.
3. Confirm the slug shows as `senior-cloud-engineer` and status is `draft`.
4. Confirm it is **not** on `/jobs` yet.
5. **Publish it.**
6. Open `/jobs/senior-cloud-engineer` → it renders.
7. **Click Apply Now** → you land on the exact URL you typed, unchanged.
8. Unpublish → `/jobs/senior-cloud-engineer` returns a real 404 immediately.

Step 7 is the acceptance criterion from the original request. Step 8 catches the
most common publishing bug.

---

## Free-tier limits worth knowing

| | Free plan |
|---|---|
| Workers CPU | **10 ms per invocation** |
| Workers requests | 100,000/day (static assets are unmetered and excluded) |
| Subrequests | 50 per request |
| D1 storage | 5 GB |
| D1 reads / writes | 5M / 100k rows per day |
| Pages builds | 500/month |
| Access users | 50 |
| Commercial use | **permitted** |

The 10 ms CPU cap is why admin auth is Access rather than a password: Argon2id
needs 50–150 ms of CPU and cannot run there. CPU time excludes waiting on D1, so
the actual handlers sit far below the cap.

---

## Troubleshooting, in the order things actually go wrong

| Symptom | Cause | Fix |
|---|---|---|
| Build fails, no `package.json` found | Root directory not set | Set it to `frontend` |
| Build fails on an engine/version error | Pages default Node is too old | Set `NODE_VERSION=22` |
| `/api/ready` returns 503 while `/health` is 200 | D1 not bound, or `database_id` is still the zero placeholder | Put the real id in `wrangler.toml`, commit, redeploy |
| Every `/api/admin/*` call is 401 even after signing in | Access app path is wrong, or `ACCESS_AUD` / `ACCESS_TEAM_DOMAIN` mismatch | Path must be `admin*`; re-copy the AUD from the app Overview |
| Signing in shows raw HTML in the dashboard instead of data | Access is protecting `/api/*` too | Restrict the application to `admin*` |
| Mutations return 403 from a script | Astro's `checkOrigin` — a state-changing request needs a matching `Origin` | Send `Origin`, or use a JSON content type. Browsers do this automatically. |
| Apply Now renders disabled | The stored `apply_url` failed the scheme allowlist | Only `https://` is accepted (`http://` requires `ALLOW_HTTP_APPLY_URLS=true`) |
| A published job is missing from `/jobs` | Visibility predicate | Check `status`, `deleted_at`, `expires_at` |
| Custom domain stuck "pending" | Zone not in this Cloudflare account | Confirm `jobpaglu.eu.cc` is in your dashboard |

---

## Rollback

Pages keeps every deployment. Project → **Deployments** → pick a previous
one → **Rollback**. Instant, no rebuild.

**A rollback does not undo a migration.** D1 migrations are forward-only, so write
them additively: add a nullable column rather than renaming or dropping one, so
the previous build still runs against the new schema.

---

## Teardown

1. Pages project → Settings → **Delete project**.
2. `npx wrangler d1 delete job-paglu`
3. Zero Trust → Access → delete the application.
4. Remove the `home` DNS record if Cloudflare left it.

**None of this is in AWS.** The workspace's `verify-cleanup.ps1` has nothing to
sweep for this project, and that is the correct result rather than an oversight.

---

## Verification already done

Tested on the **real `workerd` runtime** with real local D1, on a disposable EC2
host (`projects/2026-08-22-cloudflare-only-e2e`) — not a mock:

| | |
|---|---|
| `astro check` | 0 errors, 84 files |
| `vitest` | 272 tests |
| `astro build` | succeeds, `_worker.js` emitted |
| E2E behaviour | **57 / 57** |
| E2E security | **19 / 19** |

The security suite ran with `APP_ENV=production` and confirmed: all 8 admin
routes return `401` without a verified token; `alg:none` and HS256-downgrade
forgeries are rejected; and the dev bypass is **inert in production even with
`ACCESS_DEV_BYPASS_EMAIL` set**.

**What has never been tested:** a real Cloudflare deployment, a real Access token
end to end (local runs use the dev bypass), and the custom domain. Those need the
steps above.
</content>
