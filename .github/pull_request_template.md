<!--
  Job Paglu pull-request template.

  Purpose: make a reviewer's first question ("what is this and how do I know it
  works?") answerable without a round trip. Delete any section that genuinely
  does not apply - an honest "n/a" is better than a box ticked out of habit.
-->

## What and why

<!-- One or two sentences. What changes, and what problem it solves. Link the
     issue if there is one. If this is a follow-up, say what it follows up. -->

## How it was verified

<!-- Be specific and be honest. "CI is green" is a fact; "should work" is not a
     verification. If something could not be run, say so and say why - an
     unverified change that is labelled unverified is fine, an unverified change
     labelled tested is not. -->

- [ ] `cd frontend && npm run check` (astro check / typecheck)
- [ ] `cd frontend && npm run build`
- [ ] `cd frontend && npm run test`
- [ ] `cd backend && go vet ./... && go test ./... -race -count=1`
- [ ] `cd backend && go test -tags=integration -count=1 ./...` against a real Postgres
- [ ] Exercised by hand in a browser (say which pages, and at which widths)
- [ ] Not verified locally - relying on CI (explain below)

Notes:

## Contract and data

- [ ] No change to `docs/API-CONTRACT.md`
- [ ] `docs/API-CONTRACT.md` changed, **and this PR changes both sides to match**

<!-- The contract is normative: it is what lets the frontend and backend be
     written independently. Changing one side to fit the other without updating
     the contract is how the two silently diverge. If you changed the contract,
     say here what a consumer has to do. -->

- [ ] No database migration
- [ ] Adds a migration. It is additive and backward-compatible with the
      currently deployed backend (a rolling deploy runs old and new code against
      the same schema for a while).
- [ ] Adds a destructive or non-backward-compatible migration - deployment order
      is described below.

## Security

<!-- Only the items that this diff actually touches. Untouched areas do not need
     a tick; a review is not a compliance exercise. -->

- [ ] No new SQL. Any new SQL uses `$n` placeholders exclusively - no value is
      concatenated or formatted into a query string.
- [ ] No new HTML sink. Nothing new renders untrusted text through `set:html` /
      `dangerouslySetInnerHTML`.
- [ ] `apply_url` handling unchanged, or: the allowlist (scheme, host, userinfo,
      length, control characters) is still applied on **write and on render**.
- [ ] Every new `/api/admin/*` route goes through session auth, and every new
      state-changing one through the CSRF check.
- [ ] No secret, token, connection string or password in the diff - including in
      a test fixture, a comment, a log line, or an example in documentation.
- [ ] New environment variables are documented in `.env.example` (or
      `frontend/.env.example`) with placeholders only, and read through the
      existing config validation so a missing value fails fast at startup.
- [ ] No new error response leaks internal detail (SQL text, file path, stack
      frame, internal hostname).

## Frontend only

- [ ] No framework JavaScript added to a public page. React stays inside
      `/admin`. (If a public page now ships an island, justify it here.)
- [ ] Loading, empty and error states exist for any new API-driven view.
- [ ] Checked at 320px and at 1920px: no horizontal scroll, no overlap, touch
      targets still >= 44px.
- [ ] Keyboard-reachable, focus visible, headings still in order, new controls
      labelled. Anything announced to a screen reader uses `aria-live` rather
      than colour or position alone.
- [ ] Colour comes from `src/styles/tokens.css` - no raw hex, no magic px.
- [ ] SEO surface unaffected, or: title/description/canonical/OG and any
      `JobPosting` markup are still correct and still generated from the real
      record.

## Backend only

- [ ] Errors use the closed code set from the contract; no new code invented
      without adding it to the contract.
- [ ] The public job query still cannot return a draft, unpublished,
      soft-deleted or expired row - the predicate is in the SQL, not in the
      handler.
- [ ] Anything long-running respects the request context (no unbounded query,
      no ignored `ctx`).

## Deployment

- [ ] Nothing to do - merge and deploy normally.
- [ ] Needs a new environment variable / secret set on a Vercel project (web and/or api)
      Pages **before** the deploy. Listed by name below (never the value).
- [ ] Needs migrations applied first, or in a specific order. Described below.

Notes:

## Anything a reviewer should push back on

<!-- Say what you are least sure about, or what you chose against. This is the
     most useful box on the form and the one most often left empty. -->
