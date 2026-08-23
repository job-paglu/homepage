-- 0003_external_id.sql — the webhook ingestion idempotency key.
--
-- Adds `external_id`, the CALLER'S own id for a posting (an ATS id, a scraper's
-- record id, …). It is what makes POST /api/webhook/jobs idempotent: a second
-- delivery of the same posting UPDATEs the existing row instead of inserting a
-- duplicate. Nothing on the admin path reads or writes it; it is null for every
-- admin-created job.
--
-- Unlike 0001/0002 this file is NOT fully idempotent, and it cannot be: SQLite's
-- `ALTER TABLE ... ADD COLUMN` has no `IF NOT EXISTS`. Re-running it errors with
-- "duplicate column name", which is a safe, loud failure rather than a silent
-- one. The migration runner applies each file exactly once, so this only matters
-- to a human replaying by hand — in which case, skip the ALTER if the column is
-- already present.

ALTER TABLE jobs ADD COLUMN external_id TEXT;

-- Uniqueness among LIVE rows only, mirroring `jobs_slug_active` exactly.
--
-- Partial on `deleted_at IS NULL` so a soft-deleted job FREES its external_id:
-- the source system can then re-post that id and get a fresh row rather than a
-- conflict. Also partial on `external_id IS NOT NULL` so the many admin-created
-- rows that have no external_id are not forced to collide on NULL — SQLite would
-- otherwise treat only one NULL as allowed under a plain UNIQUE... in fact SQLite
-- permits many NULLs under UNIQUE, but stating the predicate keeps the index
-- narrow (it indexes only rows that actually carry an id) and makes the intent
-- explicit alongside the slug index it is modelled on.
CREATE UNIQUE INDEX IF NOT EXISTS jobs_external_id_active
    ON jobs (external_id)
    WHERE external_id IS NOT NULL AND deleted_at IS NULL;
