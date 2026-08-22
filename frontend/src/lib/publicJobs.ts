/**
 * How public pages get job data: by calling the service, in-process.
 *
 * ###########################################################################
 * #  SERVER-ONLY. Never import this from a `client:*` island — it pulls the  #
 * #  D1 repository into the bundle. Browser code uses `src/lib/api.ts`.      #
 * ###########################################################################
 *
 * Why not fetch `/api/jobs` over HTTP
 * -----------------------------------
 * The page and the API are the SAME Worker. A page that fetched its own
 * `/api/jobs` would be a Worker calling itself, which is worse in four separate
 * ways, and one of them is a hard limit rather than a preference:
 *
 *   1. it spends a subrequest, and the Workers free plan allows 50 per request —
 *      `sitemap.xml` walks up to 20 pages of jobs and would burn 20 of them;
 *   2. it pays a full HTTP round trip (serialise, dispatch, parse) to reach code
 *      that is already loaded in the same isolate;
 *   3. self-`fetch` can deadlock on some runtimes, because the invocation is
 *      waiting on itself;
 *   4. it needs an origin to fetch, and an origin is a thing that can be
 *      configured wrongly — which is exactly how `/`, `/jobs`, `/jobs/{slug}`
 *      and `sitemap.xml` all ended up serving their error state instead of data.
 *
 * Calling the service directly removes all four. A missing base URL cannot
 * break a call that has no URL.
 *
 * This module is a thin adapter and nothing else: it fills in the `ListQuery`
 * fields the public site does not use, unwraps the service's `CollectionResult`
 * into the `{ jobs, pagination }` shape the pages render, and turns a thrown
 * server error into visitor-safe text. Business rules stay in
 * `src/lib/server/jobs.service.ts`.
 */

import { copy } from './copy';
import { getRuntimeEnv, type RuntimeEnv } from './server/env';
import { createJobService, MAX_PER_PAGE } from './server/jobs.service';
import { ApiError } from './server/respond';
import type { ListQuery } from './server/validate';
import type { Job, JobSummary, Pagination } from './types';

/** Re-exported so a page needs one import to go from `Astro.locals` to data. */
export { getRuntimeEnv, MAX_PER_PAGE };
export type { RuntimeEnv };

export interface PublicListQuery {
  page?: number;
  per_page?: number;
}

export interface PublicJobList {
  jobs: JobSummary[];
  pagination: Pagination;
}

/**
 * The public site exposes no search or filter UI yet (the API supports both —
 * see the project's *Extension Points*), so every filter field is explicitly
 * neutral here. Spelling them out rather than casting a partial object keeps the
 * day a field is added a compile error instead of a silent `undefined` reaching
 * the SQL builder.
 */
function neutralQuery(page: number, perPage: number): ListQuery {
  return {
    page,
    perPage,
    q: '',
    location: '',
    country: '',
    jobType: '',
    remoteType: '',
    featured: null,
    status: '',
    includeDeleted: false,
  };
}

/**
 * Published, non-deleted, non-expired jobs. Clamping and the visibility
 * predicate belong to the service and the repository respectively; nothing here
 * can widen either.
 */
export async function listPublishedJobs(
  env: RuntimeEnv,
  query: PublicListQuery = {},
): Promise<PublicJobList> {
  const { data, meta } = await createJobService(env).listPublic(
    neutralQuery(query.page ?? 1, query.per_page ?? 0),
  );
  return { jobs: data, pagination: meta };
}

/**
 * One job by slug, or `null` when it is not publicly visible.
 *
 * `null` covers "no such slug" AND "draft / unpublished / soft-deleted /
 * expired", indistinguishably and on purpose (contract section 3): an admin's
 * unpublished slug must not be discoverable by probing for a different answer.
 * The caller turns `null` into a 404 — see `src/pages/jobs/[slug].astro`.
 *
 * Every other failure is re-thrown, because "the database is unreachable" and
 * "this job does not exist" must not render the same page.
 */
export async function getPublishedJob(env: RuntimeEnv, slug: string): Promise<Job | null> {
  const trimmed = slug.trim();
  if (trimmed === '') return null;
  try {
    return await createJobService(env).getPublic(trimmed);
  } catch (error) {
    if (error instanceof ApiError && error.code === 'JOB_NOT_FOUND') return null;
    throw error;
  }
}

// ---------------------------------------------------------------- failures

export interface PageFailure {
  /** Safe to render. Never a SQL fragment, hostname, or stack frame. */
  readonly message: string;
  /** Whether a "Try again" link can plausibly help. */
  readonly retryable: boolean;
}

/**
 * Turn anything thrown by the calls above into text a visitor may see, and log
 * the real cause.
 *
 * The logging is the part that matters. The bug this whole rewrite fixes was
 * invisible precisely because the pages swallowed the reason and rendered a
 * generic panel; a `console.error` here means the next occurrence shows up in
 * `wrangler tail` with its cause attached.
 *
 * `ApiError.message` is contract-guaranteed safe (section 1.2). `ApiError.reason`
 * — and any error that is not an `ApiError`, such as `ConfigError` naming
 * `wrangler.toml` — is log-only and never reaches the response.
 */
export function describeFailure(error: unknown): PageFailure {
  if (error instanceof ApiError) {
    console.error('public page data failed', {
      code: error.code,
      status: error.status,
      cause: error.reason instanceof Error ? error.reason.stack : String(error.reason ?? ''),
    });
    return { message: error.message, retryable: error.status >= 500 };
  }

  console.error('public page data failed with an unexpected error', {
    cause: error instanceof Error ? error.stack : String(error),
  });
  return { message: copy.states.errorFallbackMessage, retryable: true };
}
