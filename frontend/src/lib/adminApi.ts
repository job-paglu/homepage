/**
 * Typed wrappers for every admin endpoint, called from the browser.
 *
 * One function per endpoint, each returning the unwrapped `data` and throwing
 * `ApiError` on anything else. This layer deliberately does **not** catch,
 * translate, or soften errors: the UI decides whether a 422 becomes inline field
 * messages, a toast, or a full-page notice, and it can only do that if the
 * original `code` and `details` reach it intact.
 *
 * Every path is site-relative, so these are same-origin requests to the Worker
 * that served the page. See `api.ts` for why there is no base URL.
 *
 * ---------------------------------------------------------------------------
 * CSRF: there is no token here, and `security.checkOrigin` must stay ON.
 * ---------------------------------------------------------------------------
 * The old `X-CSRF-Token` header is gone because the token it echoed came from a
 * session cookie this application no longer sets. What replaced it is NOT
 * "nothing".
 *
 * Cloudflare Access authenticates with the `CF_Authorization` **cookie**, and a
 * browser attaches a cookie to a cross-site request just as willingly as to a
 * same-site one. So a `POST /api/admin/jobs` forged by `evil.example` would
 * arrive at this Worker *already authenticated*. Two things stop it, and both are
 * load-bearing:
 *
 *   1. `security.checkOrigin: true` in `astro.config.mjs`. Astro rejects a
 *      state-changing request whose `Origin` does not match the host. This is
 *      what covers the dangerous case — a form-encoded POST, which a browser
 *      will send cross-site with no preflight to ask permission for.
 *   2. The absence of any CORS response header. Every write below is a JSON
 *      body or a `DELETE`, neither of which is a CORS "simple request", so the
 *      browser must preflight it — and this Worker answers no
 *      `Access-Control-Allow-*`, so the real request is never sent.
 *
 * Neither needs code on this side: the browser sets `Origin` on our same-origin
 * writes automatically, and same-origin requests are never preflighted. That is
 * exactly why they are easy to delete by accident. **Turning `checkOrigin` off,
 * or adding CORS headers to `/api/admin/*`, removes this application's entire
 * CSRF defence.** Do not switch a write to a form content type either — that
 * moves it out of protection 2 and leaves it relying on 1 alone.
 *
 * There is also no `login` and no `logout` here. Access authenticates at the
 * edge before a request reaches this project, so there is nothing to post
 * credentials to; signing out is Access's own endpoint (`adminIdentity.ts`).
 */

import { apiFetch, buildQuery } from './api';
import type { ApiFetchOptions } from './api';
import type {
  AdminJobListQuery,
  ApiCollection,
  ApiSuccess,
  Job,
  JobInput,
  JobSummary,
  Pagination,
} from './types';

/**
 * The whole of an admin identity, as `GET /api/admin/me` returns it.
 *
 * `email` and `sub` and nothing else. Cloudflare Access is the identity
 * provider now: there is no `admin_users` row, so there is no `id`, no `name`
 * and no `last_login_at` to report. The shape lives here rather than in
 * `types.ts` because it is a response body, and `types.ts` still mirrors the
 * older Go contract.
 */
export interface AdminIdentity {
  readonly email: string;
  /** Access's stable per-user identifier. Useful in a log line, not in the UI. */
  readonly sub: string;
}

/**
 * Admin calls get a slightly longer budget than the 10s default: a cold isolate
 * plus a first D1 query is the slowest request of a session, and timing out on
 * it makes a healthy service look broken. There is no outer platform deadline to
 * stay under — a Worker is bounded by CPU time, not wall-clock.
 */
const ADMIN_TIMEOUT_MS = 12_000;

/** Options a caller may pass through to any admin read. */
export type AdminReadOptions = Pick<ApiFetchOptions, 'signal' | 'timeoutMs'>;

export interface AdminJobListResult {
  jobs: JobSummary[];
  pagination: Pagination;
}

// ----------------------------------------------------------------- primitives

/** Authenticated read. `no-store` so admin data never lands in a disk cache. */
function read<T>(path: string, options: AdminReadOptions = {}): Promise<T> {
  return apiFetch<T>(path, {
    timeoutMs: ADMIN_TIMEOUT_MS,
    cache: 'no-store',
    ...options,
  });
}

/** Authenticated state change. Authenticated by the Access cookie the browser
 *  attaches, guarded by the `Origin` check described in the header. */
function write<T>(path: string, method: 'POST' | 'PUT' | 'DELETE', body?: unknown): Promise<T> {
  return apiFetch<T>(path, {
    method,
    timeoutMs: ADMIN_TIMEOUT_MS,
    ...(body === undefined ? {} : { body }),
  });
}

/** IDs are UUIDs from the API, but encoding them costs nothing and closes a hole. */
function jobPath(id: string, suffix = ''): string {
  return `/api/admin/jobs/${encodeURIComponent(id)}${suffix}`;
}

// ------------------------------------------------------------------ identity

/**
 * GET /api/admin/me.
 *
 * A 401 here is the authoritative "Cloudflare Access has not authenticated this
 * browser" — the only auth signal the dashboard has, and the only one it needs.
 */
export async function getMe(options: AdminReadOptions = {}): Promise<AdminIdentity> {
  const result = await read<ApiSuccess<{ admin: AdminIdentity }>>('/api/admin/me', options);
  return result.data.admin;
}

// ---------------------------------------------------------------------- jobs

/**
 * GET /api/admin/jobs — all statuses, ignores expiry, `updated_at DESC`.
 *
 * `status` must be omitted rather than sent empty for "any status"; `buildQuery`
 * drops undefined and empty values, so the caller can pass a partially filled
 * query object without special-casing.
 */
export async function listAdminJobs(
  query: AdminJobListQuery = {},
  options: AdminReadOptions = {},
): Promise<AdminJobListResult> {
  const search = buildQuery({
    page: query.page,
    per_page: query.per_page,
    q: query.q,
    location: query.location,
    country: query.country,
    job_type: query.job_type,
    remote_type: query.remote_type,
    featured: query.featured,
    status: query.status,
    include_deleted: query.include_deleted,
  });
  const result = await read<ApiCollection<JobSummary>>(`/api/admin/jobs${search}`, options);
  return { jobs: result.data, pagination: result.meta };
}

/** GET /api/admin/jobs/{id} — by UUID, so an edit survives a slug change. */
export async function getAdminJob(id: string, options: AdminReadOptions = {}): Promise<Job> {
  const result = await read<ApiSuccess<Job>>(jobPath(id), options);
  return result.data;
}

/** POST /api/admin/jobs. Omit `slug` in the input to let the server derive it. */
export async function createJob(input: JobInput): Promise<Job> {
  const result = await write<ApiSuccess<Job>>('/api/admin/jobs', 'POST', input);
  return result.data;
}

/**
 * PUT /api/admin/jobs/{id}. Full replacement of the mutable fields.
 *
 * Leaving `slug` out of `input` keeps the existing slug even when the title
 * changed — that is the contract's slug-stability guarantee, and the reason the
 * form makes changing a URL an explicit opt-in.
 */
export async function updateJob(id: string, input: JobInput): Promise<Job> {
  const result = await write<ApiSuccess<Job>>(jobPath(id), 'PUT', input);
  return result.data;
}

/** DELETE /api/admin/jobs/{id}. Soft delete: the row survives and is recoverable. */
export async function deleteJob(id: string): Promise<void> {
  await write<void>(jobPath(id), 'DELETE');
}

/**
 * POST /api/admin/jobs/{id}/publish.
 *
 * Can legitimately fail with 422 when the job would be invalid in public — most
 * often a missing or non-https `apply_url`. The caller must surface that rather
 * than treat it as a transient error.
 */
export async function publishJob(id: string): Promise<Job> {
  const result = await write<ApiSuccess<Job>>(jobPath(id, '/publish'), 'POST');
  return result.data;
}

/** POST /api/admin/jobs/{id}/unpublish. `published_at` is left intact. */
export async function unpublishJob(id: string): Promise<Job> {
  const result = await write<ApiSuccess<Job>>(jobPath(id, '/unpublish'), 'POST');
  return result.data;
}
