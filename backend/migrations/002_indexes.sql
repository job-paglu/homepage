-- 002_indexes.sql — the indexes the real queries need.
--
-- Separated from 001 so the table definitions stay readable and so an index can
-- be added, measured and reverted without touching the schema file. Every index
-- here corresponds to a query that exists in internal/jobs/repository.go; none is
-- speculative.

-- The public list, which is the hottest query in the product:
--
--   WHERE status = 'published' AND deleted_at IS NULL AND (expires_at IS NULL OR expires_at > now())
--   ORDER BY is_featured DESC, published_at DESC NULLS LAST, created_at DESC
--
-- Partial on the two constant predicates, and ordered to match the ORDER BY
-- exactly, so the common unfiltered page is an index scan with no sort at all.
-- The expires_at test is not indexable here because now() is not stable across
-- statements; it is a cheap filter on an already-tiny candidate set.
CREATE INDEX IF NOT EXISTS jobs_public_order_idx
    ON jobs (is_featured DESC, published_at DESC NULLS LAST, created_at DESC)
    WHERE deleted_at IS NULL AND status = 'published';

-- The admin list, which orders by recency of edit across all statuses.
CREATE INDEX IF NOT EXISTS jobs_admin_order_idx
    ON jobs (updated_at DESC)
    WHERE deleted_at IS NULL;

-- The admin list's ?status= filter.
CREATE INDEX IF NOT EXISTS jobs_status_idx
    ON jobs (status)
    WHERE deleted_at IS NULL;

-- Supports a future expiry sweep, and keeps the "expiring soon" query cheap
-- whenever the dashboard grows one.
CREATE INDEX IF NOT EXISTS jobs_expires_at_idx
    ON jobs (expires_at)
    WHERE deleted_at IS NULL AND expires_at IS NOT NULL;

-- The ?country= filter compares lower(country), so the index has to as well or
-- it will not be used.
CREATE INDEX IF NOT EXISTS jobs_country_lower_idx
    ON jobs (lower(country))
    WHERE deleted_at IS NULL;

-- ------------------------------------------------------- free-text search (q)
--
-- `q` is an unanchored, case-insensitive ILIKE '%term%'. A b-tree cannot help
-- with a leading wildcard; a trigram GIN index can, and turns a sequential scan
-- into an index scan once the table is more than a few hundred rows.
--
-- Wrapped in a DO block with an exception handler because CREATE EXTENSION needs
-- privileges the application role may not have. Neon grants them, but a
-- developer's local Postgres or a locked-down managed instance may not, and a
-- missing search index must not stop a deploy: the query still works without it,
-- just slower. Each migration runs in its own transaction, so without this guard
-- a permission error here would abort the whole file.
DO $$
BEGIN
    CREATE EXTENSION IF NOT EXISTS pg_trgm;

    CREATE INDEX IF NOT EXISTS jobs_title_trgm_idx       ON jobs USING gin (title gin_trgm_ops);
    CREATE INDEX IF NOT EXISTS jobs_company_trgm_idx     ON jobs USING gin (company gin_trgm_ops);
    CREATE INDEX IF NOT EXISTS jobs_location_trgm_idx    ON jobs USING gin (location gin_trgm_ops);
    CREATE INDEX IF NOT EXISTS jobs_description_trgm_idx ON jobs USING gin (description gin_trgm_ops);

EXCEPTION
    WHEN insufficient_privilege THEN
        RAISE NOTICE 'pg_trgm is unavailable to this role; text search will use a sequential scan';
    WHEN feature_not_supported THEN
        RAISE NOTICE 'pg_trgm is not installable on this server; text search will use a sequential scan';
END
$$;
