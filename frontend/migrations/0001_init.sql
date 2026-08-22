-- 0001_init.sql — baseline D1 (SQLite) schema for Job Paglu.
--
-- Ported from backend/migrations/001_init.sql + 002_indexes.sql. The LOGICAL
-- schema is unchanged, so API output is byte-identical; the dialect is not.
-- Deltas versus Postgres, all deliberate:
--
--   * id        uuid DEFAULT gen_random_uuid()  ->  TEXT PRIMARY KEY, generated
--               by the application with crypto.randomUUID(). SQLite has no UUID
--               type and no uuid generator, and D1 has no extensions.
--   * time      timestamptz  ->  TEXT holding RFC 3339 UTC with a Z suffix and
--               NO fractional seconds, e.g. '2026-08-21T09:41:07Z'. That is the
--               format API-CONTRACT.md section 1 already mandates, so the wire
--               format is unchanged. Fixed-width and Z-normalised means plain
--               lexicographic string comparison IS chronological comparison,
--               which is what makes `expires_at > ?` correct. Anything that
--               writes a value here must go through the one formatter in
--               src/lib/server/validate.ts — a stray '.123Z' would break
--               ordering inside a single second.
--   * boolean   is_featured boolean  ->  INTEGER NOT NULL DEFAULT 0. SQLite has
--               no boolean. The 0/1 is converted at the repository boundary and
--               never reaches JSON (contract section 2.1 says `boolean`).
--   * bigint    view_count/apply_click_count  ->  INTEGER (SQLite INTEGER is
--               already 64-bit).
--   * now()     -> removed. Every timestamp is bound as a parameter by the
--               service, which computes "now" once per request so a create and
--               its validation cannot disagree about it.
--   * pg_trgm   -> removed. `q` is a LIKE scan (see the note on indexes below).
--   * advisory  -> not needed. The Postgres version relied on the unique index
--     locks     alone for slug de-duplication, and D1 has a single writer per
--               database, so the read-then-insert in the slug de-duplicator
--               cannot interleave with another writer at all.
--   * admin_users / sessions tables -> DELETED ENTIRELY. Authentication is
--               Cloudflare Access, so this application stores no password hash,
--               no session token and no CSRF token. There is nothing here for a
--               database dump to turn into a login.
--
-- Every statement is idempotent so a partially-applied file is recoverable by
-- hand, exactly as in the Postgres original.

-- ---------------------------------------------------------------------- jobs

CREATE TABLE IF NOT EXISTS jobs (
    -- Application-generated UUID v4 (crypto.randomUUID()). TEXT, because the
    -- contract promises a UUID *string* and storing it as one keeps the value
    -- readable in `wrangler d1 execute` output.
    id                TEXT PRIMARY KEY,

    -- The public URL key. Not UNIQUE here: uniqueness applies to live rows only
    -- and is enforced by the partial index below, which is a different rule.
    slug              TEXT NOT NULL,

    -- The seven fields the product requires of every posting.
    title             TEXT NOT NULL,
    description       TEXT NOT NULL,
    salary            TEXT NOT NULL,
    qualification     TEXT NOT NULL,
    job_type          TEXT NOT NULL,
    location          TEXT NOT NULL,
    apply_url         TEXT NOT NULL,

    -- Nullable and unused by the current UI. They exist now so the roadmap
    -- features (search, filters, categories) are additive queries rather than a
    -- migration on a live table.
    company           TEXT,
    country           TEXT,
    remote_type       TEXT,
    experience_level  TEXT,
    category          TEXT,

    is_featured       INTEGER NOT NULL DEFAULT 0,
    status            TEXT NOT NULL DEFAULT 'draft',

    -- Counters for later analytics. Nothing writes them and the API does not
    -- return them; they are here so adding the increment is one UPDATE.
    view_count        INTEGER NOT NULL DEFAULT 0,
    apply_click_count INTEGER NOT NULL DEFAULT 0,

    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL,

    -- Set once, the first time a job becomes published, and never rewritten.
    published_at      TEXT,
    expires_at        TEXT,
    -- Soft delete. A row is never removed, so an accidental delete is
    -- recoverable and the slug becomes reusable immediately.
    deleted_at        TEXT,

    -- SQLite enforces CHECK constraints, so the Postgres design carries over
    -- unchanged: adding an enum member later is an additive migration rather
    -- than a type change (contract section 2.4).
    CONSTRAINT jobs_status_check
        CHECK (status IN ('draft', 'published', 'unpublished')),
    CONSTRAINT jobs_job_type_check
        CHECK (job_type IN ('full-time', 'part-time', 'contract', 'internship', 'freelance', 'temporary')),
    CONSTRAINT jobs_remote_type_check
        CHECK (remote_type IS NULL OR remote_type IN ('remote', 'hybrid', 'onsite')),
    -- SQLite would happily store 7 in an INTEGER column, and `is_featured === 1`
    -- in the repository would then read it as false. Pin it.
    CONSTRAINT jobs_is_featured_check
        CHECK (is_featured IN (0, 1))
);

-- Slug uniqueness among LIVE rows only.
--
-- SQLite supports partial indexes, so this is a straight port and the rule it
-- encodes survives: a plain UNIQUE would let a soft-deleted job hold its slug
-- hostage for ever, and this index is also what makes the de-duplication rule
-- (-2, -3, …) agree with the database rather than merely hope to.
CREATE UNIQUE INDEX IF NOT EXISTS jobs_slug_active ON jobs (slug) WHERE deleted_at IS NULL;

-- ------------------------------------------------------------------- indexes
--
-- Ported from 002_indexes.sql. Each one still corresponds to a query that
-- exists in src/lib/server/jobs.repo.ts; none is speculative.

-- The public list, the hottest query in the product. Partial on the two
-- constant predicates and ordered to match the ORDER BY exactly, so the common
-- unfiltered page is an index scan with no sort.
CREATE INDEX IF NOT EXISTS jobs_public_order_idx
    ON jobs (is_featured DESC, published_at DESC, created_at DESC)
    WHERE deleted_at IS NULL AND status = 'published';

-- The admin list, which orders by recency of edit across all statuses.
CREATE INDEX IF NOT EXISTS jobs_admin_order_idx
    ON jobs (updated_at DESC)
    WHERE deleted_at IS NULL;

-- The admin list's ?status= filter.
CREATE INDEX IF NOT EXISTS jobs_status_idx
    ON jobs (status)
    WHERE deleted_at IS NULL;

-- Keeps a future expiry sweep and an "expiring soon" dashboard query cheap.
CREATE INDEX IF NOT EXISTS jobs_expires_at_idx
    ON jobs (expires_at)
    WHERE deleted_at IS NULL AND expires_at IS NOT NULL;

-- The ?country= filter compares case-insensitively, so the index must too or it
-- will not be used. COLLATE NOCASE is the SQLite equivalent of the Postgres
-- version's lower(country) expression index.
CREATE INDEX IF NOT EXISTS jobs_country_nocase_idx
    ON jobs (country COLLATE NOCASE)
    WHERE deleted_at IS NULL;

-- No index for `q`.
--
-- The Postgres version used a pg_trgm GIN index to make `ILIKE '%term%'` fast.
-- D1 has no extensions and SQLite's FTS5 is a separate virtual table with its
-- own write path, which is a real feature and not a migration detail. `q` is
-- therefore an unindexed LIKE scan: correct, and fine at this table size.
-- If the job count reaches five figures, add FTS5 in its own migration rather
-- than trying to coax a b-tree into serving a leading wildcard.

-- --------------------------------------------------------------- app_flags

-- Tiny key/value table with exactly one job today: gating the development seed
-- in 0002_seed_dev.sql.
--
-- A .sql file cannot read APP_ENV, and `wrangler d1 migrations apply` applies
-- every file in this directory — including the seed — wherever it is pointed.
-- So the opt-in has to live in the database itself. 0002 inserts nothing unless
-- a row ('allow_dev_seed', '1') exists here, and creating that row is a manual,
-- explicit act that nobody performs against production by accident.
CREATE TABLE IF NOT EXISTS app_flags (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
