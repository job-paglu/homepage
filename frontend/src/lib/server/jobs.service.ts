/**
 * The business rules: validation orchestration, slug settling, the
 * status/published_at transitions, pagination clamping and DTO shaping.
 *
 * No SQL and no `Request` reach this file. It talks to `JobsRepository` and
 * returns plain objects, which is what makes every rule here testable without a
 * database and without an HTTP server.
 *
 * Ported from backend/internal/jobs/service.go.
 */

import type { Job, JobSummary, Pagination } from '../types';
import { applyUrlPolicy, type ApplyUrlPolicy, type RuntimeEnv } from './env';
import { ApiError, errors } from './respond';
import {
  JobsRepository,
  RepoError,
  type AdminListFilter,
  type JobRecord,
  type JobWrite,
  type ListFilter,
} from './jobs.repo';
import { deduplicateSlug, deriveSlugForTitle, isReservedSlug } from './slug';
import { type ListQuery, nowRfc3339, validateJobInput, validateStoredJob } from './validate';

/** Pagination bounds from contract sections 3 and 4. */
export const DEFAULT_PER_PAGE = 20;
export const MAX_PER_PAGE = 50;
export const ADMIN_MAX_PER_PAGE = 100;

/** The excerpt character budget from contract section 2.1. */
const EXCERPT_LENGTH = 200;

// ------------------------------------------------------------------- excerpt

/**
 * First `max` characters of a description, cut on a word boundary, with an
 * ellipsis when it was truncated.
 *
 * Internal whitespace is collapsed first: a description is plain text with
 * paragraph breaks, and an excerpt that begins with two blank lines looks like a
 * rendering bug in a card that has no room for them.
 */
export function excerpt(description: string, max = EXCERPT_LENGTH): string {
  const collapsed = description.split(/\s+/u).filter((word) => word !== '').join(' ');
  const chars = [...collapsed];
  if (chars.length <= max) return collapsed;

  let window = chars.slice(0, max);
  const lastSpace = window.lastIndexOf(' ');
  // Walk back to the last space so a word is not sliced in half. A window that
  // is one long token — a URL, or a CJK sentence — has no boundary to find, and
  // a hard cut is the only option.
  if (lastSpace > 0) window = window.slice(0, lastSpace);
  return `${window.join('').replace(/ +$/u, '')}…`;
}

// ----------------------------------------------------------------------- DTOs

/** Drops `deleted_at` and `description`, and computes `excerpt`. Dropping
 *  `deleted_at` is the reason this conversion exists at all. */
function toSummary(record: JobRecord): JobSummary {
  return {
    id: record.id,
    slug: record.slug,
    title: record.title,
    excerpt: excerpt(record.description),
    salary: record.salary,
    qualification: record.qualification,
    job_type: record.job_type,
    location: record.location,
    company: record.company,
    country: record.country,
    remote_type: record.remote_type,
    experience_level: record.experience_level,
    category: record.category,
    is_featured: record.is_featured,
    apply_url: record.apply_url,
    status: record.status,
    published_at: record.published_at,
    expires_at: record.expires_at,
    created_at: record.created_at,
    updated_at: record.updated_at,
  };
}

/** `excerpt` stays present on the detail shape so a client can reuse one type. */
function toJob(record: JobRecord): Job {
  return { ...toSummary(record), description: record.description };
}

/** `total_pages` is 0 — not 1 — for an empty result (contract section 2.3). */
function paginate(page: number, perPage: number, total: number): Pagination {
  return {
    page,
    per_page: perPage,
    total,
    total_pages: total > 0 && perPage > 0 ? Math.ceil(total / perPage) : 0,
  };
}

function clamp(query: ListQuery, maxPerPage: number): { page: number; perPage: number } {
  const page = Math.max(query.page, 1);
  // 0 or negative means "unspecified" and yields the default, not a useless
  // one-row page nobody asked for (contract section 3).
  const perPage = query.perPage <= 0 ? DEFAULT_PER_PAGE : Math.min(query.perPage, maxPerPage);
  return { page, perPage };
}

function toFilter(query: ListQuery, page: number, perPage: number): ListFilter {
  return {
    q: query.q,
    location: query.location,
    country: query.country,
    jobType: query.jobType,
    remoteType: query.remoteType,
    featured: query.featured,
    limit: perPage,
    offset: (page - 1) * perPage,
  };
}

/**
 * The only place the repository's vocabulary meets the client's, so a new
 * `RepoError` kind has exactly one place to be handled and an unrecognised
 * failure can only ever become a 500.
 *
 * It RETURNS the error rather than throwing it, so every call site reads
 * `throw toApiError(err)` — an explicit throw the compiler can see, instead of
 * relying on never-returning-function control-flow analysis to prove a method
 * cannot fall off its end.
 */
function toApiError(err: unknown): ApiError {
  if (err instanceof ApiError) return err;
  if (err instanceof RepoError) {
    return err.kind === 'slug_conflict' ? errors.slugConflict() : errors.serviceUnavailable(err);
  }
  return errors.internal(err);
}

export interface CollectionResult {
  readonly data: JobSummary[];
  readonly meta: Pagination;
}

/**
 * Wire a service for one request.
 *
 * Per-request rather than module-scoped, because the D1 binding arrives on the
 * request context — a module-level singleton would capture whichever request
 * happened to be first and keep using its binding.
 */
export function createJobService(env: RuntimeEnv): JobService {
  return new JobService(new JobsRepository(env.DB), applyUrlPolicy(env));
}

// -------------------------------------------------------------------- service

export class JobService {
  constructor(
    private readonly repo: JobsRepository,
    private readonly policy: ApplyUrlPolicy,
    /** Injected so the expiry and published_at rules are testable without
     *  sleeping, and so one request sees a single consistent "now". */
    private readonly now: () => string = nowRfc3339,
  ) {}

  async listPublic(query: ListQuery): Promise<CollectionResult> {
    const { page, perPage } = clamp(query, MAX_PER_PAGE);
    try {
      const result = await this.repo.listPublic(toFilter(query, page, perPage), this.now());
      return { data: result.records.map(toSummary), meta: paginate(page, perPage, result.total) };
    } catch (err) {
      throw toApiError(err);
    }
  }

  async getPublic(slug: string): Promise<Job> {
    try {
      const record = await this.repo.getPublicBySlug(slug, this.now());
      if (record === null) throw errors.jobNotFound();
      return toJob(record);
    } catch (err) {
      throw toApiError(err);
    }
  }

  async listAdmin(query: ListQuery): Promise<CollectionResult> {
    const { page, perPage } = clamp(query, ADMIN_MAX_PER_PAGE);
    const filter: AdminListFilter = {
      ...toFilter(query, page, perPage),
      status: query.status,
      includeDeleted: query.includeDeleted,
    };
    try {
      const result = await this.repo.listAdmin(filter);
      return { data: result.records.map(toSummary), meta: paginate(page, perPage, result.total) };
    } catch (err) {
      throw toApiError(err);
    }
  }

  async getAdmin(id: string): Promise<Job> {
    const record = await this.load(id);
    return toJob(record);
  }

  async create(body: Record<string, unknown>): Promise<Job> {
    const now = this.now();
    const { fields, errors: fieldErrors, suppliedSlug } = validateJobInput(
      body,
      null,
      new Date(now),
      this.policy,
    );
    if (fieldErrors.any) throw errors.validationFailed(fieldErrors.toDetails());

    // The id is generated before the slug so the section 8 step-6 fallback
    // (`job-<first 8 of uuid>`) can use it, and so the INSERT is a single
    // statement with no server-side default to read back.
    const id = crypto.randomUUID();
    const slug = await this.settleSlug(suppliedSlug, fields.title, id, null);

    const write: JobWrite = {
      ...fields,
      slug,
      // A create that asks for "published" IS publishing, so it gets a
      // publication date. Every other status leaves it null.
      published_at: fields.status === 'published' ? now : null,
    };
    try {
      return toJob(await this.repo.create(id, write, now));
    } catch (err) {
      throw toApiError(err);
    }
  }

  /**
   * Full replacement of the mutable fields, with two deliberate departures from
   * a literal "replacement", both required by contract section 4:
   *
   *   * an absent `slug` keeps the existing one even when the title changed —
   *     that is what protects a live URL and its search ranking;
   *   * an absent `status` keeps the existing one rather than resetting to
   *     `draft`, so a dashboard "Save" on a live job cannot silently unpublish it.
   */
  async update(id: string, body: Record<string, unknown>): Promise<Job> {
    const existing = await this.load(id);
    const now = this.now();

    const { fields, errors: fieldErrors, suppliedSlug } = validateJobInput(
      body,
      existing,
      new Date(now),
      this.policy,
    );
    if (fieldErrors.any) throw errors.validationFailed(fieldErrors.toDetails());

    const slug = await this.settleSlug(suppliedSlug, fields.title, id, existing.slug);

    const write: JobWrite = {
      ...fields,
      slug,
      // published_at is set the first time a job becomes published and is never
      // rewritten afterwards, whichever route the transition took.
      published_at:
        existing.published_at ?? (fields.status === 'published' ? now : null),
    };
    try {
      const updated = await this.repo.update(id, write, now);
      if (updated === null) throw errors.jobNotFound();
      return toJob(updated);
    } catch (err) {
      throw toApiError(err);
    }
  }

  /** Soft delete: 204, and the row survives so it can be recovered. */
  async remove(id: string): Promise<void> {
    try {
      if (!(await this.repo.softDelete(id, this.now()))) throw errors.jobNotFound();
    } catch (err) {
      throw toApiError(err);
    }
  }

  /**
   * Make a job publicly visible.
   *
   * The STORED record is re-validated first, every field of it. The contract
   * requires this for `apply_url`, and extending it to the rest costs nothing
   * and covers the case that motivated the rule: a row edited directly in the
   * database, bypassing every check, must not become a live page with a
   * `javascript:` apply link.
   */
  async publish(id: string): Promise<Job> {
    const existing = await this.load(id);
    const fieldErrors = validateStoredJob(existing, this.policy);
    if (fieldErrors.any) throw errors.validationFailed(fieldErrors.toDetails());

    try {
      const published = await this.repo.publish(id, this.now());
      if (published === null) throw errors.jobNotFound();
      return toJob(published);
    } catch (err) {
      throw toApiError(err);
    }
  }

  /** Hides the job. `published_at` is left intact, so re-publishing keeps the
   *  original date, and the public detail route 404s immediately. */
  async unpublish(id: string): Promise<Job> {
    try {
      const record = await this.repo.unpublish(id, this.now());
      if (record === null) throw errors.jobNotFound();
      return toJob(record);
    } catch (err) {
      throw toApiError(err);
    }
  }

  // ------------------------------------------------------------------ helpers

  private async load(id: string): Promise<JobRecord> {
    try {
      const record = await this.repo.getById(id);
      if (record === null) throw errors.jobNotFound();
      return record;
    } catch (err) {
      throw toApiError(err);
    }
  }

  /**
   * Resolve the slug for a create or an update.
   *
   * An EXPLICIT slug is never silently altered: the admin asked for a specific
   * URL and deserves to be told it is unavailable (409) rather than to discover
   * later that the site published a different one. A DERIVED slug is
   * de-duplicated instead and can never conflict.
   */
  private async settleSlug(
    supplied: string | undefined,
    title: string,
    id: string,
    existingSlug: string | null,
  ): Promise<string> {
    const excludeId = existingSlug === null ? null : id;
    const taken = (candidate: string): Promise<boolean> => this.repo.slugTaken(candidate, excludeId);

    try {
      if (supplied !== undefined) {
        if (isReservedSlug(supplied)) {
          throw errors.validationFailed({
            slug: 'That slug is reserved by the site. Please choose another.',
          });
        }
        if (await taken(supplied)) throw errors.slugConflict();
        return supplied;
      }

      // Update with no slug: keep the existing one, even though the title may
      // have changed.
      if (existingSlug !== null) return existingSlug;

      return await deduplicateSlug(deriveSlugForTitle(title, id), taken);
    } catch (err) {
      throw toApiError(err);
    }
  }
}
