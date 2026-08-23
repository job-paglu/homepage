/**
 * The ONLY place SQL lives. Nothing else in the codebase may contain a query.
 *
 * Two invariants this file exists to enforce:
 *
 *  1. Every value reaches SQLite through a `?` bind parameter. There is no
 *     string interpolation of a value anywhere, including the dynamic list
 *     filters — the `Conditions` builder below can only hand out a `?` in
 *     exchange for the value that fills it, so a fragment and its binds are
 *     built together and cannot drift out of order.
 *
 *  2. The public visibility predicate is written once, as a constant, and baked
 *     into the public query methods. It is a constant rather than a helper that
 *     returns a WHERE clause because a helper can be left uncalled — and a
 *     handler that forgot it would leak drafts.
 *
 * Ported from backend/internal/jobs/repository.go.
 */

import type { D1Database } from './env';
import type { JobFields } from './validate';
import type { JobStatus } from '../types';

/** A row of `jobs` as this layer reads and writes it. Carries `deleted_at`,
 *  which no response may ever include, and no `excerpt`, which is computed. */
export interface JobRecord extends JobFields {
  id: string;
  slug: string;
  status: JobStatus;
  /** The webhook caller's own id, or null for an admin-created job. It is NOT a
   *  member of JobFields because no admin write ever sets it — it is settled by
   *  the ingestion path alone. */
  external_id: string | null;
  published_at: string | null;
  created_at: string;
  updated_at: string;
  deleted_at: string | null;
}

/** The mutable fields plus the two the service settles: slug and published_at. */
export interface JobWrite extends JobFields {
  slug: string;
  published_at: string | null;
}

export interface ListFilter {
  q: string;
  location: string;
  country: string;
  jobType: string;
  remoteType: string;
  featured: boolean | null;
  limit: number;
  offset: number;
}

export interface AdminListFilter extends ListFilter {
  status: string;
  includeDeleted: boolean;
}

export interface Page {
  readonly records: JobRecord[];
  readonly total: number;
}

/**
 * Raised for a database condition the service must branch on. A missing row is
 * NOT one of these — lookups return `null`, which is harder to forget to handle
 * than a thrown sentinel.
 */
export class RepoError extends Error {
  constructor(
    readonly kind: 'slug_conflict' | 'unavailable',
    message: string,
    /** The underlying D1 error. Logged, never serialised — it can quote SQL. */
    readonly reason?: unknown,
  ) {
    super(message);
    this.name = 'RepoError';
  }
}

// ------------------------------------------------------------------- SQL text

const COLUMNS = `id, slug, title, description, salary, qualification, job_type, location,
  apply_url, company, country, remote_type, experience_level, category,
  is_featured, status, external_id, published_at, expires_at, created_at, updated_at, deleted_at`;

/**
 * The public visibility predicate. `?` is bound with the current timestamp.
 *
 * Timestamps are fixed-width RFC 3339 UTC strings (see validate.ts
 * `toRfc3339Utc`), so this string comparison is a chronological comparison.
 */
const PUBLIC_VISIBLE = `status = 'published' AND deleted_at IS NULL
  AND (expires_at IS NULL OR expires_at > ?)`;

/**
 * The contract's ordering is `is_featured DESC, published_at DESC NULLS LAST,
 * created_at DESC`. `NULLS LAST` is not spelled out because SQLite already puts
 * NULLs last under DESC — writing it would be a no-op that also required a newer
 * SQLite than the syntax-free version needs. It is called out here so nobody adds
 * it thinking it was forgotten.
 */
const PUBLIC_ORDER = 'ORDER BY is_featured DESC, published_at DESC, created_at DESC';

interface JobRow {
  id: string;
  slug: string;
  title: string;
  description: string;
  salary: string;
  qualification: string;
  job_type: string;
  location: string;
  apply_url: string;
  company: string | null;
  country: string | null;
  remote_type: string | null;
  experience_level: string | null;
  category: string | null;
  is_featured: number;
  status: string;
  external_id: string | null;
  published_at: string | null;
  expires_at: string | null;
  created_at: string;
  updated_at: string;
  deleted_at: string | null;
}

/** `count(*) OVER ()` rides along on every list row, so a page and its total are
 *  one round trip. Window functions are evaluated before LIMIT, so the value is
 *  the full match count. */
interface JobRowWithTotal extends JobRow {
  total: number;
}

function toRecord(row: JobRow): JobRecord {
  return {
    ...row,
    // SQLite has no boolean. The 0/1 is converted here and only here, so it can
    // never reach JSON — the contract says `is_featured` is a `boolean`.
    is_featured: row.is_featured === 1,
    // The `jobs_status_check` CHECK constraint guarantees membership, so this is
    // a narrowing of a value the database already validated, not a hope.
    status: row.status as JobStatus,
  };
}

// -------------------------------------------------------------- filter builder

class Conditions {
  readonly parts: string[] = [];
  readonly binds: unknown[] = [];

  /** The fragment may contain SQL and `?` placeholders only. Its values are
   *  handed over in the same call, which is what keeps the two in step. */
  add(fragment: string, ...values: readonly unknown[]): void {
    this.parts.push(fragment);
    this.binds.push(...values);
  }

  where(): string {
    return this.parts.length === 0 ? '' : ` WHERE ${this.parts.join(' AND ')}`;
  }
}

/** Neutralise LIKE metacharacters so a search for "50%" is a search for "50%"
 *  and not for "50<anything>". */
function likePattern(value: string): string {
  return `%${value.replace(/[\\%_]/g, (match) => `\\${match}`)}%`;
}

function applyCommonFilters(c: Conditions, f: ListFilter): void {
  if (f.q !== '') {
    const pattern = likePattern(f.q);
    // SQLite's LIKE is already case-insensitive for ASCII, which is what the
    // contract's "case-insensitive match" asks for. COLLATE would be a no-op
    // here: LIKE uses its own folding and ignores the column collation.
    c.add(
      `(title LIKE ? ESCAPE '\\' OR coalesce(company, '') LIKE ? ESCAPE '\\'
        OR location LIKE ? ESCAPE '\\' OR description LIKE ? ESCAPE '\\')`,
      pattern,
      pattern,
      pattern,
      pattern,
    );
  }
  if (f.location !== '') {
    c.add(`location LIKE ? ESCAPE '\\'`, likePattern(f.location));
  }
  if (f.country !== '') {
    // Exact but case-insensitive (contract section 3). Here COLLATE NOCASE does
    // the work, and jobs_country_nocase_idx is declared to match.
    c.add(`coalesce(country, '') COLLATE NOCASE = ?`, f.country);
  }
  if (f.jobType !== '') c.add('job_type = ?', f.jobType);
  if (f.remoteType !== '') c.add('remote_type = ?', f.remoteType);
  if (f.featured !== null) c.add('is_featured = ?', f.featured ? 1 : 0);
}

const WRITE_COLUMNS = `slug, title, description, salary, qualification, job_type, location,
  apply_url, company, country, remote_type, experience_level, category,
  is_featured, status, published_at, expires_at`;

/** Bind values for WRITE_COLUMNS, in exactly that order. */
function writeBinds(job: JobWrite): unknown[] {
  return [
    job.slug,
    job.title,
    job.description,
    job.salary,
    job.qualification,
    job.job_type,
    job.location,
    job.apply_url,
    job.company,
    job.country,
    job.remote_type,
    job.experience_level,
    job.category,
    job.is_featured ? 1 : 0,
    job.status,
    job.published_at,
    job.expires_at,
  ];
}

// ------------------------------------------------------------------ repository

export class JobsRepository {
  constructor(private readonly db: D1Database) {}

  // ----------------------------------------------------------------- public

  async listPublic(f: ListFilter, now: string): Promise<Page> {
    const c = new Conditions();
    c.add(PUBLIC_VISIBLE, now);
    applyCommonFilters(c, f);

    return this.page(
      `SELECT ${COLUMNS}, count(*) OVER () AS total FROM jobs${c.where()}
       ${PUBLIC_ORDER} LIMIT ? OFFSET ?`,
      [...c.binds, f.limit, f.offset],
    );
  }

  /** Cannot return a draft even if a caller forgets to check: the predicate is
   *  in the query, not in the handler. */
  async getPublicBySlug(slug: string, now: string): Promise<JobRecord | null> {
    return this.one(`SELECT ${COLUMNS} FROM jobs WHERE slug = ? AND ${PUBLIC_VISIBLE}`, [slug, now]);
  }

  // ------------------------------------------------------------------ admin

  /** All statuses, expiry ignored, soft-deleted rows excluded unless asked for. */
  async listAdmin(f: AdminListFilter): Promise<Page> {
    const c = new Conditions();
    if (!f.includeDeleted) c.add('deleted_at IS NULL');
    if (f.status !== '') c.add('status = ?', f.status);
    applyCommonFilters(c, f);

    return this.page(
      `SELECT ${COLUMNS}, count(*) OVER () AS total FROM jobs${c.where()}
       ORDER BY updated_at DESC LIMIT ? OFFSET ?`,
      [...c.binds, f.limit, f.offset],
    );
  }

  async getById(id: string): Promise<JobRecord | null> {
    return this.one(`SELECT ${COLUMNS} FROM jobs WHERE id = ? AND deleted_at IS NULL`, [id]);
  }

  /**
   * The active job carrying `externalId`, or null — the lookup half of the
   * webhook upsert. `deleted_at IS NULL` matches the `jobs_external_id_active`
   * partial index exactly, so a soft-deleted job's id is treated as free and a
   * re-post creates a fresh row rather than resurrecting the old one.
   */
  async getActiveByExternalId(externalId: string): Promise<JobRecord | null> {
    return this.one(
      `SELECT ${COLUMNS} FROM jobs WHERE external_id = ? AND deleted_at IS NULL`,
      [externalId],
    );
  }

  /**
   * Is `slug` used by a live job other than `excludeId`? Pass null on create.
   *
   * The `(? IS NULL OR id <> ?)` form keeps this one statement rather than two
   * nearly identical ones — a NULL exclusion disables itself.
   */
  async slugTaken(slug: string, excludeId: string | null): Promise<boolean> {
    const row = await this.scalar<{ found: number }>(
      `SELECT 1 AS found FROM jobs
       WHERE slug = ? AND deleted_at IS NULL AND (? IS NULL OR id <> ?) LIMIT 1`,
      [slug, excludeId, excludeId],
    );
    return row !== null;
  }

  async create(id: string, job: JobWrite, now: string): Promise<JobRecord> {
    const created = await this.one(
      // 20 placeholders: id, then the 17 of WRITE_COLUMNS in order, then
      // created_at and updated_at. Written out rather than generated so the
      // count is checkable by eye against writeBinds().
      `INSERT INTO jobs (id, ${WRITE_COLUMNS}, created_at, updated_at)
       VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
       RETURNING ${COLUMNS}`,
      [id, ...writeBinds(job), now, now],
    );
    // RETURNING on a successful INSERT always yields the row. A null here means
    // the statement did not insert, which is a bug rather than a 404.
    if (created === null) throw new RepoError('unavailable', 'INSERT returned no row');
    return created;
  }

  /**
   * INSERT for the webhook, identical to `create` but also storing `external_id`.
   *
   * A separate method rather than a parameter on `create` (or a new WRITE_COLUMNS
   * entry) on purpose: the admin `create`/`update` statements deliberately never
   * mention `external_id`, so an admin edit can never wipe the id a webhook set,
   * and an admin create can never accidentally claim one. Only this path writes
   * the column.
   *
   * 21 placeholders: id, then the 17 of WRITE_COLUMNS in order, then external_id,
   * then created_at and updated_at. A UNIQUE-index violation on external_id is
   * classified as `slug_conflict` by `classify` (it matches any "UNIQUE
   * constraint failed"); the service maps that to a per-item failure, which is
   * the correct outcome for a genuine concurrent double-insert of the same id.
   */
  async createWithExternalId(
    id: string,
    job: JobWrite,
    externalId: string | null,
    now: string,
  ): Promise<JobRecord> {
    const created = await this.one(
      `INSERT INTO jobs (id, ${WRITE_COLUMNS}, external_id, created_at, updated_at)
       VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
       RETURNING ${COLUMNS}`,
      [id, ...writeBinds(job), externalId, now, now],
    );
    if (created === null) throw new RepoError('unavailable', 'INSERT returned no row');
    return created;
  }

  /** `updated_at` is written explicitly by every write. There is deliberately no
   *  trigger: a trigger is invisible at the call site, and one mechanism is
   *  easier to reason about than two. */
  async update(id: string, job: JobWrite, now: string): Promise<JobRecord | null> {
    return this.one(
      `UPDATE jobs SET
         slug = ?, title = ?, description = ?, salary = ?, qualification = ?,
         job_type = ?, location = ?, apply_url = ?, company = ?, country = ?,
         remote_type = ?, experience_level = ?, category = ?, is_featured = ?,
         status = ?, published_at = ?, expires_at = ?, updated_at = ?
       WHERE id = ? AND deleted_at IS NULL
       RETURNING ${COLUMNS}`,
      [...writeBinds(job), now, id],
    );
  }

  /** Soft delete. The row survives so it is recoverable, and jobs_slug_active is
   *  partial on `deleted_at IS NULL` so the slug is reusable immediately.
   *  Returns false when the job is unknown OR already deleted — indistinguishable,
   *  and the contract says both are 404. */
  async softDelete(id: string, now: string): Promise<boolean> {
    const row = await this.scalar<{ id: string }>(
      `UPDATE jobs SET deleted_at = ?, updated_at = ?
       WHERE id = ? AND deleted_at IS NULL RETURNING id`,
      [now, now, id],
    );
    return row !== null;
  }

  /** `coalesce(published_at, ?)` is the whole rule: re-publishing does not
   *  rewrite the original publication date (contract section 4). */
  async publish(id: string, now: string): Promise<JobRecord | null> {
    return this.one(
      `UPDATE jobs SET status = 'published', published_at = coalesce(published_at, ?),
         updated_at = ?
       WHERE id = ? AND deleted_at IS NULL RETURNING ${COLUMNS}`,
      [now, now, id],
    );
  }

  /** Leaves `published_at` intact, so a later re-publish keeps the original date. */
  async unpublish(id: string, now: string): Promise<JobRecord | null> {
    return this.one(
      `UPDATE jobs SET status = 'unpublished', updated_at = ?
       WHERE id = ? AND deleted_at IS NULL RETURNING ${COLUMNS}`,
      [now, id],
    );
  }

  /** Readiness probe. Cheapest statement that proves the binding round-trips. */
  async ping(): Promise<void> {
    await this.scalar<{ ok: number }>('SELECT 1 AS ok', []);
  }

  // -------------------------------------------------------------- execution

  /** One row of an arbitrary shape, or null. Every D1 failure is classified. */
  private async scalar<T>(sql: string, binds: readonly unknown[]): Promise<T | null> {
    try {
      return await this.db.prepare(sql).bind(...binds).first<T>();
    } catch (cause) {
      throw classify(cause);
    }
  }

  private async one(sql: string, binds: readonly unknown[]): Promise<JobRecord | null> {
    try {
      const row = await this.db.prepare(sql).bind(...binds).first<JobRow>();
      return row === null ? null : toRecord(row);
    } catch (cause) {
      throw classify(cause);
    }
  }

  private async page(sql: string, binds: readonly unknown[]): Promise<Page> {
    try {
      const { results } = await this.db.prepare(sql).bind(...binds).all<JobRowWithTotal>();
      return {
        records: results.map(toRecord),
        // No rows means no window-function value to read, and zero is correct.
        total: results[0]?.total ?? 0,
      };
    } catch (cause) {
      throw classify(cause);
    }
  }
}

/**
 * Turn a D1 failure into something the service can branch on.
 *
 * The unique-index violation is matched on message text because D1 surfaces
 * SQLite's error string and nothing more structured. It is checked before the
 * generic branch so a slug collision is a 409 rather than a 500.
 */
function classify(cause: unknown): RepoError {
  const message = cause instanceof Error ? cause.message : String(cause);
  if (/UNIQUE constraint failed/i.test(message)) {
    return new RepoError('slug_conflict', 'slug already in use', cause);
  }
  return new RepoError('unavailable', 'the database could not be reached', cause);
}
