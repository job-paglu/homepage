# Contributing

Short and practical. Read [ARCHITECTURE.md](ARCHITECTURE.md) once before your
first change; everything else here you can look up as you go.

---

## The one rule that is not negotiable

**A change to the API means changing [API-CONTRACT.md](API-CONTRACT.md) first.**

The contract is normative. The Go backend and the Astro frontend are written
independently against it, and it is the only thing keeping them in agreement. So:

1. Change the contract.
2. Get that reviewed on its own merits, before any implementation.
3. Then change the backend, `frontend/src/lib/types.ts`, and the UI.

A field that reaches the frontend without passing through the contract is a field
the backend does not know about — and because the contract says unknown request
fields are *ignored, not rejected*, the failure is silent. The form submits, the
save returns `200`, and the value is discarded on every request with no error
anywhere.

There is a worked example of every place a field change has to touch in
[API.md](API.md#9-walkthrough-adding-a-field-to-job).

---

## Branches

```
<type>/<short-kebab-description>
```

| Type | For |
|---|---|
| `feat/` | A new capability |
| `fix/` | A bug fix |
| `docs/` | Documentation only |
| `refactor/` | No behaviour change |
| `chore/` | Dependencies, CI, tooling |
| `perf/` | Performance work |
| `test/` | Tests only |

```
feat/job-expiry-admin-field
fix/apply-url-trailing-newline
docs/deployment-same-site-section
```

Branch from `main`. Keep branches short-lived — a branch that lives for two
weeks is a merge conflict with a calendar entry. Never commit directly to `main`.

---

## Commits

Conventional Commits, because the prefix makes history skimmable and the scope
tells a reviewer which tier changed:

```
<type>(<scope>): <imperative summary, lower case, no full stop>

<optional body: WHY, not what. The diff already says what.>
```

Scopes in use: `api`, `frontend`, `admin`, `db`, `docs`, `ci`, `deploy`.

```
feat(api): add visa_sponsorship to the job payload
fix(frontend): strip a trailing newline before validating apply_url
docs(deploy): explain the site-origin to api-project cookie failure
refactor(db): move the visibility predicate into the repository
```

Rules:

- **One logical change per commit.** A commit that fixes a bug and reformats a
  file is a commit nobody can revert.
- Present-tense imperative: "add", not "added" or "adds".
- The body is for the reasoning. If a decision took thought, write it down — six
  months from now the diff will still be obvious and the reason will not.
- Reference an issue when there is one.
- Never mention a credential, even to say it changed.

---

## Code quality rules

These are the standards a review will hold you to.

| Rule | What it means in practice |
|---|---|
| **Readable** | Optimise for the next person reading it, not for the fewest characters. A clear ten-line function beats a clever three-line one |
| **Modular** | One responsibility per file, one job per function. If you cannot name what a function does without "and", split it |
| **Typed** | No `any` in TypeScript. No `interface{}`/`any` in Go where a real type exists. `tsconfig.json` runs `strict` plus `noUncheckedIndexedAccess`, `noImplicitOverride` and `noFallthroughCasesInSwitch` — do not weaken it to make an error go away |
| **No copy-pasted logic** | The second occurrence is a signal, the third is a defect. Extract it. **The one sanctioned exception is the `apply_url` scheme check**, which runs on write *and* on render on purpose — see [SECURITY.md](SECURITY.md#why-it-is-checked-twice). If you touch either side, leave a comment saying why the duplication exists |
| **No massive files or functions** | If a file needs a table of contents, split it. A function that does not fit on a screen is doing several things |
| **No hidden magic constants** | `42`, `#1f5f4b`, `300` and `20` in the middle of a function are all bugs waiting to happen. Colours, sizes, spacing and radii belong in `frontend/src/styles/tokens.css` and **nowhere else**. Limits, timeouts and cache durations belong in a named constant or in config, and if the contract specifies the value, cite the contract in a comment |
| **No hardcoded production config** | No hostname, no origin, no connection string, no bucket name in source. It comes from the environment, it is validated at startup, and the process refuses to start without it |
| **No unnecessary abstraction** | An interface with one implementation and no test double is indirection, not design. A pass-through service method that only forwards to the repository adds a file and hides a call. Write the direct thing; abstract when the second caller arrives |

Layer-specific rules that a review *will* catch:

- **SQL lives only in the repository layer.** A handler or service that builds a
  query will be rejected, even if it works. See
  [ARCHITECTURE.md](ARCHITECTURE.md#4-backend-layering) for what each layer is
  forbidden from doing.
- **The public visibility predicate stays in SQL.** Never re-implement it in Go.
- **React only under `components/admin/`.** No public page may carry a `client:`
  directive.
- **Every string that a user reads goes in `frontend/src/lib/copy.ts`.**
- **Never log a secret, a request body, a header or a cookie.**

Formatting is not a matter of taste:

```bash
cd backend  && gofmt -l .          # must print nothing
cd frontend && npm run check
```

---

## Accessibility and responsiveness are review criteria

Not polish items. A pull request that adds UI is expected to have:

- A programmatically associated `<label>` for every form control, and errors
  linked to their field
- A visible `:focus-visible` ring that is not colour-only
- Correct heading order — one `h1` per page, no skipped levels
- A minimum 44px touch target at mobile widths (`--jp-touch-min`)
- Contrast of at least 4.5:1 for body text and 3:1 for large text and meaningful
  borders. `--jp-accent` is **background only** — never colour text with it
- `prefers-reduced-motion` honoured, which the motion tokens already do
- Status messages announced through `aria-live`, not only shown
- A modal with a focus trap, `Escape` to close, `aria-modal`, and focus restored
  to the trigger

Check the layout from 320px to 2560px. No horizontal scrolling, no overlap, at
any width.

---

## CI gates

Every pull request must pass. These run in `.github/workflows/ci.yml`.

| Gate | Command | Fails on |
|---|---|---|
| Frontend types and diagnostics | `npm run check` | Any type error, any Astro diagnostic |
| Frontend build | `npm run build` | Build failure — note this is `astro check && astro build`, so a type error fails here too |
| Frontend tests | `npm run test` | Any failing test |
| Backend vet | `go vet ./...` | Any finding |
| Backend tests | `go test ./...` | Any failing test. Integration tests need a reachable Postgres, provided as a service container |
| Backend build | `go build ./...` | Compile failure |
| Formatting | `gofmt -l .` | Any output at all |
| Dependency scan (Go) | `govulncheck ./...` | A known vulnerability in a reachable code path |
| Static security scan (Go) | `gosec ./...` | A finding |
| Dependency scan (npm) | `npm audit` | A high-severity advisory |
| Secret scan | secret scanning over the diff | Anything that looks like a credential |

**Honest status:** the workflow is configured and **has never run** — nothing in
this repository has been built or executed. Expect the first pipeline to surface
something real (a missing `go.sum`, a version mismatch, a lint rule). That is CI
working, not a regression. Fix it rather than relaxing the gate.

Run the same checks locally before you push:

```bash
cd backend  && go mod tidy && gofmt -l . && go vet ./... && go test ./...
cd ../frontend && npm run check && npm run build && npm run test
```

---

## Tests

| Change | Minimum expected test |
|---|---|
| A repository query | A round-trip against a real Postgres, including the visibility predicate where relevant |
| A validation rule | Both a valid and an **invalid** case. A happy-path-only test documents nothing |
| A handler | Status code, response envelope, and the auth/CSRF rejection paths |
| A component | Renders with real props; the loading, empty and error states where it has them |
| A bug fix | A test that fails before the fix. If you cannot write one, say so in the PR and explain why |

Anything touching auth, sessions, CSRF, the visibility predicate or `apply_url`
needs a test for the **rejection** path, not only the acceptance path. Those are
the paths that protect people, and a control nobody tested is a control nobody
knows works.

Record what you ran. A test whose result was not written down did not happen, and
a test that was never run is not a pass.

---

## Documentation is part of the change

| You changed | Also update |
|---|---|
| An endpoint, a shape, a status code, an error code | [API-CONTRACT.md](API-CONTRACT.md) **first**, then [API.md](API.md) |
| The schema, an index, a constraint | [DATABASE.md](DATABASE.md) |
| An environment variable | `.env.example` **and** the table in [DEPLOYMENT.md](DEPLOYMENT.md#7-environment-variables) |
| A deploy step, a hosting setting | [DEPLOYMENT.md](DEPLOYMENT.md) |
| Anything about auth, cookies, CORS, secrets | [SECURITY.md](SECURITY.md) |
| A technology choice, or the reversal of one | A new record in [DECISIONS.md](DECISIONS.md). Do not rewrite an existing record — supersede it, and say which one it replaces |
| A failure you spent more than an hour on | [TROUBLESHOOTING.md](TROUBLESHOOTING.md), symptom first. This is the highest-value documentation in the repository, because it is the only kind nobody can reconstruct later |

Do not put a real credential, hostname or connection string in any document. Use
obvious placeholders: `postgres://USER:PASSWORD@HOST/DB`,
`changeme-generate-a-32-byte-random-string`, `https://example.com/careers/role`.

---

## Pre-PR checklist

- [ ] Branch named `<type>/<description>`, branched from `main`
- [ ] Commits are Conventional, one logical change each
- [ ] `cd backend && gofmt -l .` prints nothing
- [ ] `go vet ./...` and `go test ./...` pass
- [ ] `npm run check`, `npm run build` and `npm run test` pass
- [ ] `go.sum` is committed if dependencies changed
- [ ] **The contract was changed first** if the API changed
- [ ] `.env.example` updated if a variable was added, with a placeholder value
- [ ] Affected documents updated in the same PR
- [ ] Tests added, including a rejection case for anything security-relevant
- [ ] New UI checked from 320px to 2560px, and keyboard-navigable
- [ ] No `any`, no raw hex outside `tokens.css`, no magic constant, no hardcoded hostname
- [ ] **No secret in the diff** — read it line by line, do not skim. `.env`, `*.pem`, `*.key`, a service-account JSON, a connection string with a password, a session token, a cookie jar (`.jp-cookies.txt`), a database dump
- [ ] No debug output left behind: no `console.log`, no `fmt.Println`, no commented-out block "for later"
- [ ] The PR description says **why**, not only what, and lists what you actually ran

If a secret ever does land in a commit, do not quietly remove it. Follow
[SECURITY.md](SECURITY.md#if-a-secret-is-exposed): rotate first, tidy second. A
rewritten history does not un-disclose a value.

---

## Review

- Every change is reviewed before merge. Self-merging is for the solo maintainer
  and even then the CI gates apply.
- Reviewers check correctness, layering, the code-quality rules above,
  accessibility, and whether the documentation moved with the code.
- Small PRs get reviewed. A 2,000-line PR gets skimmed, which is worse than not
  being reviewed at all because it looks like it was.
- Squash on merge. `main` reads as one commit per logical change.
