# Job Paglu — API Contract

**This file is normative.** The Go backend and the Astro frontend are written
independently against it. If the two sides disagree, this file decides. Change
this file *before* changing either implementation.

Mirror of these shapes in TypeScript: `frontend/src/lib/types.ts`.
Mirror in Go: `backend/internal/jobs/model.go` + `backend/internal/httpx`.

---

## 1. Conventions

- Base path: `/api`. Health endpoints are at the root (`/health`, `/ready`).
- Request and response bodies are `application/json; charset=utf-8`.
- Max request body: **64 KiB**. Larger returns `413 PAYLOAD_TOO_LARGE`.
- All timestamps are **RFC 3339 in UTC with a `Z` suffix**, e.g.
  `2026-08-21T09:41:07Z`. The database stores `timestamptz`; the API never
  emits a local offset. Humanising a date is the client's job.
- IDs are UUID v4 strings. **Public URLs use `slug`, never `id`.**
- Unknown fields in a request body are ignored, not rejected.
- Every response carries `X-Request-Id`. It also appears in the server log line
  and in `error.request_id`, so a user-reported failure is traceable.

### 1.1 Success envelope

Single resource:

```json
{ "data": { "...": "..." } }
```

Collection:

```json
{
  "data": [ { "...": "..." } ],
  "meta": { "page": 1, "per_page": 20, "total": 42, "total_pages": 3 }
}
```

A bare array is never returned, so adding `meta` later can never be a breaking
change.

### 1.2 Error envelope

```json
{
  "error": {
    "code": "VALIDATION_FAILED",
    "message": "One or more fields are invalid.",
    "details": { "apply_url": "Must be an absolute https:// URL." },
    "request_id": "01J8Z..."
  }
}
```

- `code` is from the closed set in section 6. Clients switch on `code`, never on
  `message`.
- `message` is safe to show a user. It never contains SQL, a file path, a stack
  frame, or an internal hostname.
- `details` is present only for `VALIDATION_FAILED`, and maps field name to a
  single human-readable message.

---

## 2. Data shapes

### 2.1 `JobSummary` — returned by list endpoints

Deliberately omits the full `description` to keep the list payload small.

| Field | Type | Notes |
|---|---|---|
| `id` | `string` | UUID. Present in admin responses; also present publicly but unused by public URLs. |
| `slug` | `string` | URL-safe, unique among non-deleted jobs. |
| `title` | `string` | |
| `excerpt` | `string` | Server-computed. First 200 chars of `description`, cut on a word boundary, `…` appended if truncated. Never HTML. |
| `salary` | `string` | Free text, e.g. `"$120k – $150k"`. |
| `qualification` | `string` | Free text. |
| `job_type` | `JobType` | See 2.4. |
| `location` | `string` | Free text, e.g. `"Berlin, Germany"`. |
| `company` | `string \| null` | |
| `country` | `string \| null` | |
| `remote_type` | `RemoteType \| null` | See 2.4. |
| `experience_level` | `string \| null` | |
| `category` | `string \| null` | |
| `is_featured` | `boolean` | |
| `apply_url` | `string` | Exactly the URL the admin stored. See section 7. |
| `status` | `JobStatus` | Always `"published"` on public endpoints. |
| `published_at` | `string \| null` | |
| `expires_at` | `string \| null` | |
| `created_at` | `string` | |
| `updated_at` | `string` | |

### 2.2 `Job` — returned by single-resource endpoints

Every `JobSummary` field, plus:

| Field | Type | Notes |
|---|---|---|
| `description` | `string` | Full text. **Plain text with newlines, not HTML.** Renderers must escape it and may convert blank lines to paragraphs. |

`excerpt` is still present on `Job` so a client can reuse one type.

### 2.3 `Pagination` (`meta`)

| Field | Type |
|---|---|
| `page` | `number` (1-based) |
| `per_page` | `number` |
| `total` | `number` (matching rows, ignoring pagination) |
| `total_pages` | `number` (`0` when `total` is `0`) |

### 2.4 Enumerations

```
JobStatus  = "draft" | "published" | "unpublished"
JobType    = "full-time" | "part-time" | "contract" | "internship"
           | "freelance" | "temporary"
RemoteType = "remote" | "hybrid" | "onsite"
```

`JobType` and `RemoteType` are stored as text with a `CHECK` constraint, so
adding a member is an additive migration. Clients must tolerate an unknown
member by displaying it verbatim rather than crashing.

### 2.5 `AdminIdentity`

| Field | Type |
|---|---|
| `email` | `string` — from the verified Access token |
| `sub` | `string` — opaque Access subject id |

No password hash, session token or CSRF token exists anywhere in the system.

---|---|
| `id` | `string` (UUID) |
| `email` | `string` |
| `name` | `string` |
| `last_login_at` | `string \| null` |

A password hash is never present in any response.

---

## 3. Public endpoints

### `GET /api/jobs`

Returns published, non-deleted, non-expired jobs, newest `published_at` first,
with featured jobs ahead of the rest.

Ordering, exactly: `is_featured DESC, published_at DESC NULLS LAST, created_at DESC`.

Query parameters — all optional, all already implemented server-side even
though no UI is wired to them yet:

| Param | Type | Default | Rules |
|---|---|---|---|
| `page` | int | `1` | `< 1` is clamped to `1`. Non-numeric is `400 BAD_REQUEST`. |
| `per_page` | int | `20` | Clamped to `1..50`. `0`, absent, or unparseable means "unspecified" and yields the default `20` — not `1`, which would be a useless page nobody asked for. |
| `q` | string | — | Case-insensitive match against title, company, location, description. Over 100 chars is `400 BAD_REQUEST`, **not** silently truncated: quietly searching for something other than what was asked returns confidently wrong results. |
| `location` | string | — | Case-insensitive substring. |
| `country` | string | — | Case-insensitive exact. |
| `job_type` | `JobType` | — | Unknown value is `400 BAD_REQUEST`. |
| `remote_type` | `RemoteType` | — | Unknown value is `400 BAD_REQUEST`. |
| `featured` | `true`/`false` | — | Any other value is `400 BAD_REQUEST`. |

Response `200`: `{ data: JobSummary[], meta: Pagination }`.
An empty result is `200` with `data: []` and `total: 0` — **not** `404`.

Cache header: `Cache-Control: public, max-age=0, s-maxage=60, stale-while-revalidate=300`.

### `GET /api/jobs/{slug}`

Response `200`: `{ data: Job }`.
`404 JOB_NOT_FOUND` when the slug does not exist **or** the job is `draft`,
`unpublished`, soft-deleted, or expired. The public endpoint cannot distinguish
these cases, deliberately — an admin's draft slug must not be discoverable.

Cache header: `Cache-Control: public, max-age=0, s-maxage=300, stale-while-revalidate=600`.

### `GET /health`

`200 {"data":{"status":"ok"}}` whenever the process is alive. Does not touch the
database — a liveness probe must not fail because a dependency is down.

### `GET /ready`

- `200 {"data":{"status":"ready"}}` when a `SELECT 1` against Postgres succeeds
  within 2s.
- `503 SERVICE_UNAVAILABLE` otherwise.

---

## 4. Admin endpoints

All paths are under `/api/admin`. All except `login` require a valid session
cookie. All state-changing methods (`POST`, `PUT`, `DELETE`) additionally
require CSRF (section 5).

Admin endpoints always send `Cache-Control: no-store`.

### `POST /api/admin/login`

Request:

```json
{ "email": "admin@example.com", "password": "..." }
```

- `200`: `{ "data": { "admin": Admin, "csrf_token": "..." } }` plus
  `Set-Cookie: __Host-jp_session=...; HttpOnly; Secure; SameSite=Lax; Path=/; Max-Age=43200`
  and `Set-Cookie: __Host-jp_csrf=...; Secure; SameSite=Lax; Path=/; Max-Age=43200`
  (the CSRF cookie is intentionally **not** HttpOnly — the client must read it).
  Cookie names are `APP_ENV`-derived; see section 5.
- `401 INVALID_CREDENTIALS` for both an unknown email and a wrong password, with
  the same message and a comparable response time — no user enumeration.
- `422 VALIDATION_FAILED` for a missing/malformed field.
- `429 RATE_LIMITED` after **5 failed attempts from one IP within 15 minutes**,
  and after **10 failed attempts against one email within 15 minutes**. Includes
  a `Retry-After` header in seconds. A successful login clears the counters.

Logging in again rotates the session token (no session fixation).

### `POST /api/admin/logout`

`204`, deletes the server-side session and sends expired cookies. Idempotent:
calling it without a session is still `204`.

### `GET /api/admin/me`

`200 { "data": { "admin": Admin, "csrf_token": "..." } }` — the frontend's way
to check "am I still logged in?" and to obtain the CSRF token after a reload.
`401 UNAUTHORIZED` when there is no valid session.

### `GET /api/admin/jobs`

Like the public list, but returns **all** statuses and ignores expiry. Excludes
soft-deleted rows unless `include_deleted=true`.

Extra parameters: `status` (`draft|published|unpublished`), `include_deleted`
(`true|false`, default `false`). `per_page` clamps to `1..100` here.
Ordering: `updated_at DESC`.

Returns `JobSummary[]` — the same shape, with a real `status`.

### `GET /api/admin/jobs/{id}`

By **UUID**, not slug, so an admin can still load a job after a slug change.
`200 { data: Job }` / `404 JOB_NOT_FOUND`.

### `POST /api/admin/jobs`

Request — the 7 required fields, plus optional ones:

```json
{
  "title": "Senior Cloud Engineer",
  "description": "Full text...",
  "salary": "$140k – $175k",
  "qualification": "B.Sc. Computer Science or equivalent experience",
  "job_type": "full-time",
  "location": "Berlin, Germany",
  "apply_url": "https://example.com/careers/senior-cloud-engineer",

  "company": "Acme GmbH",
  "country": "Germany",
  "remote_type": "hybrid",
  "experience_level": "5+ years",
  "category": "Engineering",
  "is_featured": false,
  "expires_at": "2026-12-31T23:59:59Z",
  "status": "draft",
  "slug": "senior-cloud-engineer"
}
```

- `status` defaults to `"draft"`. Supplying `"published"` sets `published_at` to
  now.
- `slug` is optional. When omitted it is derived from `title` (section 8). When
  supplied it is normalised the same way and must still be unique.
- `201 { data: Job }` with a `Location: /api/admin/jobs/{id}` header.
- `422 VALIDATION_FAILED` with per-field `details`.
- `409 SLUG_CONFLICT` only when an explicit `slug` collides. An auto-derived
  slug never conflicts — it is de-duplicated (section 8).

### `PUT /api/admin/jobs/{id}`

Full replacement of the mutable fields, same body as `POST`.

- The `id` never changes.
- **The slug is stable:** if `slug` is absent from the body, the existing slug
  is kept even when `title` changes. A slug only changes when explicitly sent.
  This protects live URLs and SEO.
- **`status` is also stable when omitted.** If `status` is absent from the body,
  the job keeps the status it already had — it does **not** fall back to the
  `POST` default of `draft`. Anything else would mean a dashboard "Save" on a
  live job silently unpublishes it, which is a data-loss-shaped bug rather than
  a defensible default.
- `status` *may* be changed here when sent explicitly, but the publish/unpublish
  endpoints are the preferred path because they carry the `published_at`
  semantics.
- `200 { data: Job }` / `404` / `422` / `409`.

### `DELETE /api/admin/jobs/{id}`

**Soft delete:** sets `deleted_at = now()`. The row survives so it can be
recovered; the job disappears from public listings and from the admin list
immediately.

`204` on success, `404 JOB_NOT_FOUND` if unknown or already deleted.
The UI must require an explicit confirmation before calling this.

### `POST /api/admin/jobs/{id}/publish`

Sets `status = 'published'`. Sets `published_at = now()` **only if it was
null**, so re-publishing does not rewrite the original publication date.
`200 { data: Job }`.

Rejects with `422 VALIDATION_FAILED` if the job would be invalid when public.
**Every stored field is re-validated, not just `apply_url`**, and against the
policy the server is currently running with. Two reasons: a row can reach an
invalid state by a direct database edit or an older migration, and validating
`apply_url` against the *current* `ALLOW_HTTP_APPLY_URLS` avoids the trap where
a value accepted on write is rejected at publish time (or worse, the reverse).

A job must never become publicly applicable without a usable apply link.

### `POST /api/admin/jobs/{id}/unpublish`

Sets `status = 'unpublished'`, leaves `published_at` intact. The public detail
route starts returning `404` immediately. `200 { data: Job }`.

---

## 5. Authentication and CSRF

**Cloudflare-only branch: admin auth is Cloudflare Access. There is no login
endpoint, no password, no session of ours, and no CSRF token.**

`POST /api/admin/login` and `POST /api/admin/logout` **do not exist**. Signing
out is a link to Access's own `/cdn-cgi/access/logout`.

### 5.1 Why it changed

The Workers free plan caps CPU at **10 ms per invocation**. Argon2id at the
specified parameters (64 MiB, t=3) needs 50-150 ms of pure CPU, so a password
hash cannot run there at all. Weakening the parameters to fit would defeat the
purpose of hashing. Access moves authentication to the edge, before the request
reaches this code, which removes the KDF, the session table, session rotation
and login rate limiting along with it.

### 5.2 How a request is authenticated

Cloudflare Access places a signed JWT in the `Cf-Access-Jwt-Assertion` header
(and a `CF_Authorization` cookie). Every `/api/admin/*` route verifies it before
doing anything else, and a failure is `401 UNAUTHORIZED` with no detail about
*which* check failed:

1. three non-empty base64url segments;
2. `alg` is exactly `RS256` — compared against one allowed value, never used to
   *select* an algorithm. Accepting `none`, or letting the token pick HS256 so
   the public key is treated as an HMAC secret, are the two classic forgeries;
3. RSASSA-PKCS1-v1_5 / SHA-256 signature verified against the JWKS key named by
   `kid`, fetched from `https://<team>/cdn-cgi/access/certs` and cached in
   module scope with a TTL (an unknown `kid` forces one refresh, so key rotation
   does not wait out the cache);
4. `iss` equals `https://<team domain>`;
5. `aud` contains the configured `ACCESS_AUD` — **this is what stops a token
   minted for a different application in the same Access team**;
6. `exp` in the future, `iat`/`nbf` not in the future, with 60s of skew.

An unreachable JWKS is `503`, never "trust the header anyway".

### 5.3 CSRF is still required, and this is why

Access authenticates with the **`CF_Authorization` cookie**, which a browser
attaches to cross-site requests like any other. A state-changing request forged
from another origin would therefore arrive already authenticated.

The control is Astro's origin check (`security.checkOrigin: true` in
`astro.config.mjs`): a state-changing request whose `Origin` does not match the
host is refused. That is the same guarantee the old `X-CSRF-Token` gave, without
a token to mint, store or rotate.

Consequence for non-browser clients: a `curl` caller must send `Origin`, or use
a JSON content type, or it gets `403`. That is deliberate.

### 5.4 CORS

There is none, and that is the point. Site and API are one Pages project, so
every admin call is same-origin. No `Access-Control-Allow-Origin` header is
emitted, which is itself a protection: a JSON or `DELETE` write from another
origin is a non-simple request, so the browser preflights it, and the absence of
CORS headers fails the preflight.

### 5.5 `GET /api/admin/me`

`200 {"data":{"admin":{"email":"...","sub":"..."}}}`

`email` and `sub` come from the verified token. There is deliberately **no**
`name`, `last_login_at`, or `csrf_token`: there is no `admin_users` row to
source a name from, and no cookie of ours to protect with a token. `401` means
Access did not authenticate the browser.

### 5.6 Development

There is no Access edge in front of `wrangler dev`. When `APP_ENV` is exactly
`development` **and** `ACCESS_DEV_BYPASS_EMAIL` is set, the admin API accepts
that identity with no JWT, and logs a warning on every use. Both conditions are
required and the variable is never read unless `APP_ENV === 'development'`, so
with `APP_ENV=production` the bypass is unreachable — verified by test (Mode B:
all 8 admin routes return 401 with the variable present but `APP_ENV=production`).

## 6. Error codes

| Code | HTTP | When |
|---|---|---|
| `BAD_REQUEST` | 400 | malformed JSON, bad query parameter, unparseable UUID |
| `UNAUTHORIZED` | 401 | no session, expired session, unknown session |
| `INVALID_CREDENTIALS` | 401 | login failed (unknown email or wrong password) |
| `FORBIDDEN` | 403 | authenticated but not permitted |
| `CSRF_INVALID` | 403 | missing/mismatched CSRF token, or bad `Origin` |
| `JOB_NOT_FOUND` | 404 | no such job, or not publicly visible |
| `NOT_FOUND` | 404 | no such route |
| `METHOD_NOT_ALLOWED` | 405 | known path, wrong method |
| `SLUG_CONFLICT` | 409 | explicit slug already taken |
| `PAYLOAD_TOO_LARGE` | 413 | body over 64 KiB |
| `VALIDATION_FAILED` | 422 | body parsed, fields invalid; `details` populated |
| `RATE_LIMITED` | 429 | too many login attempts; `Retry-After` set |
| `INTERNAL_ERROR` | 500 | unexpected; message is generic, `request_id` set |
| `SERVICE_UNAVAILABLE` | 503 | database unreachable (`/ready`, or a query timeout) |

`204` responses have no body at all.

---

## 7. `apply_url` validation

This is the one field where a bad value becomes an attack on visitors, so it is
validated on write and re-checked on render.

Accept only if **all** hold:

1. Parses as an absolute URL.
2. Scheme is `https`. `http` is accepted only when the backend runs with
   `ALLOW_HTTP_APPLY_URLS=true` (default `false`).
3. Scheme is not one of `javascript`, `data`, `vbscript`, `file`, `blob`,
   `about`, `mailto`, `tel`, or anything else — the check is an allowlist, not a
   denylist.
4. A non-empty host containing a `.`, or `localhost` in development.
5. No userinfo component (`https://user:pass@host` is rejected).
6. Length <= 2048.
7. No ASCII control characters, no whitespace, no raw newline.

The stored value is returned **byte-for-byte** — never rewritten, proxied, or
shortened (request section 11).

On render, `ApplyButton` re-validates the scheme before emitting `href`. A value
failing that check renders a disabled state, not a link. Belt and braces: a bad
row from a manual database edit must not become a live `javascript:` link.

Outbound links use `target="_blank"` with `rel="noopener noreferrer nofollow"`.

---

## 8. Slug rules

Derivation from `title`:

1. Fold to ASCII and strip combining marks (`Señor` becomes `senor`), handling
   both precomposed and decomposed input.

   *Implementation note:* the Go side does this with an explicit
   Latin-1/Latin-Extended-A fold table rather than true Unicode NFKD, because
   NFKD would require `golang.org/x/text` and break the two-dependency rule. The
   observable difference is narrow: an unmapped script (Cyrillic, Greek) folds to
   nothing and the slug falls back to `job-<8 hex>` — which is already the
   specified behaviour for a CJK-only title in step 6. Titles in a Latin script,
   accented or not, behave identically.
2. Lowercase.
3. Replace every run of non-`[a-z0-9]` with a single `-`.
4. Trim leading/trailing `-`.
5. Truncate to 80 chars on a `-` boundary.
6. If the result is empty (e.g. a CJK-only title), use `job-<first 8 of uuid>`.

De-duplication: if `slug` exists among non-deleted jobs, append `-2`, then
`-3`, and so on until free. `Senior Cloud Engineer` becomes
`senior-cloud-engineer`, then `senior-cloud-engineer-2`.

Reserved slugs that must never be assigned: `new`, `admin`, `api`, `jobs`,
`login`, `logout`, `health`, `ready`, `sitemap`, `robots`. A derived slug
matching one gets `-job` appended.

---

## 9. Field validation rules

| Field | Required | Rules |
|---|---|---|
| `title` | yes | 3–160 chars after trim |
| `description` | yes | 20–20000 chars after trim |
| `salary` | yes | 1–120 chars |
| `qualification` | yes | 1–240 chars |
| `job_type` | yes | member of `JobType` |
| `location` | yes | 1–160 chars |
| `apply_url` | yes | section 7 |
| `company` | no | <= 160 chars |
| `country` | no | <= 80 chars |
| `remote_type` | no | member of `RemoteType` |
| `experience_level` | no | <= 80 chars |
| `category` | no | <= 80 chars |
| `is_featured` | no | boolean, default `false` |
| `expires_at` | no | RFC 3339. Must be in the future **on create only**; on update only the format is checked, otherwise an already-expired job could never be edited — including to extend its expiry, which is the one edit it most needs |
| `status` | no | member of `JobStatus`, default `draft` |
| `slug` | no | 1–100 chars, matches `^[a-z0-9]+(-[a-z0-9]+)*$` |

Every string field is trimmed of leading/trailing whitespace and has ASCII
control characters (other than `\n` and `\t` in `description`) stripped before
validation. Validation errors are returned **all at once** in `details`, not one
at a time.

---

## 10. Example exchanges

```http
GET /api/jobs?page=1&per_page=2 HTTP/1.1
```

```json
{
  "data": [
    {
      "id": "8f14e45f-ceea-467a-9c5b-2b1a0e1c9d33",
      "slug": "senior-cloud-engineer",
      "title": "Senior Cloud Engineer",
      "excerpt": "Own the platform that serves millions of requests a day…",
      "salary": "$140k – $175k",
      "qualification": "B.Sc. Computer Science or equivalent",
      "job_type": "full-time",
      "location": "Berlin, Germany",
      "company": "Acme GmbH",
      "country": "Germany",
      "remote_type": "hybrid",
      "experience_level": "5+ years",
      "category": "Engineering",
      "is_featured": true,
      "apply_url": "https://example.com/careers/sce",
      "status": "published",
      "published_at": "2026-08-20T11:00:00Z",
      "expires_at": null,
      "created_at": "2026-08-20T10:58:12Z",
      "updated_at": "2026-08-20T11:00:00Z"
    }
  ],
  "meta": { "page": 1, "per_page": 2, "total": 1, "total_pages": 1 }
}
```

```http
POST /api/admin/jobs HTTP/1.1
Cookie: __Host-jp_session=...
X-CSRF-Token: ...
Content-Type: application/json
```

```json
{
  "error": {
    "code": "VALIDATION_FAILED",
    "message": "One or more fields are invalid.",
    "details": {
      "apply_url": "Must be an absolute https:// URL.",
      "title": "Must be between 3 and 160 characters."
    },
    "request_id": "01J8ZQ4M7T"
  }
}
```
