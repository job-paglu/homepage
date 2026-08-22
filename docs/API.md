# API guide

> **[API-CONTRACT.md](API-CONTRACT.md) is normative.** It defines the exact
> shapes, status codes, validation rules and headers, and both the Go backend
> and the Astro frontend are written against it. If this guide and the contract
> disagree, **the contract is right and this file is a bug** — fix it here.
>
> This document is the tutorial: how to actually call the thing, what to do when
> it says no, and what to change when you add a field.

Every host below is a placeholder and every credential is obviously fake. Never
paste a real token, password or connection string into a shell command you will
keep, an issue, or a document.

---

## Contents

1. [Conventions you need before the first call](#1-conventions)
2. [Endpoint reference](#2-endpoint-reference)
3. [Public endpoints, with curl](#3-public-endpoints)
4. [Admin session: logging in](#4-admin-session)
5. [Admin mutations, with curl](#5-admin-mutations)
6. [Error codes and what a client should do](#6-error-codes-and-what-to-do-about-them)
7. [Pagination](#7-pagination)
8. [Caching](#8-caching)
9. [Walkthrough: adding a field to Job](#9-walkthrough-adding-a-field-to-job)

Set this once per shell and every example below works:

```bash
API=http://localhost:8080          # or https://api.jobpaglu.com
```

```powershell
$API = 'http://localhost:8080'     # or https://api.jobpaglu.com
```

---

## 1. Conventions

| Rule | Detail |
|---|---|
| Base path | `/api`. The health endpoints are at the root: `/health`, `/ready` |
| Content type | `application/json; charset=utf-8` both ways |
| Max body | **64 KiB**. Larger is `413 PAYLOAD_TOO_LARGE` |
| Timestamps | RFC 3339, UTC, `Z` suffix: `2026-08-21T09:41:07Z`. The API never emits a local offset. Humanising a date is the client's job |
| Identifiers | UUID v4 strings. **Public URLs use `slug`; admin URLs use `id`** |
| Unknown request fields | Ignored, not rejected. So a newer client can talk to an older server |
| Unknown enum values in a response | Must be tolerated. Display them verbatim rather than crashing — `lib/types.ts` models this with `LooseEnum` |
| `X-Request-Id` | On every response, in the server log line, and in `error.request_id`. Quote it in a bug report |

### Success envelope

A single resource:

```json
{ "data": { "id": "...", "slug": "...", "title": "..." } }
```

A collection:

```json
{
  "data": [ { "id": "...", "slug": "..." } ],
  "meta": { "page": 1, "per_page": 20, "total": 42, "total_pages": 3 }
}
```

**A bare array is never returned.** That is not decoration: it means `meta` can
gain a field, or a collection can gain a sibling key, without breaking a single
client. Adding a key to an object is compatible; changing an array into an
object is not.

### Error envelope

```json
{
  "error": {
    "code": "VALIDATION_FAILED",
    "message": "One or more fields are invalid.",
    "details": { "apply_url": "Must be an absolute https:// URL." },
    "request_id": "01J8ZQ4M7T"
  }
}
```

- **Switch on `code`, never on `message`.** `code` is a closed set and is part
  of the contract. `message` is human-facing prose and may be reworded at any
  time.
- `message` is safe to show a user. It never contains SQL, a file path, a stack
  frame or an internal hostname.
- `details` appears **only** for `VALIDATION_FAILED`, mapping field name to one
  human-readable message. All invalid fields are returned at once, not one per
  round trip.
- `204` responses have no body at all — do not try to parse one.

The frontend implements all of this once, in `frontend/src/lib/api.ts`:
`apiFetch` normalises every failure — including a transport failure, which
becomes the client-side-only code `NETWORK_ERROR` — into a single `ApiError`
carrying `code`, `status`, `details`, `requestId`, `retryAfterSeconds` and the
`isRetryable` / `isNotFound` / `isUnauthorized` predicates. UI code branches on
those and never touches a raw `Response`.

---

## 2. Endpoint reference

| Method | Path | Auth | Success | Cache-Control |
|---|---|---|---|---|
| `GET` | `/api/jobs` | public | `200` `{data: JobSummary[], meta}` | `public, max-age=0, s-maxage=60, stale-while-revalidate=300` |
| `GET` | `/api/jobs/{slug}` | public | `200` `{data: Job}` | `public, max-age=0, s-maxage=300, stale-while-revalidate=600` |
| `GET` | `/health` | public | `200` while the process is alive | not cached |
| `GET` | `/ready` | public | `200` ready / `503` degraded | not cached |
| `POST` | `/api/admin/login` | public, rate-limited | `200` + `Set-Cookie` | `no-store` |
| `POST` | `/api/admin/logout` | session | `204` | `no-store` |
| `GET` | `/api/admin/me` | session | `200` `{data:{admin, csrf_token}}` | `no-store` |
| `GET` | `/api/admin/jobs` | session | `200`, all statuses | `no-store` |
| `GET` | `/api/admin/jobs/{id}` | session | `200` `{data: Job}` | `no-store` |
| `POST` | `/api/admin/jobs` | session + CSRF | `201` + `Location` | `no-store` |
| `PUT` | `/api/admin/jobs/{id}` | session + CSRF | `200` | `no-store` |
| `DELETE` | `/api/admin/jobs/{id}` | session + CSRF | `204` (soft delete) | `no-store` |
| `POST` | `/api/admin/jobs/{id}/publish` | session + CSRF | `200` | `no-store` |
| `POST` | `/api/admin/jobs/{id}/unpublish` | session + CSRF | `200` | `no-store` |

"session + CSRF" means: send the cookie **and** the `X-CSRF-Token` header
**and** an `Origin` matching the server's `FRONTEND_URL`. All three. Missing any
one is `403 CSRF_INVALID`.

---

## 3. Public endpoints

### List jobs

```bash
curl -s "$API/api/jobs" | jq
```

Only published, non-deleted, non-expired jobs come back, ordered
`is_featured DESC, published_at DESC NULLS LAST, created_at DESC`.

With pagination:

```bash
curl -s "$API/api/jobs?page=2&per_page=10" | jq '.meta'
```

With filters — all of these are implemented server-side today, even though no UI
is wired to them yet:

```bash
# free-text across title, company, location and description
curl -s "$API/api/jobs?q=cloud+engineer" | jq '.data[].title'

# location substring, case-insensitive
curl -s "$API/api/jobs?location=berlin" | jq '.meta.total'

# exact country, case-insensitive
curl -s "$API/api/jobs?country=Germany" | jq '.meta.total'

# enumerated filters
curl -s "$API/api/jobs?job_type=full-time&remote_type=hybrid" | jq '.meta.total'

# featured only
curl -s "$API/api/jobs?featured=true" | jq '.data[].title'

# everything at once
curl -s "$API/api/jobs?q=engineer&country=Germany&job_type=full-time&featured=true&page=1&per_page=5" | jq
```

Parameter rules:

| Param | Default | Behaviour on a bad value |
|---|---|---|
| `page` | `1` | Below 1 is clamped to 1. Non-numeric is `400 BAD_REQUEST` |
| `per_page` | `20` | Clamped to `1..50` (`1..100` on the admin list) |
| `q` | — | Max 100 chars |
| `location` | — | Case-insensitive substring |
| `country` | — | Case-insensitive exact match |
| `job_type` | — | Not a `JobType` member is `400 BAD_REQUEST` |
| `remote_type` | — | Not a `RemoteType` member is `400 BAD_REQUEST` |
| `featured` | — | Anything but `true`/`false` is `400 BAD_REQUEST` |

Note the split: an out-of-range *number* is clamped, an unknown *enum member* is
an error. Clamping a page number is forgiving with no ambiguity about intent;
silently ignoring `job_type=fulltime` would return "all jobs" to a caller who
asked for a subset, which looks like a data problem rather than a typo.

**An empty result is `200` with `data: []` and `total: 0` — never `404`.** A
query that matched nothing is a successful query. This is why the frontend has
distinct `EmptyState` and `ErrorState` components.

Inspect the headers:

```bash
curl -sD - -o /dev/null "$API/api/jobs"
```

```powershell
curl.exe -sD - -o NUL "$API/api/jobs"
```

### Get one job

```bash
curl -s "$API/api/jobs/senior-cloud-engineer" | jq '.data.title, .data.apply_url'
```

`404 JOB_NOT_FOUND` when the slug does not exist **or** the job is `draft`,
`unpublished`, soft-deleted, or expired. The endpoint cannot distinguish those
cases and that is deliberate: if a draft returned `403` instead of `404`, the
existence of an unpublished slug would be discoverable by guessing.

```bash
curl -si "$API/api/jobs/this-does-not-exist" | head -1
curl -s  "$API/api/jobs/this-does-not-exist" | jq '.error.code'   # "JOB_NOT_FOUND"
```

### Health and readiness

```bash
curl -s "$API/health"    # {"data":{"status":"ok"}}       - never touches the DB
curl -s "$API/ready"     # {"data":{"status":"ready"}}    - or 503 with an error envelope
```

---

## 4. Admin session

Admin auth is a cookie plus a CSRF token. With `curl`, that means a **cookie
jar** — a file `curl` writes cookies to and reads them back from.

```bash
JAR=./.jp-cookies.txt        # add this to .gitignore; it holds a live session

curl -s -c "$JAR" \
  -X POST "$API/api/admin/login" \
  -H 'Content-Type: application/json' \
  -H "Origin: http://localhost:4321" \
  -d '{"email":"admin@example.com","password":"changeme-not-a-real-password"}' | jq
```

```powershell
$JAR = '.\.jp-cookies.txt'

curl.exe -s -c $JAR `
  -X POST "$API/api/admin/login" `
  -H 'Content-Type: application/json' `
  -H 'Origin: http://localhost:4321' `
  -d '{\"email\":\"admin@example.com\",\"password\":\"changeme-not-a-real-password\"}'
```

`-c` writes cookies; `-b` reads them. Every later call needs `-b "$JAR"`.

The response body:

```json
{
  "data": {
    "admin": {
      "id": "00000000-0000-4000-8000-000000000000",
      "email": "admin@example.com",
      "name": "Site Admin",
      "last_login_at": "2026-08-21T09:41:07Z"
    },
    "csrf_token": "EXAMPLE-CSRF-TOKEN-NOT-A-REAL-VALUE"
  }
}
```

Capture the token for later calls:

```bash
CSRF=$(curl -s -b "$JAR" "$API/api/admin/me" | jq -r '.data.csrf_token')
```

```powershell
$CSRF = (curl.exe -s -b $JAR "$API/api/admin/me" | ConvertFrom-Json).data.csrf_token
```

`GET /api/admin/me` is also the "am I still signed in?" call, and it is how the
dashboard recovers the CSRF token after a page reload without forcing a new
login.

Two cookies are set. Both matter:

Names are `APP_ENV`-derived: `__Host-` prefixed in production, plain in
development (contract section 5).

| Cookie | `HttpOnly`? | Why |
|---|---|---|
| `__Host-jp_session` (`jp_session` in dev) | **yes** | The credential. Script must never be able to read it |
| `__Host-jp_csrf` (`jp_csrf` in dev) | **no** | The client is *required* to read it and echo it in `X-CSRF-Token`. Making it `HttpOnly` would break the mechanism |

Login failures:

```bash
# wrong password OR unknown email - identical response, by design
curl -s -X POST "$API/api/admin/login" \
  -H 'Content-Type: application/json' -H "Origin: http://localhost:4321" \
  -d '{"email":"nobody@example.com","password":"wrong"}' | jq '.error.code'
# "INVALID_CREDENTIALS"
```

Six rapid failures from one IP trips the limiter:

```bash
for i in 1 2 3 4 5 6; do
  curl -s -o /dev/null -w '%{http_code}\n' -X POST "$API/api/admin/login" \
    -H 'Content-Type: application/json' -H "Origin: http://localhost:4321" \
    -d '{"email":"admin@example.com","password":"wrong"}'
done
# expect 401 401 401 401 401 429
```

The `429` carries `Retry-After` in seconds. A successful login clears the
counters. Logging in again also **rotates** the session token, so an old value
stops working — that is the anti-session-fixation control, not a bug.

Sign out:

```bash
curl -s -o /dev/null -w '%{http_code}\n' \
  -b "$JAR" -X POST "$API/api/admin/logout" \
  -H "X-CSRF-Token: $CSRF" -H "Origin: http://localhost:4321"
# 204, and idempotent: calling it again is still 204
```

---

## 5. Admin mutations

Every call below needs the jar, the CSRF header, and a matching `Origin`.

### List all jobs, any status

```bash
curl -s -b "$JAR" "$API/api/admin/jobs" | jq '.data[] | {slug, status}'

curl -s -b "$JAR" "$API/api/admin/jobs?status=draft" | jq '.meta'
curl -s -b "$JAR" "$API/api/admin/jobs?include_deleted=true&per_page=100" | jq '.meta.total'
```

Ordered `updated_at DESC` — the admin list is a worklist, so most-recently-touched
first. `per_page` clamps to `1..100` here. Soft-deleted rows are excluded unless
`include_deleted=true`.

### Create

```bash
curl -s -b "$JAR" \
  -X POST "$API/api/admin/jobs" \
  -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" \
  -H "Origin: http://localhost:4321" \
  -d '{
    "title": "Senior Cloud Engineer",
    "description": "Own the platform that serves millions of requests a day. You will work across Kubernetes, Terraform and a Go service mesh, and you will be on call one week in six.",
    "salary": "$140k - $175k",
    "qualification": "B.Sc. Computer Science or equivalent experience",
    "job_type": "full-time",
    "location": "Berlin, Germany",
    "apply_url": "https://example.com/careers/senior-cloud-engineer",
    "company": "Example GmbH",
    "country": "Germany",
    "remote_type": "hybrid",
    "experience_level": "5+ years",
    "category": "Engineering",
    "is_featured": false
  }' | jq '.data | {id, slug, status, published_at}'
```

`201` with a `Location: /api/admin/jobs/{id}` header. `status` defaults to
`draft`, so a create is never accidentally a publish. `slug` was omitted, so it
was derived from the title and de-duplicated — a derived slug never returns
`409`.

Capture the id:

```bash
JOB_ID=$(curl -s -b "$JAR" "$API/api/admin/jobs?status=draft&per_page=1" | jq -r '.data[0].id')
```

### Update

```bash
curl -s -b "$JAR" \
  -X PUT "$API/api/admin/jobs/$JOB_ID" \
  -H 'Content-Type: application/json' \
  -H "X-CSRF-Token: $CSRF" \
  -H "Origin: http://localhost:4321" \
  -d '{
    "title": "Staff Cloud Engineer",
    "description": "Own the platform that serves millions of requests a day. Updated scope: you will also lead the migration off the legacy queue.",
    "salary": "$160k - $195k",
    "qualification": "B.Sc. Computer Science or equivalent experience",
    "job_type": "full-time",
    "location": "Berlin, Germany",
    "apply_url": "https://example.com/careers/staff-cloud-engineer"
  }' | jq '.data | {slug, title}'
```

`PUT` is a **full replacement of the mutable fields**, so send every field you
want to keep. An omitted optional field is cleared, not preserved.

**One deliberate exception: the slug.** Omitting `slug` keeps the existing one
even though `title` changed. In the response above, `title` is
`"Staff Cloud Engineer"` and `slug` is still `senior-cloud-engineer`. That is
correct and important — a live URL must not change because someone fixed a
typo in a heading. Changing a slug takes an explicit `"slug": "..."` in the
body, and it is a decision with SEO and inbound-link consequences.

### Publish

```bash
curl -s -b "$JAR" \
  -X POST "$API/api/admin/jobs/$JOB_ID/publish" \
  -H "X-CSRF-Token: $CSRF" -H "Origin: http://localhost:4321" \
  | jq '.data | {status, published_at}'
```

Sets `status = 'published'`, and sets `published_at` **only if it was null** —
so re-publishing after an unpublish does not rewrite the original publication
date, and the job does not jump back to the top of the list.

Publish can fail validation even though the row already exists:

```json
{
  "error": {
    "code": "VALIDATION_FAILED",
    "message": "One or more fields are invalid.",
    "details": { "apply_url": "Must be an absolute https:// URL." },
    "request_id": "01J8ZQ4M7T"
  }
}
```

A job must not become publicly applicable without a usable apply link. That is
the one product invariant the API refuses to break for you.

### Unpublish

```bash
curl -s -b "$JAR" \
  -X POST "$API/api/admin/jobs/$JOB_ID/unpublish" \
  -H "X-CSRF-Token: $CSRF" -H "Origin: http://localhost:4321" \
  | jq '.data | {status, published_at}'
```

`published_at` is left intact. The public detail route starts returning `404`
immediately at the API layer, though an edge cache may serve the old HTML until
`s-maxage` expires.

### Delete (soft)

```bash
curl -s -o /dev/null -w '%{http_code}\n' -b "$JAR" \
  -X DELETE "$API/api/admin/jobs/$JOB_ID" \
  -H "X-CSRF-Token: $CSRF" -H "Origin: http://localhost:4321"
# 204
```

Sets `deleted_at = now()`. The row survives and can be recovered — see
[DATABASE.md](DATABASE.md#soft-delete). Deleting an already-deleted job is
`404 JOB_NOT_FOUND`. The UI must require an explicit confirmation before calling
this.

### Proving the CSRF layer works

```bash
# no CSRF header
curl -s -b "$JAR" -X DELETE "$API/api/admin/jobs/$JOB_ID" \
  -H "Origin: http://localhost:4321" | jq '.error.code'      # "CSRF_INVALID"

# wrong Origin
curl -s -b "$JAR" -X DELETE "$API/api/admin/jobs/$JOB_ID" \
  -H "X-CSRF-Token: $CSRF" -H "Origin: https://evil.example" | jq '.error.code'
# "CSRF_INVALID"

# no cookie at all
curl -s -X GET "$API/api/admin/jobs" | jq '.error.code'      # "UNAUTHORIZED"
```

When you are finished, delete the jar. It contains a live session token:

```bash
rm -f "$JAR"
```

---

## 6. Error codes and what to do about them

| Code | HTTP | Cause | What a client should do |
|---|---|---|---|
| `BAD_REQUEST` | 400 | Malformed JSON, bad query parameter, unparseable UUID | Fix the request. Never retry unchanged — it will fail identically forever |
| `UNAUTHORIZED` | 401 | No session, expired session, unknown session | Clear local session state and send the user to the login screen. Do **not** retry; the session is gone |
| `INVALID_CREDENTIALS` | 401 | Login failed | Show one generic "email or password is incorrect" message. Do not hint which was wrong — the API deliberately does not tell you, and passing on a guess re-creates the enumeration hole |
| `FORBIDDEN` | 403 | Authenticated but not permitted | Do not retry. Surface it; it means the account lacks the right, not that the request was malformed |
| `CSRF_INVALID` | 403 | Missing or mismatched `X-CSRF-Token`, or a bad `Origin` | Refresh the token with `GET /api/admin/me` and retry **once**. If it fails again it is configuration, not a stale token — see [TROUBLESHOOTING.md](TROUBLESHOOTING.md) |
| `JOB_NOT_FOUND` | 404 | No such job, or not publicly visible | Render the 404 page. Do not distinguish "missing" from "not published" in the UI — the API deliberately does not |
| `NOT_FOUND` | 404 | No such route | A client bug. Fix the path |
| `METHOD_NOT_ALLOWED` | 405 | Known path, wrong method | A client bug. Fix the method |
| `SLUG_CONFLICT` | 409 | An explicitly supplied slug is taken | Show the conflict on the slug field and let the user choose another, or omit `slug` and let the server derive and de-duplicate one |
| `PAYLOAD_TOO_LARGE` | 413 | Body over 64 KiB | Shorten the input. In practice this means the description; the limit is well above the contract's 20,000-character maximum |
| `VALIDATION_FAILED` | 422 | Body parsed, fields invalid | Map `details` onto the form fields. Every invalid field is present, so show them all at once rather than one per submit |
| `RATE_LIMITED` | 429 | Too many login attempts | Read `Retry-After` and disable the submit button for that long. Do not retry in a loop |
| `INTERNAL_ERROR` | 500 | Unexpected server fault | Show a generic error with a retry affordance, and surface `request_id` so the user can quote it. Retry at most once, with a delay |
| `SERVICE_UNAVAILABLE` | 503 | Database unreachable, or a query timed out | Retryable, with backoff. Frequently this is a scaled-to-zero database still waking |
| `NETWORK_ERROR` | — | **Client-side only.** The request never reached the API — DNS, TLS, offline, or the 10s `apiFetch` timeout | Offer a retry. It is not a server response, so there is no `request_id` |

Retry policy in one line: **retry `NETWORK_ERROR`, `SERVICE_UNAVAILABLE` and
`INTERNAL_ERROR` with backoff; retry `CSRF_INVALID` exactly once after
refreshing the token; never retry anything else.** `ApiError.isRetryable` in
`frontend/src/lib/api.ts` encodes the first part of that rule so no UI component
has to remember it.

---

## 7. Pagination

`meta` is on every collection response:

```json
{ "page": 1, "per_page": 20, "total": 42, "total_pages": 3 }
```

| Field | Meaning |
|---|---|
| `page` | 1-based, echoing the effective page after clamping |
| `per_page` | The effective size after clamping — not necessarily what you asked for |
| `total` | Matching rows **ignoring pagination** |
| `total_pages` | `0` when `total` is `0` |

Semantics worth knowing before you build a pager:

- `total_pages` is `0`, not `1`, for an empty result. `page === total_pages` is
  therefore not a safe "last page" test on an empty list. Check `total === 0`
  first and render the empty state.
- Requesting a page beyond the end is `200` with `data: []`, not `404`. A user
  who bookmarks `?page=9` and comes back after jobs expire gets a valid, empty
  page.
- `per_page` is clamped: `1..50` on the public list, `1..100` on the admin list.
  Always read `meta.per_page` back rather than assuming your request was honoured.
- Ordering is stable across pages because it ends in a total tiebreak
  (`created_at DESC` publicly, `updated_at DESC` in the admin). It is still
  offset pagination, so a job published between two page fetches can shift a row
  across a page boundary. For a job board at this scale that is acceptable;
  cursor pagination is the fix if it ever is not, and the envelope can carry a
  cursor without breaking clients because `meta` is an object.

---

## 8. Caching

| Response | `Cache-Control` |
|---|---|
| `GET /api/jobs` | `public, max-age=0, s-maxage=60, stale-while-revalidate=300` |
| `GET /api/jobs/{slug}` | `public, max-age=0, s-maxage=300, stale-while-revalidate=600` |
| Everything under `/api/admin` | `no-store` |

`max-age=0` with a non-zero `s-maxage` means: **browsers do not cache, shared
caches do.** A visitor never sees yesterday's page from their own disk, while the
CDN absorbs the load. `stale-while-revalidate` lets the edge serve a slightly
stale response *immediately* while it refreshes behind the scenes, which is what
prevents one unlucky visitor from paying for a cold serverless instance plus a
cold Neon compute.

Practical consequences for a client:

- After a publish, a cached path can lag by up to `s-maxage`. If you are testing
  and want the truth right now, call the API directly rather than the page, or
  bypass the cache.
- `no-store` on admin responses is not negotiable. Do not add a client-side cache
  in front of admin calls; the whole point is that per-session data is never
  stored anywhere.
- Confirm what the edge did with the response headers your CDN adds for cache
  status. See [TROUBLESHOOTING.md](TROUBLESHOOTING.md).

---

## 9. Walkthrough: adding a field to Job

Say you need `visa_sponsorship` (a nullable boolean: does the employer sponsor a
work visa?). Nothing about this is hard; the value of the walkthrough is that it
names **every** place that must change, because missing one produces a field
that exists in the database and is invisible in the API, or a form that submits a
field the server silently ignores.

**Rule zero, from [CONTRIBUTING.md](CONTRIBUTING.md): change
[API-CONTRACT.md](API-CONTRACT.md) first.** The contract is what keeps three
independently written pieces in agreement. A field that reaches the frontend
without passing through the contract is a field the backend does not know about.

| # | Where | Change | Notes |
|---|---|---|---|
| 1 | `docs/API-CONTRACT.md` | Add the row to `JobSummary` (2.1) and to the validation table (9). Add it to the `POST` example body (4) | Do this first, and get it reviewed before writing code |
| 2 | `backend/migrations/` | A new sequential file, e.g. `003_add_visa_sponsorship.sql`: `ALTER TABLE jobs ADD COLUMN visa_sponsorship boolean` | **Nullable or defaulted, always.** A `NOT NULL` column with no default fails against existing rows. Never edit an applied migration — see [DATABASE.md](DATABASE.md#migrations) |
| 3 | `backend/internal/jobs` — model | Add the field to the `Job` struct with its JSON tag. Use a nullable Go type so `null` round-trips as `null` rather than becoming `false` | `null` and `false` mean different things here: "unknown" versus "no" |
| 4 | `backend/internal/jobs` — repository | Add the column to every `SELECT` column list, to the `INSERT`, to the `UPDATE`, and to the row-scanning code | The most common miss. A field absent from one `SELECT` is `null` on exactly one endpoint, which reads as a data bug |
| 5 | `backend/internal/validation` (or the jobs validator) | A rule, even a permissive one: it is optional, and it must be a boolean | Every field has an explicit rule. "No rule" is how an unvalidated field arrives |
| 6 | `backend/internal/jobs` — service | Only if the field participates in a business rule. A plain optional flag does not | Do not add a pass-through method for its own sake |
| 7 | `frontend/src/lib/types.ts` | Add to `JobSummary` (so `Job` inherits it) and to `JobInput` as optional | `strict` plus `noUncheckedIndexedAccess` are on, so the compiler will find most of the places that now need updating |
| 8 | `frontend/src/components/admin/JobForm` (React island) | A labelled control, wired into submit. Programmatically associated label, and an error region if it can be invalid | Accessibility is not optional: a control without a label is a bug, not a polish item |
| 9 | `frontend/src/components/JobCard` | Only if it belongs in the list view. A visa badge probably does | `JobSummary` already carries the field, so no extra fetch is needed. That is why the list shape includes everything except `description` |
| 10 | `frontend/src/pages/jobs/[slug].astro` | Render it in the detail page's meta block | Escape it. Everything from the database is untrusted |
| 11 | `frontend/src/lib/seo.ts` — JSON-LD | Map it if `JobPosting` has a corresponding property. `jobLocationType`, `employmentType` and `baseSalary` have real Schema.org meanings; a made-up property adds nothing | Do not invent Schema.org properties. Validate with the Rich Results Test |
| 12 | `frontend/src/styles/*.module.css` | Any new styling, using tokens only | No raw hex, no raw pixel font size, no magic radius. `tokens.css` is the only place those live |
| 13 | `frontend/src/lib/copy.ts` | The label and any help text | Centralised copy is what keeps i18n a lookup change |
| 14 | Tests | A repository round-trip, a validation case, and a component render | Both a valid and an invalid value. A test only for the happy path documents nothing |
| 15 | `docs/DATABASE.md` | Add the column to the schema table, with type, nullability, default and purpose | The schema documentation is part of the change, not a follow-up |
| 16 | `docs/API.md` | Update the `curl` create example if the field is worth showing | This file |

Then, before opening the pull request:

```bash
cd backend  && go vet ./... && go test ./...
cd ../frontend && npm run check && npm run build && npm run test
```

Two failure modes this list is designed to prevent, both of which are quiet:

- **Step 4 skipped for one query.** The field works everywhere except the public
  list, and it looks like the data is wrong rather than the SQL.
- **Step 7 done without step 1.** TypeScript is happy, the form submits the
  field, the backend ignores unknown request fields by contract, and the value
  is silently discarded on every save with no error anywhere.
