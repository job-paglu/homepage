-- 001_init.sql — baseline schema for Job Paglu.
--
-- Conventions used throughout, so they are stated once:
--
--   * Every timestamp is timestamptz. The API reads and writes UTC and formats
--     RFC 3339; a naive `timestamp` column would make "now" ambiguous the first
--     time a second region is involved.
--   * updated_at is written explicitly by every UPDATE in the Go code. There is
--     deliberately no trigger: a trigger is invisible at the call site, and one
--     mechanism is easier to reason about than two.
--   * Enumerations are text with a CHECK constraint rather than a Postgres enum,
--     so adding a member later is an ALTER ... CHECK instead of a type
--     migration (API-CONTRACT section 2.4).
--   * Every statement is idempotent. The runner already skips applied versions;
--     this makes a partially-applied file recoverable by hand too.

-- gen_random_uuid() is built in from PostgreSQL 13, but the extension is
-- requested anyway so this file also works on an older server.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- The migration runner creates this table before reading it, because 001 cannot
-- both define it and be recorded in it. Repeated here so the schema is complete
-- when read as a document.
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    int PRIMARY KEY,
    applied_at timestamptz NOT NULL DEFAULT now()
);

-- ---------------------------------------------------------------------- jobs

CREATE TABLE IF NOT EXISTS jobs (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),

    -- The public URL key. Not UNIQUE here: uniqueness is enforced by a partial
    -- index below, which is a different rule.
    slug              text NOT NULL,

    -- The seven fields the product requires of every posting.
    title             text NOT NULL,
    description       text NOT NULL,
    salary            text NOT NULL,
    qualification     text NOT NULL,
    job_type          text NOT NULL,
    location          text NOT NULL,
    apply_url         text NOT NULL,

    -- Nullable and unused by the current UI. They exist now so the roadmap
    -- features (search, filters, categories) are additive queries rather than a
    -- migration on a live table.
    company           text,
    country           text,
    remote_type       text,
    experience_level  text,
    category          text,

    is_featured       boolean NOT NULL DEFAULT false,
    status            text NOT NULL DEFAULT 'draft',

    -- Counters for later analytics. Nothing writes them yet and the API does not
    -- return them; they are here so adding the increment is one UPDATE and not a
    -- schema change on a table with rows in it.
    view_count        bigint NOT NULL DEFAULT 0,
    apply_click_count bigint NOT NULL DEFAULT 0,

    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),

    -- Set once, the first time a job becomes published, and never rewritten.
    published_at      timestamptz,
    expires_at        timestamptz,
    -- Soft delete. A row is never removed, so an accidental delete is
    -- recoverable and referential history survives.
    deleted_at        timestamptz,

    CONSTRAINT jobs_status_check
        CHECK (status IN ('draft', 'published', 'unpublished')),
    CONSTRAINT jobs_job_type_check
        CHECK (job_type IN ('full-time', 'part-time', 'contract', 'internship', 'freelance', 'temporary')),
    CONSTRAINT jobs_remote_type_check
        CHECK (remote_type IS NULL OR remote_type IN ('remote', 'hybrid', 'onsite'))
);

-- Slug uniqueness among live rows only.
--
-- A plain UNIQUE constraint would let a soft-deleted job hold its slug hostage
-- for ever: deleting "senior-cloud-engineer" and creating it again would fail
-- with a conflict the admin cannot see or resolve. The partial index means a
-- deleted row's slug is immediately reusable, and it is also what makes the
-- de-duplication rule (-2, -3, ...) agree with the database.
CREATE UNIQUE INDEX IF NOT EXISTS jobs_slug_active_key
    ON jobs (slug)
    WHERE deleted_at IS NULL;

-- --------------------------------------------------------------- admin_users

CREATE TABLE IF NOT EXISTS admin_users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    -- Stored as typed, compared case-insensitively via the index below. citext
    -- would work too but needs an extension for one column's worth of benefit.
    email         text NOT NULL,
    -- A PHC-encoded Argon2id string: the cost parameters travel with the hash,
    -- so they can be raised later without invalidating existing passwords.
    password_hash text NOT NULL,
    name          text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz,
    -- Set to disable an account without deleting it, which would cascade the
    -- sessions away and lose the audit trail.
    disabled_at   timestamptz
);

CREATE UNIQUE INDEX IF NOT EXISTS admin_users_email_key
    ON admin_users (lower(email));

-- ------------------------------------------------------------------ sessions

CREATE TABLE IF NOT EXISTS sessions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    admin_id     uuid NOT NULL REFERENCES admin_users (id) ON DELETE CASCADE,

    -- SHA-256 of the session token, never the token. A dump of this table yields
    -- no usable cookie. No salt or stretching is needed: the token is 256 bits
    -- of CSPRNG output, so unlike a password it cannot be guessed.
    token_hash   bytea NOT NULL UNIQUE,

    -- Stored in plaintext on purpose: the client has to echo this value back in
    -- the X-CSRF-Token header for the comparison to mean anything.
    csrf_token   text NOT NULL,

    created_at   timestamptz NOT NULL DEFAULT now(),
    -- Idle expiry is derived from this at lookup time, so an active session does
    -- not need its expires_at rewritten on every request.
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    -- The absolute deadline: created_at + 7 days, set by the application.
    expires_at   timestamptz NOT NULL,

    -- Diagnostics only. The IP is stored already redacted by the application.
    user_agent   text,
    ip           text
);

CREATE INDEX IF NOT EXISTS sessions_admin_id_idx ON sessions (admin_id);
-- Supports the expiry sweep, which is a range scan over a table that is mostly
-- expired rows by the time it runs.
CREATE INDEX IF NOT EXISTS sessions_expires_at_idx ON sessions (expires_at);
