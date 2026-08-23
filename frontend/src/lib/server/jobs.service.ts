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

import type { Job, JobStatus, JobSummary, Pagination } from '../types';
import { applyUrlPolicy, type ApplyUrlPolicy, type RuntimeEnv } from './env';
import { ApiError, type ErrorCode, errors } from './respond';
import {
  JobsRepository,
  RepoError,
  type AdminListFilter,
  type JobRecord,
  type JobWrite,
  type ListFilter,
} from './jobs.repo';
import { deduplicateSlug, deriveSlugForTitle, isReservedSlug } from './slug';
import {
  charLength,
  cleanSingleLine,
  type FieldErrors,
  type ListQuery,
  nowRfc3339,
  validateJobInput,
  validateStoredJob,
} from './validate';

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
    external_id: record.external_id,
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

// ----------------------------------------------------------------- ingestion

/** Contract "Webhook ingestion": the caller-supplied id is capped at 200 chars. */
const EXTERNAL_ID_MAX_LENGTH = 200;

/** The per-item error, shaped exactly like the wire's error object. */
export interface IngestItemError {
  readonly code: ErrorCode;
  readonly message: string;
  readonly details?: Record<string, string>;
}

/** One row of the batch result. `id`/`slug`/`status` are present on success;
 *  `error` is present on failure. `external_id` is always echoed (or null) so a
 *  sender can reconcile each result against what it sent. */
export interface IngestItemResult {
  readonly index: number;
  readonly action: 'created' | 'updated' | 'failed';
  readonly id?: string;
  readonly slug?: string;
  readonly external_id: string | null;
  readonly status?: JobStatus;
  readonly error?: IngestItemError;
}

export interface IngestSummary {
  readonly created: number;
  readonly updated: number;
  readonly failed: number;
  readonly results: IngestItemResult[];
}

/**
 * Read and validate `external_id`, recording any problem on the SHARED
 * FieldErrors so it is reported together with the field errors, not in a second
 * round trip. Returns the cleaned id, or null when absent or blank.
 */
function readExternalId(body: Record<string, unknown>, e: FieldErrors): string | null {
  const raw = body['external_id'];
  if (raw === undefined || raw === null) return null;
  if (typeof raw !== 'string') {
    e.add('external_id', 'Must be text.');
    return null;
  }
  const cleaned = cleanSingleLine(raw);
  if (cleaned === '') return null;
  if (charLength(cleaned) > EXTERNAL_ID_MAX_LENGTH) {
    e.add('external_id', `Must be at most ${EXTERNAL_ID_MAX_LENGTH} characters.`);
    return null;
  }
  return cleaned;
}

/** Best-effort external_id for a FAILED item's report. Never throws — it runs in
 *  the catch path, where the item may be malformed or not even an object. */
function externalIdForReport(item: unknown): string | null {
  if (typeof item !== 'object' || item === null || Array.isArray(item)) return null;
  const raw = (item as Record<string, unknown>)['external_id'];
  if (typeof raw !== 'string') return null;
  const cleaned = cleanSingleLine(raw);
  return cleaned === '' ? null : cleaned;
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

  // ---------------------------------------------------------------- ingestion

  /**
   * Ingest a batch of webhook items.
   *
   * This NEVER throws for a per-item problem: a bad row becomes a `failed`
   * result so the rest of the batch still applies, and the sender is not driven
   * to retry the whole delivery over one bad row (the webhook convention — see
   * the route, which answers 200 for any processed batch). Only a SYSTEMIC
   * failure — a 5xx such as the database being unreachable — aborts the batch,
   * because retrying the same item cannot help and the sender should instead
   * retry the whole request later.
   */
  async ingest(items: readonly unknown[]): Promise<IngestSummary> {
    const results: IngestItemResult[] = [];
    let created = 0;
    let updated = 0;
    let failed = 0;

    for (let index = 0; index < items.length; index += 1) {
      const item = items[index];
      try {
        const result = await this.ingestOne(index, item);
        if (result.action === 'created') created += 1;
        else updated += 1;
        results.push(result);
      } catch (err) {
        const apiError = err instanceof ApiError ? err : toApiError(err);
        // A 5xx cannot be a per-item outcome: it means the database or the
        // process is unwell, so it aborts the whole batch and the route turns it
        // into a 503/500. Every 4xx is a fact about THIS item and is recorded.
        if (apiError.status >= 500) throw apiError;
        results.push({
          index,
          action: 'failed',
          external_id: externalIdForReport(item),
          error: {
            code: apiError.code,
            message: apiError.message,
            ...(apiError.details !== undefined ? { details: apiError.details } : {}),
          },
        });
        failed += 1;
      }
    }

    return { created, updated, failed, results };
  }

  /**
   * Ingest one item. Idempotency lives here:
   *
   *   * `external_id` present AND an active row already carries it -> UPDATE that
   *     row (the upsert). Its slug is kept STABLE — a changed title does not move
   *     a live URL — exactly as `update` (PUT) behaves. `action: 'updated'`.
   *   * `external_id` present, no active row carries it -> INSERT, storing the
   *     id. `action: 'created'`.
   *   * `external_id` absent -> INSERT with the ordinary auto-slug de-duplication
   *     (the plain create path). `action: 'created'`.
   *
   * Every item is validated as an admin CREATE (`existing = null`), which fixes
   * two rules deliberately: `status` defaults to `draft` — publishing is opt-in
   * per item, because a leaked key that can only draft is far less dangerous than
   * one that can publish — and a supplied `expires_at` must be in the future.
   */
  private async ingestOne(index: number, item: unknown): Promise<IngestItemResult> {
    if (typeof item !== 'object' || item === null || Array.isArray(item)) {
      throw errors.validationFailed({ item: 'Each job must be a JSON object.' });
    }
    const body = item as Record<string, unknown>;
    const now = this.now();

    const { fields, errors: fieldErrors, suppliedSlug } = validateJobInput(
      body,
      null,
      new Date(now),
      this.policy,
    );
    const externalId = readExternalId(body, fieldErrors);
    if (fieldErrors.any) throw errors.validationFailed(fieldErrors.toDetails());

    if (externalId !== null) {
      const existing = await this.repo.getActiveByExternalId(externalId);
      if (existing !== null) {
        // UPSERT: replace the mutable fields, keep the slug stable, and apply the
        // same published_at rule as every other write — set once, never rewritten.
        //
        // STATUS ON UPSERT: if the caller did not supply `status`, keep the
        // existing row's status rather than resetting to the create-default
        // `draft`. Webhooks re-fire the same event, often with the record they
        // originally sent; an omitted status must not silently unpublish a live
        // job. This mirrors the PUT (update) rule. A supplied status still wins.
        const statusSupplied = body['status'] !== undefined && body['status'] !== null;
        const effectiveStatus = statusSupplied ? fields.status : existing.status;
        const slug = await this.settleSlug(suppliedSlug, fields.title, existing.id, existing.slug);
        const write: JobWrite = {
          ...fields,
          status: effectiveStatus,
          slug,
          published_at: existing.published_at ?? (effectiveStatus === 'published' ? now : null),
        };
        try {
          const record = await this.repo.update(existing.id, write, now);
          // Null means the row was soft-deleted between the lookup and the write
          // (a race). Report a not-found failure rather than silently dropping it.
          if (record === null) throw errors.jobNotFound();
          return {
            index,
            action: 'updated',
            id: record.id,
            slug: record.slug,
            external_id: externalId,
            status: record.status,
          };
        } catch (err) {
          throw toApiError(err);
        }
      }
    }

    // INSERT. The id is generated before the slug so the section-8 step-6
    // fallback (`job-<first 8 of uuid>`) can use it, exactly as `create` does.
    const id = crypto.randomUUID();
    const slug = await this.settleSlug(suppliedSlug, fields.title, id, null);
    const write: JobWrite = {
      ...fields,
      slug,
      published_at: fields.status === 'published' ? now : null,
    };
    try {
      const record = await this.repo.createWithExternalId(id, write, externalId, now);
      return {
        index,
        action: 'created',
        id: record.id,
        slug: record.slug,
        external_id: externalId,
        status: record.status,
      };
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
