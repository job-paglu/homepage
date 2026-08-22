-- 0002_seed_dev.sql — obviously-fake development rows.
--
-- ==========================================================================
-- HOW THIS IS GUARDED, AND WHY THE GUARD LOOKS LIKE THIS
-- --------------------------------------------------------------------------
-- `wrangler d1 migrations apply` applies every file in migrations/ against
-- whichever database it is pointed at. It cannot read APP_ENV, and a .sql file
-- has no way to ask. So a comment saying "development only" would be a wish,
-- not a guard.
--
-- Instead every statement below is `INSERT … SELECT … WHERE EXISTS (a flag row)`.
-- With the flag absent, each statement inserts zero rows and the migration is
-- recorded as applied — so production and preview get the schema and nothing
-- else, and the migration ledger stays in step across environments.
--
-- To actually seed a LOCAL database, opt in explicitly first:
--
--   wrangler d1 execute job-paglu --local --command \
--     "INSERT OR REPLACE INTO app_flags (key, value) VALUES ('allow_dev_seed','1')"
--   wrangler d1 migrations apply job-paglu --local
--
-- and to stop seeding again:
--
--   wrangler d1 execute job-paglu --local --command \
--     "DELETE FROM app_flags WHERE key = 'allow_dev_seed'"
--
-- Ids are fixed and `INSERT OR IGNORE`, so re-running is idempotent rather than
-- duplicating the fixtures.
--
-- Every row is unmistakably fake: `deadbeef-…` ids, `example.com` apply links
-- (a reserved documentation domain that cannot be registered), and "DEV SEED"
-- in the company name. Nothing here can be mistaken for a real posting, and no
-- seeded row is ever used to satisfy the JobPosting structured-data requirement.
-- ==========================================================================

-- Published + featured. The list's ordering rule puts this first.
INSERT OR IGNORE INTO jobs (
    id, slug, title, description, salary, qualification, job_type, location,
    apply_url, company, country, remote_type, experience_level, category,
    is_featured, status, created_at, updated_at, published_at, expires_at, deleted_at
)
SELECT
    'deadbeef-0000-4000-8000-000000000001',
    'dev-seed-senior-platform-engineer',
    'DEV SEED — Senior Platform Engineer',
    'This is a development fixture, not a real vacancy. It exists so the job list, the detail page and the JobPosting structured data can be rendered locally without an administrator having to type a posting first. The text is deliberately long enough to exercise the 200-character excerpt rule and its word-boundary cut.',
    '$140k - $175k (fake)',
    'B.Sc. Computer Science or equivalent experience',
    'full-time',
    'Berlin, Germany',
    'https://example.com/dev-seed/senior-platform-engineer',
    'EXAMPLE GmbH (DEV SEED)',
    'Germany',
    'hybrid',
    '5+ years',
    'Engineering',
    1, 'published',
    '2026-08-20T10:58:12Z', '2026-08-20T11:00:00Z', '2026-08-20T11:00:00Z', NULL, NULL
WHERE EXISTS (SELECT 1 FROM app_flags WHERE key = 'allow_dev_seed' AND value = '1');

-- Published, not featured, fully remote, no company. Covers the nullable
-- columns and the "featured jobs ahead of the rest" half of the ordering rule.
INSERT OR IGNORE INTO jobs (
    id, slug, title, description, salary, qualification, job_type, location,
    apply_url, company, country, remote_type, experience_level, category,
    is_featured, status, created_at, updated_at, published_at, expires_at, deleted_at
)
SELECT
    'deadbeef-0000-4000-8000-000000000002',
    'dev-seed-technical-writer',
    'DEV SEED — Technical Writer (Remote)',
    'A second development fixture. Short enough that no excerpt truncation happens, which is the case the first row does not cover.',
    'EUR 55,000 (fake)',
    'Portfolio of published technical documentation',
    'contract',
    'Remote — Europe',
    'https://example.com/dev-seed/technical-writer',
    NULL, NULL, 'remote', NULL, NULL,
    0, 'published',
    '2026-08-19T08:00:00Z', '2026-08-19T08:30:00Z', '2026-08-19T08:30:00Z', NULL, NULL
WHERE EXISTS (SELECT 1 FROM app_flags WHERE key = 'allow_dev_seed' AND value = '1');

-- Draft. MUST NOT appear on any public endpoint — this is the row that proves
-- the visibility predicate is actually applied.
INSERT OR IGNORE INTO jobs (
    id, slug, title, description, salary, qualification, job_type, location,
    apply_url, company, country, remote_type, experience_level, category,
    is_featured, status, created_at, updated_at, published_at, expires_at, deleted_at
)
SELECT
    'deadbeef-0000-4000-8000-000000000003',
    'dev-seed-unfinished-draft',
    'DEV SEED — Unfinished Draft',
    'A draft fixture. If this ever shows up on /jobs or resolves at /jobs/dev-seed-unfinished-draft, the public visibility predicate has been bypassed and that is a security bug, not a display bug.',
    'TBC (fake)',
    'Not yet decided',
    'internship',
    'London, United Kingdom',
    'https://example.com/dev-seed/unfinished-draft',
    'EXAMPLE Ltd (DEV SEED)',
    'United Kingdom',
    'onsite', NULL, NULL,
    0, 'draft',
    '2026-08-21T09:00:00Z', '2026-08-21T09:00:00Z', NULL, NULL, NULL
WHERE EXISTS (SELECT 1 FROM app_flags WHERE key = 'allow_dev_seed' AND value = '1');

-- Published but EXPIRED. Also must not appear publicly, and unlike the draft it
-- exercises the `expires_at` half of the predicate rather than the `status` half.
-- The date is in the past on purpose and must stay that way.
INSERT OR IGNORE INTO jobs (
    id, slug, title, description, salary, qualification, job_type, location,
    apply_url, company, country, remote_type, experience_level, category,
    is_featured, status, created_at, updated_at, published_at, expires_at, deleted_at
)
SELECT
    'deadbeef-0000-4000-8000-000000000004',
    'dev-seed-expired-listing',
    'DEV SEED — Expired Listing',
    'An expired fixture. It is published, not deleted, and still invisible to every public endpoint because its expires_at is in the past. Keep that date in the past when editing this file.',
    'USD 90,000 (fake)',
    'Any',
    'temporary',
    'Toronto, Canada',
    'https://example.com/dev-seed/expired-listing',
    'EXAMPLE Inc (DEV SEED)',
    'Canada',
    NULL, NULL, NULL,
    0, 'published',
    '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z', '2026-01-02T00:00:00Z', '2026-02-01T00:00:00Z', NULL
WHERE EXISTS (SELECT 1 FROM app_flags WHERE key = 'allow_dev_seed' AND value = '1');
