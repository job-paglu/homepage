-- =============================================================================
-- Job Paglu - DEVELOPMENT SEED DATA
-- =============================================================================
--
--   !!  DO NOT RUN THIS AGAINST PRODUCTION, STAGING, OR ANY DATABASE WHOSE  !!
--   !!  CONTENTS SOMEONE WOULD MISS. IT IS FAKE CONTENT FOR A THROWAWAY     !!
--   !!  LOCAL DATABASE AND NOTHING ELSE.                                    !!
--
-- Every row below is self-evidently fictional: the companies are named
-- "Example", "Placeholder", "Fictional" and so on, every apply_url is on
-- example.com (an IANA-reserved domain that can never belong to a real
-- employer), and every title is marked as sample or demo data. That is not
-- decoration - it is the last line of defence. If one of these rows ever
-- escapes onto a public page, a visitor must be able to tell at a glance that
-- it is not a real job, and must not be able to click through to a real site.
--
-- THREE INDEPENDENT GUARDS PROTECT THIS FILE:
--   1. scripts/dev.sh and scripts/dev.ps1 refuse to load it unless APP_ENV is
--      exactly 'development'.
--   2. The DO block below aborts if this database already holds any job whose
--      apply_url is not on example.com - i.e. it refuses to touch a database
--      that contains real listings, whatever APP_ENV happens to say.
--   3. Fixed UUIDs plus a scoped DELETE make it idempotent, so a re-run
--      replaces the seed rows and never touches anything else.
--
-- Purpose beyond "some rows to look at": the set deliberately covers every
-- state the public visibility predicate has to filter on -
--   status = 'published' AND deleted_at IS NULL
--       AND (expires_at IS NULL OR expires_at > now())
-- so a draft, an unpublished job, an expired job and a soft-deleted job are all
-- present. If any of them shows up on /jobs, the predicate is wrong. That is a
-- bug you want to find locally rather than in production.
--
-- Run it:
--   ./scripts/dev.sh --seed            (bash)
--   .\scripts\dev.ps1 -Seed            (PowerShell)
-- Or directly, with the guard still active:
--   docker compose exec -T db psql -v ON_ERROR_STOP=1 -U jobpaglu -d jobpaglu \
--       < scripts/seed-dev.sql
--
-- Requires the migrations to have been applied first (the `jobs` table must
-- exist). scripts/dev.* does that for you.
-- =============================================================================

BEGIN;

-- -----------------------------------------------------------------------------
-- GUARD: refuse to run against a database that holds real data.
-- -----------------------------------------------------------------------------
-- APP_ENV is a shell variable and SQL cannot read it, so the shell guard can be
-- bypassed by running this file by hand. This check cannot: it looks at what is
-- actually in the table. Any job that does not point at example.com is treated
-- as real, and the transaction aborts.
DO $guard$
DECLARE
  real_jobs bigint;
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM information_schema.tables
    WHERE table_schema = current_schema() AND table_name = 'jobs'
  ) THEN
    RAISE EXCEPTION
      'Table "jobs" does not exist. Apply migrations first: ./scripts/migrate.sh (or .\scripts\migrate.ps1).';
  END IF;

  SELECT count(*) INTO real_jobs
  FROM jobs
  WHERE apply_url NOT LIKE 'https://example.com/%';

  IF real_jobs > 0 THEN
    RAISE EXCEPTION
      'REFUSING TO SEED. This database contains % job row(s) whose apply_url is not on example.com, so it holds real or hand-entered data. seed-dev.sql only ever runs against a throwaway development database.',
      real_jobs;
  END IF;
END
$guard$;

-- -----------------------------------------------------------------------------
-- Reset the seed rows only.
-- -----------------------------------------------------------------------------
-- Scoped to the fixed UUIDs below - a hard DELETE, not the application's soft
-- delete, because these rows are not history worth keeping. Nothing outside this
-- id list is touched, so the statement is safe to re-run.
DELETE FROM jobs WHERE id IN (
  '00000000-0000-4000-8000-0000000000d1',
  '00000000-0000-4000-8000-0000000000d2',
  '00000000-0000-4000-8000-0000000000d3',
  '00000000-0000-4000-8000-0000000000d4',
  '00000000-0000-4000-8000-0000000000d5',
  '00000000-0000-4000-8000-0000000000d6',
  '00000000-0000-4000-8000-0000000000d7',
  '00000000-0000-4000-8000-0000000000d8',
  '00000000-0000-4000-8000-0000000000d9'
);

-- -----------------------------------------------------------------------------
-- PUBLISHED - these are the rows that should appear on / and /jobs.
-- -----------------------------------------------------------------------------
-- Only the columns this file is sure of are listed, so any nullable or
-- future-ready column (view_count, apply_click_count, ...) keeps its schema
-- default rather than being pinned here.

INSERT INTO jobs (
  id, slug, title, description, salary, qualification, job_type, location,
  company, country, remote_type, experience_level, category, is_featured,
  apply_url, status, published_at, expires_at
) VALUES

-- 1. Featured, remote. Long description so the excerpt truncation is visible.
(
  '00000000-0000-4000-8000-0000000000d1',
  'sample-job-widget-wrangler-demo-data',
  'SAMPLE JOB: Widget Wrangler (demo data, not a real vacancy)',
  E'This is placeholder text used to develop the Job Paglu interface. It is not a real job advertisement and Example Systems GmbH is not a real company.\n\nThe paragraph exists so that the excerpt logic has more than two hundred characters to cut, and so the detail page has enough copy to check line length, line height and the reading measure at every breakpoint.\n\nWhat a fictional Widget Wrangler would do:\n- Wrangle widgets, hypothetically\n- Attend imaginary stand-ups\n- File tickets against a system that does not exist\n\nIf you are reading this on a live website, something has gone wrong: development seed data has reached production. Remove it.',
  '$120,000 - $150,000 (placeholder figure)',
  'Placeholder qualification text. No real requirement is described here.',
  'full-time',
  'Remote (Europe) - sample location',
  'Example Systems GmbH',
  'Germany',
  'remote',
  '5+ years (sample)',
  'Engineering',
  true,
  'https://example.com/careers/sample-widget-wrangler',
  'published',
  now() - interval '2 days',
  NULL
),

-- 2. Featured, hybrid, in a different country, to exercise the featured sort.
(
  '00000000-0000-4000-8000-0000000000d2',
  'demo-listing-imaginary-data-plumber',
  'DEMO LISTING: Imaginary Data Plumber (fake posting)',
  E'Development seed data. Placeholder Labs Ltd is a fictional employer invented to fill this row.\n\nUse this listing to check the hybrid badge, the country field and the featured ordering. Nothing described here corresponds to any real role, team, product or salary.\n\nResponsibilities, all fictional:\n- Move imaginary data from one imaginary place to another\n- Draw diagrams nobody will read\n- Explain, patiently, that this is not a real job',
  'GBP 65,000 - 80,000 (placeholder figure)',
  'Sample qualification field. Not a real requirement.',
  'full-time',
  'London, United Kingdom (sample location)',
  'Placeholder Labs Ltd',
  'United Kingdom',
  'hybrid',
  '3-5 years (sample)',
  'Data',
  true,
  'https://example.com/careers/demo-data-plumber',
  'published',
  now() - interval '3 days',
  NULL
),

-- 3. Not featured, onsite, contract. Short description: excerpt should NOT be
--    truncated and no ellipsis should appear.
(
  '00000000-0000-4000-8000-0000000000d3',
  'test-fixture-contract-button-polisher',
  'TEST FIXTURE: Contract Button Polisher (not a real vacancy)',
  'Short placeholder description, deliberately under two hundred characters so the excerpt is returned whole and without an ellipsis. Fictional Widgets Oy does not exist.',
  'EUR 400/day (placeholder figure)',
  'None. This is sample data.',
  'contract',
  'Helsinki, Finland (sample location)',
  'Fictional Widgets Oy',
  'Finland',
  'onsite',
  '2+ years (sample)',
  'Design',
  false,
  'https://example.com/careers/test-contract-button-polisher',
  'published',
  now() - interval '6 hours',
  NULL
),

-- 4. Internship, part-time-ish, minimal optional fields - checks that the card
--    and the detail page render correctly when company/country/remote_type are
--    NULL rather than merely empty.
(
  '00000000-0000-4000-8000-0000000000d4',
  'placeholder-internship-nothing-in-particular',
  'PLACEHOLDER: Internship in Nothing In Particular (sample row)',
  'Sample row with every optional column left NULL, so the UI has to cope with a missing company name, a missing country and a missing remote type. If any of those renders as the word "null" or as an empty badge, that is the bug this row exists to find.',
  'Unpaid (placeholder - do not model a real posting on this)',
  'Sample qualification.',
  'internship',
  'Nowhere in particular (sample location)',
  NULL,
  NULL,
  NULL,
  NULL,
  NULL,
  false,
  'https://example.com/careers/placeholder-internship',
  'published',
  now() - interval '1 day',
  NULL
),

-- 5. Published with a FUTURE expiry - must still be visible.
(
  '00000000-0000-4000-8000-0000000000d5',
  'sample-freelance-role-expires-later',
  'SAMPLE: Freelance Placeholder Role, expires next year (demo data)',
  'Development seed data with expires_at set well in the future. It must appear in public listings: the visibility predicate only hides a job once expires_at is in the past. Example Cooperative is fictional.',
  'Negotiable (placeholder)',
  'Sample qualification.',
  'freelance',
  'Remote (worldwide) - sample location',
  'Example Cooperative',
  NULL,
  'remote',
  NULL,
  'Engineering',
  false,
  'https://example.com/careers/sample-freelance-role',
  'published',
  now() - interval '5 days',
  now() + interval '365 days'
),

-- -----------------------------------------------------------------------------
-- NOT PUBLICLY VISIBLE - each row below is filtered out by a DIFFERENT clause
-- of the visibility predicate. If any of them appears on /jobs or resolves at
-- /jobs/<slug>, that clause is broken.
-- -----------------------------------------------------------------------------

-- 6. draft: filtered by `status = 'published'`.
(
  '00000000-0000-4000-8000-0000000000d6',
  'draft-should-never-be-public',
  'DRAFT (must never be publicly visible): Sample Unfinished Posting',
  'This row is a draft. It must be invisible on /jobs and must return 404 at /jobs/draft-should-never-be-public - not 403, and not an empty page, because a 403 would confirm the slug exists. Seeing it publicly means the status filter is not in the SQL.',
  'Placeholder',
  'Placeholder',
  'full-time',
  'Sample location',
  'Example Drafts Inc',
  NULL,
  NULL,
  NULL,
  NULL,
  false,
  'https://example.com/careers/draft-sample',
  'draft',
  NULL,
  NULL
),

-- 7. unpublished: was live, then withdrawn. published_at survives.
(
  '00000000-0000-4000-8000-0000000000d7',
  'unpublished-should-never-be-public',
  'UNPUBLISHED (must never be publicly visible): Sample Withdrawn Posting',
  'This row was published and then unpublished. published_at is intentionally still set, because unpublishing must not erase the original publication date. It must be invisible publicly and must still be visible in the admin list.',
  'Placeholder',
  'Placeholder',
  'part-time',
  'Sample location',
  'Example Retractions Ltd',
  NULL,
  NULL,
  NULL,
  NULL,
  false,
  'https://example.com/careers/unpublished-sample',
  'unpublished',
  now() - interval '20 days',
  NULL
),

-- 8. published but EXPIRED: filtered by the expires_at clause only. This is the
--    row that catches a predicate which checks status and deleted_at and then
--    forgets expiry.
(
  '00000000-0000-4000-8000-0000000000d8',
  'expired-should-never-be-public',
  'EXPIRED (must never be publicly visible): Sample Lapsed Posting',
  'status is published and deleted_at is NULL, so the only thing hiding this row is expires_at being in the past. If it shows up on /jobs, the expiry clause is missing from the query.',
  'Placeholder',
  'Placeholder',
  'temporary',
  'Sample location',
  'Example Expiry GmbH',
  NULL,
  NULL,
  NULL,
  NULL,
  false,
  'https://example.com/careers/expired-sample',
  'published',
  now() - interval '90 days',
  now() - interval '7 days'
),

-- 9. published but SOFT-DELETED. deleted_at is set below, after the insert, so
--    this file does not have to assume the column is nullable-with-default in
--    the INSERT column list.
(
  '00000000-0000-4000-8000-0000000000d9',
  'soft-deleted-should-never-be-public',
  'SOFT-DELETED (must never be publicly visible): Sample Removed Posting',
  'status is published and expires_at is NULL, so the only thing hiding this row is deleted_at. It must be invisible both publicly and in the default admin list, and must reappear in the admin list only with include_deleted=true.',
  'Placeholder',
  'Placeholder',
  'full-time',
  'Sample location',
  'Example Deletions BV',
  NULL,
  NULL,
  NULL,
  NULL,
  false,
  'https://example.com/careers/soft-deleted-sample',
  'published',
  now() - interval '30 days',
  NULL
);

-- Applied as an UPDATE so the INSERT above does not depend on the exact
-- definition of the deleted_at column.
UPDATE jobs
   SET deleted_at = now() - interval '1 day'
 WHERE id = '00000000-0000-4000-8000-0000000000d9';

-- -----------------------------------------------------------------------------
-- Report what was loaded, and re-assert the invariant.
-- -----------------------------------------------------------------------------
DO $report$
DECLARE
  total     bigint;
  visible   bigint;
  offenders bigint;
BEGIN
  SELECT count(*) INTO total FROM jobs;

  SELECT count(*) INTO visible
  FROM jobs
  WHERE status = 'published'
    AND deleted_at IS NULL
    AND (expires_at IS NULL OR expires_at > now());

  -- Belt and braces: if this file ever gains a row with a non-example.com apply
  -- URL, fail here rather than committing it.
  SELECT count(*) INTO offenders
  FROM jobs
  WHERE apply_url NOT LIKE 'https://example.com/%';

  IF offenders > 0 THEN
    RAISE EXCEPTION
      'seed-dev.sql produced % row(s) with an apply_url outside example.com. Every seed row must be unmistakably fake.',
      offenders;
  END IF;

  RAISE NOTICE 'Seed complete. % job row(s) total, % publicly visible.', total, visible;
  RAISE NOTICE 'Expected: 9 total, 5 publicly visible (1 draft, 1 unpublished, 1 expired, 1 soft-deleted are correctly hidden).';
  RAISE NOTICE 'All apply URLs are on example.com. Nothing here is a real job.';
END
$report$;

COMMIT;
