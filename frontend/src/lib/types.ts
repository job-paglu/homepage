/**
 * Job Paglu API types.
 *
 * The TypeScript mirror of `docs/API-CONTRACT.md`. That document is normative;
 * if this file and the contract disagree, the contract wins and this file is
 * the bug. The Go side mirrors the same shapes in
 * `backend/internal/jobs/model.go`.
 */

// --------------------------------------------------------------- enumerations

export const JOB_STATUSES = ['draft', 'published', 'unpublished'] as const;
export type JobStatus = (typeof JOB_STATUSES)[number];

export const JOB_TYPES = [
  'full-time',
  'part-time',
  'contract',
  'internship',
  'freelance',
  'temporary',
] as const;
export type JobType = (typeof JOB_TYPES)[number];

export const REMOTE_TYPES = ['remote', 'hybrid', 'onsite'] as const;
export type RemoteType = (typeof REMOTE_TYPES)[number];

/**
 * The API may add an enum member before the frontend knows about it (contract
 * section 2.4). `LooseEnum` keeps such a value typed as a string instead of
 * making it a type error, so the UI can render it verbatim rather than crash.
 */
type LooseEnum<T extends string> = T | (string & {});

// --------------------------------------------------------------------- models

/** Returned by list endpoints. Carries `excerpt` instead of full `description`. */
export interface JobSummary {
  id: string;
  slug: string;
  title: string;
  excerpt: string;
  salary: string;
  qualification: string;
  job_type: LooseEnum<JobType>;
  location: string;
  company: string | null;
  country: string | null;
  remote_type: LooseEnum<RemoteType> | null;
  experience_level: string | null;
  category: string | null;
  is_featured: boolean;
  apply_url: string;
  status: LooseEnum<JobStatus>;
  /**
   * The caller's own id for a webhook-ingested posting (see the "Webhook
   * ingestion" section of the contract). `null` for every job created through
   * the admin API. Returned on every job shape so a client can reconcile a
   * posting against its source system.
   */
  external_id: string | null;
  published_at: string | null;
  expires_at: string | null;
  created_at: string;
  updated_at: string;
}

/** Returned by single-resource endpoints. */
export interface Job extends JobSummary {
  /** Full text. Plain text with newlines, NOT HTML. Must be escaped on render. */
  description: string;
}

export interface Pagination {
  page: number;
  per_page: number;
  total: number;
  total_pages: number;
}

/**
 * NOTE: `Admin`, `AdminSession` and `LoginInput` were removed on the
 * cloudflare-only branch. Admin identity now comes from Cloudflare Access and is
 * `{email, sub}` only -- see `AdminIdentity` in `adminApi.ts`. There is no
 * admin_users row to carry a name, no session of ours, and no CSRF token, so
 * keeping those shapes would describe a login flow that no longer exists.
 */

// ------------------------------------------------------------------ envelopes

export interface ApiSuccess<T> {
  data: T;
}

export interface ApiCollection<T> {
  data: T[];
  meta: Pagination;
}

export const API_ERROR_CODES = [
  'BAD_REQUEST',
  'UNAUTHORIZED',
  'INVALID_CREDENTIALS',
  'FORBIDDEN',
  'CSRF_INVALID',
  'JOB_NOT_FOUND',
  'NOT_FOUND',
  'METHOD_NOT_ALLOWED',
  'SLUG_CONFLICT',
  'PAYLOAD_TOO_LARGE',
  'VALIDATION_FAILED',
  'RATE_LIMITED',
  'INTERNAL_ERROR',
  'SERVICE_UNAVAILABLE',
  /** Client-side only: the request never reached the API. */
  'NETWORK_ERROR',
] as const;
export type ApiErrorCode = LooseEnum<(typeof API_ERROR_CODES)[number]>;

export interface ApiErrorBody {
  error: {
    code: ApiErrorCode;
    message: string;
    details?: Record<string, string>;
    request_id?: string;
  };
}

// ------------------------------------------------------------- request bodies

/** Body for POST/PUT /api/admin/jobs. Mirrors contract section 9. */
export interface JobInput {
  title: string;
  description: string;
  salary: string;
  qualification: string;
  job_type: JobType;
  location: string;
  apply_url: string;

  company?: string | null;
  country?: string | null;
  remote_type?: RemoteType | null;
  experience_level?: string | null;
  category?: string | null;
  is_featured?: boolean;
  expires_at?: string | null;
  status?: JobStatus;
  /** Omit to keep the existing slug on update, or to derive it on create. */
  slug?: string;
  /**
   * Webhook ingestion only (`POST /api/webhook/jobs`). The caller's own id for
   * the posting, <= 200 chars. Supplying it makes ingestion idempotent: a repeat
   * delivery updates the matching row instead of creating a duplicate. Ignored
   * by the admin `POST`/`PUT` handlers.
   */
  external_id?: string | null;
}

// ------------------------------------------------------------- query builders

/** Query parameters accepted by GET /api/jobs (contract section 3). */
export interface JobListQuery {
  page?: number;
  per_page?: number;
  q?: string;
  location?: string;
  country?: string;
  job_type?: JobType;
  remote_type?: RemoteType;
  featured?: boolean;
}

/** Additional parameters accepted by GET /api/admin/jobs. */
export interface AdminJobListQuery extends JobListQuery {
  status?: JobStatus;
  include_deleted?: boolean;
}

// ----------------------------------------------------------------- type guards

export function isJobStatus(value: unknown): value is JobStatus {
  return typeof value === 'string' && (JOB_STATUSES as readonly string[]).includes(value);
}

export function isJobType(value: unknown): value is JobType {
  return typeof value === 'string' && (JOB_TYPES as readonly string[]).includes(value);
}

export function isRemoteType(value: unknown): value is RemoteType {
  return typeof value === 'string' && (REMOTE_TYPES as readonly string[]).includes(value);
}
