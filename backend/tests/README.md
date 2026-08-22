# Backend tests

## First command on a fresh checkout

There is **no `go.sum` in the repository.** It could not be generated where this
code was written, because that machine has no Go toolchain. `go.mod` lists the
correct module path, the Go version, and pinned versions for both direct
dependencies, but the checksum database entries have to be fetched.

So the first thing to run, before anything else:

```bash
cd backend
go mod tidy      # writes go.sum and resolves the transitive versions
go vet ./...
go build ./...
```

`go mod tidy` is authoritative. If it moves a patch version listed in `go.mod`,
that is expected and the new value should be committed.

Commit the generated `go.sum`. Without it every build re-resolves the module
graph and a dependency could change underneath a deploy.

## Two suites

| Suite | Needs Postgres | Command |
|---|---|---|
| Unit | no | `go test ./...` |
| Integration | yes | `TEST_DATABASE_URL=... go test -tags=integration ./tests/...` |

The split is deliberate. `go test ./...` on a laptop with no database must pass,
so anything that needs Postgres lives behind the `integration` build tag and is
skipped — with `t.Skip`, not a failure — when `TEST_DATABASE_URL` is unset.

## Unit tests

```bash
go test ./...                      # everything that needs no database
go test -race ./...                # the rate limiter and the argon2 sink are concurrent
go test -cover ./...
go test -run TestValidateApplyURL ./internal/validation
```

What they cover, and why each is a unit test rather than an integration test:

| Package | File | Covers |
|---|---|---|
| `internal/config` | `config_test.go` | every required variable, every rejection, and that no stringification of a `Config` can emit a secret |
| `internal/httpx` | `errors_test.go` | the closed error-code set against contract section 6, that `INTERNAL_ERROR` leaks nothing, body decoding and the 64 KiB cap, request-id sanitising |
| `internal/validation` | `applyurl_test.go` | the apply-URL allowlist: `javascript:` in three cases, `data:`, `vbscript:`, `file:`, `blob:`, `about:`, `mailto:`, `tel:`, scheme-relative, relative, userinfo, embedded newline/tab/NUL, the 2048 boundary, the `http` switch, localhost in development, and a set of schemes nobody has thought of |
| `internal/validation` | `validate_test.go` | every boundary in contract section 9, at min-1/min/max/max+1, and that all invalid fields are reported in one `details` map |
| `internal/jobs` | `slug_test.go` | derivation, accent folding (precomposed and decomposed), punctuation runs, the 80-character cut on a hyphen boundary, the CJK fallback, reserved words, and `-2`/`-3` de-duplication |
| `internal/jobs` | `service_test.go` | every business rule, against a fake `Repo`. This is why `Service` depends on an interface |
| `internal/auth` | `argon2_test.go` | round trip, wrong passwords, per-call salt, the PHC encoding, that old hashes survive a parameter change, and that 15 kinds of malformed hash return an error rather than panicking or silently returning false |

`internal/auth/argon2_test.go` uses a deliberately cheap parameter set
(8 MiB, t=1) wherever the cost is not the thing under test. `DefaultParams` at
64 MiB is checked once, for its values, without hashing at that cost repeatedly.

## Integration tests

`integration_test.go` needs a real PostgreSQL 13+ with permission to create
extensions (`pgcrypto`, and ideally `pg_trgm` — the migration degrades gracefully
without it).

```bash
docker run --rm -d --name jp-test -p 5433:5432 \
  -e POSTGRES_PASSWORD=test -e POSTGRES_DB=jobpaglu_test postgres:17-alpine

TEST_DATABASE_URL='postgres://postgres:test@localhost:5433/jobpaglu_test?sslmode=disable' \
  go test -tags=integration -v ./tests/...

docker rm -f jp-test
```

**The database is truncated at the start of every test.** Point
`TEST_DATABASE_URL` at a throwaway database, never at a development one you care
about, and never at anything shared.

The suite brings up the real router over `httptest`, runs the real migrations,
and drives it through an ordinary `http.Client` with a cookie jar — so cookie
attributes, CORS headers and CSRF all behave as they would in a browser.

`APP_ENV` is forced to `development` inside the harness for one reason: the
session cookie would otherwise carry `Secure`, and Go's cookie jar silently drops
a `Secure` cookie over the plain HTTP that `httptest` serves. The symptom would
be a wall of 401s that look like an authentication bug.

What only the integration suite can prove:

- **Access control.** Every admin route returns `401 UNAUTHORIZED` without a
  session. Logout is excluded on purpose — the contract makes it idempotent, so
  no session is a `204`.
- **CSRF.** Every write returns `403 CSRF_INVALID` with a missing or wrong
  `X-CSRF-Token`, and with a missing, foreign, `null`, wrong-scheme or wrong-port
  `Origin`. A `GET` needs neither.
- **Public visibility.** Draft, unpublished, soft-deleted and expired jobs all
  return `404` by slug and are absent from the list — and `meta.total` agrees,
  which is what catches a count query that forgot the predicate.
- **Publish semantics.** `published_at` is stamped once; unpublish/re-publish
  does not rewrite it. Unpublish makes the public detail `404` immediately.
- **Publish validation.** A row with a `javascript:` `apply_url` planted by a
  direct `UPDATE` cannot be published.
- **Soft delete.** The row survives with `deleted_at` set, disappears from both
  APIs, a second delete is `404`, and the slug is immediately reusable.
- **Rate limiting.** Five bad logins are `401`; the sixth is `429` with a
  non-zero `Retry-After`, and the correct password is refused while limited.
- **No user enumeration.** An unknown address and a wrong password produce
  byte-identical bodies apart from the request id.
- **Session rotation.** Logging in twice changes the token and kills the old one
  server-side.
- **Degradation.** Against an unreachable database, `/health` is still `200`,
  `/ready` is `503`, and the public endpoints are `503 SERVICE_UNAVAILABLE` —
  not `500`, not a panic, and with no connection detail in the body.
- **Routing surface.** An unknown path is `404 NOT_FOUND` and a wrong method is
  `405 METHOD_NOT_ALLOWED`, both as JSON — `net/http`'s plain-text replies must
  not escape.

One test sleeps for ~1.1s (`TestPublishStampsPublishedAtOnceAndUnpublishHidesImmediately`),
because timestamps are RFC 3339 at second precision and proving "not rewritten"
needs the clock to have moved.

## Notes for a reviewer

None of this has been compiled or executed: the machine it was written on has no
Go toolchain (project `AGENTS.md`, P-001). Treat the first `go vet ./...` as part
of the review, and expect it — not the prose here — to be the authority on
whether it builds.
