/**
 * Field validation — contract section 9 — plus query-string parsing and the one
 * timestamp formatter the whole API shares.
 *
 * Two properties every caller depends on, both ported from
 * backend/internal/validation:
 *
 *   * EVERY invalid field is reported, not just the first. A form that fixes one
 *     error per round trip is a bad form.
 *   * Cleaning happens BEFORE checking, so a title of `"   "` fails the required
 *     rule instead of passing a length check on whitespace.
 */

import { JOB_STATUSES, JOB_TYPES, REMOTE_TYPES, type JobStatus } from '../types';
import type { ApplyUrlPolicy } from './env';
import { validateApplyUrl } from './applyUrl';
import { errors } from './respond';
import { SUPPLIED_SLUG_MAX_LENGTH, normaliseSuppliedSlug } from './slug';

/** Contract section 9, character counts after trimming. */
const LIMITS = {
  title: { min: 3, max: 160 },
  description: { min: 20, max: 20_000 },
  salary: { min: 1, max: 120 },
  qualification: { min: 1, max: 240 },
  location: { min: 1, max: 160 },
  company: { max: 160 },
  country: { max: 80 },
  experienceLevel: { max: 80 },
  category: { max: 80 },
} as const;

/** Contract section 3: `q` over this length is a 400, never a silent truncation. */
const MAX_SEARCH_LENGTH = 100;

// -------------------------------------------------------------------- errors

/** Field name to one human sentence — exactly the `details` shape on the wire. */
export class FieldErrors {
  private readonly entries = new Map<string, string>();

  /** First message per field wins, so a specific early rule is not overwritten. */
  add(field: string, message: string): void {
    if (!this.entries.has(field)) this.entries.set(field, message);
  }

  get any(): boolean {
    return this.entries.size > 0;
  }

  toDetails(): Record<string, string> {
    return Object.fromEntries(this.entries);
  }
}

// ----------------------------------------------------------------- timestamps

/**
 * RFC 3339 UTC with a `Z` and NO fractional seconds — `2026-08-21T09:41:07Z`.
 *
 * This is the only place a timestamp string is produced. It matters twice over:
 * the contract mandates this exact shape on the wire (section 1), and D1 stores
 * timestamps as TEXT, so every stored value must be the same fixed width for
 * `expires_at > ?` to compare chronologically. `toISOString()` alone would emit
 * `.123Z` and break ordering inside a single second.
 */
export function toRfc3339Utc(date: Date): string {
  return date.toISOString().replace(/\.\d{3}Z$/, 'Z');
}

export function nowRfc3339(): string {
  return toRfc3339Utc(new Date());
}

/** Deliberately strict: `new Date()` alone accepts "March 2026" and worse. */
const RFC3339 = /^\d{4}-\d{2}-\d{2}[Tt]\d{2}:\d{2}:\d{2}(\.\d+)?([Zz]|[+-]\d{2}:\d{2})$/;
const TIMESTAMP_MESSAGE = 'Must be an RFC 3339 timestamp, for example 2026-12-31T23:59:59Z.';

function parseTimestamp(raw: string): Date | null {
  if (!RFC3339.test(raw)) return null;
  const parsed = new Date(raw);
  return Number.isNaN(parsed.getTime()) ? null : parsed;
}

// ------------------------------------------------------------------- cleaning

function isStrippable(codePoint: number, keepNewlineAndTab: boolean): boolean {
  if (keepNewlineAndTab && (codePoint === 0x0a || codePoint === 0x09)) return false;
  // C0 controls, DEL, and the C1 range, which includes the bidi and
  // line-separator characters that render unpredictably.
  return codePoint < 0x20 || codePoint === 0x7f || (codePoint >= 0x80 && codePoint <= 0x9f);
}

function stripControl(value: string, keepNewlineAndTab: boolean): string {
  let out = '';
  for (const char of value) {
    if (isStrippable(char.codePointAt(0) ?? 0, keepNewlineAndTab)) continue;
    out += char;
  }
  return out;
}

/** Trim, and remove every control character. */
export function cleanSingleLine(value: string): string {
  return stripControl(value, false).trim();
}

/** `cleanSingleLine` but keeps `\n` and `\t`, which a description legitimately
 *  contains. CRLF is normalised first so a Windows paste does not double the
 *  apparent length. */
export function cleanMultiline(value: string): string {
  return stripControl(value.replace(/\r\n?/g, '\n'), true).trim();
}

/** Code points, not UTF-16 units: the contract says "chars", and a 160-unit cap
 *  would silently cut a Japanese title short. */
export function charLength(value: string): number {
  return [...value].length;
}

// ------------------------------------------------------------- raw body reads

type RawBody = Record<string, unknown>;
const NOT_TEXT = 'Must be text.';

function requiredRaw(body: RawBody, key: string, e: FieldErrors): string {
  const value = body[key];
  if (value === undefined || value === null) return '';
  if (typeof value !== 'string') {
    e.add(key, NOT_TEXT);
    return '';
  }
  return value;
}

/** JSON `null` and an absent key are deliberately the same thing: a form that
 *  posts nothing for an untouched optional input must not store an empty value. */
function optionalRaw(body: RawBody, key: string, e: FieldErrors): string | undefined {
  const value = body[key];
  if (value === undefined || value === null) return undefined;
  if (typeof value !== 'string') {
    e.add(key, NOT_TEXT);
    return undefined;
  }
  return value;
}

// ----------------------------------------------------------------- validators

function checkRequiredLength(
  e: FieldErrors,
  field: string,
  value: string,
  min: number,
  max: number,
): void {
  const n = charLength(value);
  if (n === 0) e.add(field, 'This field is required.');
  else if (n < min || n > max) {
    e.add(
      field,
      min === 1 ? `Must be at most ${max} characters.` : `Must be between ${min} and ${max} characters.`,
    );
  }
}

function requiredString(e: FieldErrors, field: string, raw: string, min: number, max: number): string {
  const value = cleanSingleLine(raw);
  checkRequiredLength(e, field, value, min, max);
  return value;
}

function requiredText(e: FieldErrors, field: string, raw: string, min: number, max: number): string {
  const value = cleanMultiline(raw);
  checkRequiredLength(e, field, value, min, max);
  return value;
}

function optionalString(
  e: FieldErrors,
  field: string,
  raw: string | undefined,
  max: number,
): string | null {
  if (raw === undefined) return null;
  const value = cleanSingleLine(raw);
  if (value === '') return null;
  if (charLength(value) > max) e.add(field, `Must be at most ${max} characters.`);
  return value;
}

function enumMessage(allowed: readonly string[]): string {
  return `Must be one of: ${allowed.join(', ')}.`;
}

function requiredEnum(e: FieldErrors, field: string, raw: string, allowed: readonly string[]): string {
  const value = cleanSingleLine(raw);
  if (value === '') {
    e.add(field, 'This field is required.');
    return value;
  }
  if (!allowed.includes(value)) e.add(field, enumMessage(allowed));
  return value;
}

function optionalEnum(
  e: FieldErrors,
  field: string,
  raw: string | undefined,
  allowed: readonly string[],
): string | null {
  if (raw === undefined) return null;
  const value = cleanSingleLine(raw);
  if (value === '') return null;
  if (!allowed.includes(value)) {
    e.add(field, enumMessage(allowed));
    return null;
  }
  return value;
}

/** Canonical 8-4-4-4-12 hex. Route ids are checked with this BEFORE reaching SQL
 *  so an unparseable id is a 400 from the handler (contract section 6). */
export function isUuid(value: string): boolean {
  return /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/.test(value);
}

// ------------------------------------------------------------------ job input

/** The mutable fields of a job, cleaned and validated. */
export interface JobFields {
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
  is_featured: boolean;
  status: JobStatus;
  expires_at: string | null;
}

export interface ValidatedInput {
  readonly fields: JobFields;
  readonly errors: FieldErrors;
  /** Normalised, or `undefined` when the caller sent no `slug` at all. */
  readonly suppliedSlug: string | undefined;
}

/**
 * Validate a create or update body.
 *
 * `existing` is `null` for a create. Two rules depend on it, and both are
 * data-loss guards rather than conveniences:
 *
 *   * `expires_at` must be in the future ON CREATE ONLY. Requiring it on update
 *     would make an already-expired job impossible to edit — including to extend
 *     its expiry, the one edit it most needs.
 *   * an absent `status` keeps the CURRENT status on update, and only falls back
 *     to `draft` on create. Applying the create default to an update would mean a
 *     dashboard "Save" on a live job silently unpublishes it.
 *
 * The slug is not settled here: that needs a database round trip, which is the
 * service's job and not a validator's.
 */
export function validateJobInput(
  body: RawBody,
  existing: Pick<JobFields, 'status'> | null,
  now: Date,
  policy: ApplyUrlPolicy,
): ValidatedInput {
  const e = new FieldErrors();

  const title = requiredString(e, 'title', requiredRaw(body, 'title', e), LIMITS.title.min, LIMITS.title.max);
  const description = requiredText(
    e,
    'description',
    requiredRaw(body, 'description', e),
    LIMITS.description.min,
    LIMITS.description.max,
  );
  const salary = requiredString(e, 'salary', requiredRaw(body, 'salary', e), 1, LIMITS.salary.max);
  const qualification = requiredString(
    e,
    'qualification',
    requiredRaw(body, 'qualification', e),
    1,
    LIMITS.qualification.max,
  );
  const location = requiredString(e, 'location', requiredRaw(body, 'location', e), 1, LIMITS.location.max);
  const jobType = requiredEnum(e, 'job_type', requiredRaw(body, 'job_type', e), JOB_TYPES);

  // Only the OUTER whitespace is removed, which section 9 requires of every
  // string field. Anything inside is left alone and rejected, because a URL with
  // a space in the middle is not a URL and guessing what the admin meant would
  // break the byte-for-byte storage guarantee.
  const applyUrl = requiredRaw(body, 'apply_url', e).trim();
  const applyProblem = validateApplyUrl(applyUrl, policy);
  if (applyProblem !== null) e.add('apply_url', applyProblem);

  const isFeaturedRaw = body['is_featured'];
  let isFeatured = false;
  if (isFeaturedRaw !== undefined && isFeaturedRaw !== null) {
    if (typeof isFeaturedRaw === 'boolean') isFeatured = isFeaturedRaw;
    else e.add('is_featured', 'Must be true or false.');
  }

  let expiresAt: string | null = null;
  const expiresRaw = optionalRaw(body, 'expires_at', e)?.trim();
  if (expiresRaw !== undefined && expiresRaw !== '') {
    const parsed = parseTimestamp(expiresRaw);
    if (parsed === null) e.add('expires_at', TIMESTAMP_MESSAGE);
    else if (existing === null && parsed.getTime() <= now.getTime()) {
      e.add('expires_at', 'Must be in the future.');
    } else expiresAt = toRfc3339Utc(parsed);
  }

  const statusRaw = optionalRaw(body, 'status', e)?.trim();
  let status: JobStatus = existing?.status ?? 'draft';
  if (statusRaw !== undefined && statusRaw !== '') {
    // Narrowed rather than cast: an invalid value records an error and leaves
    // `status` at the safe fallback, so a bug that let the write proceed anyway
    // could not put a value the CHECK constraint rejects into the statement.
    const candidate = cleanSingleLine(statusRaw);
    if ((JOB_STATUSES as readonly string[]).includes(candidate)) status = candidate as JobStatus;
    else e.add('status', enumMessage(JOB_STATUSES));
  }

  // An explicitly supplied slug is normalised the same way a derived one is
  // (contract section 8), then checked against the format rule. Normalising
  // first means "Senior Cloud Engineer" typed into the slug field works instead
  // of failing a pattern match.
  let suppliedSlug: string | undefined;
  const slugRaw = optionalRaw(body, 'slug', e);
  if (slugRaw !== undefined) {
    const normalised = normaliseSuppliedSlug(slugRaw);
    if (normalised === '' || normalised.length > SUPPLIED_SLUG_MAX_LENGTH) {
      e.add('slug', 'Must contain only lowercase letters, numbers and single hyphens.');
    } else {
      suppliedSlug = normalised;
    }
  }

  return {
    fields: {
      title,
      description,
      salary,
      qualification,
      job_type: jobType,
      location,
      apply_url: applyUrl,
      company: optionalString(e, 'company', optionalRaw(body, 'company', e), LIMITS.company.max),
      country: optionalString(e, 'country', optionalRaw(body, 'country', e), LIMITS.country.max),
      remote_type: optionalEnum(e, 'remote_type', optionalRaw(body, 'remote_type', e), REMOTE_TYPES),
      experience_level: optionalString(
        e,
        'experience_level',
        optionalRaw(body, 'experience_level', e),
        LIMITS.experienceLevel.max,
      ),
      category: optionalString(e, 'category', optionalRaw(body, 'category', e), LIMITS.category.max),
      is_featured: isFeatured,
      status,
      expires_at: expiresAt,
    },
    errors: e,
    suppliedSlug,
  };
}

/**
 * Re-check a row that is ALREADY stored, before it becomes publicly visible.
 *
 * The row may have been written by something other than this API — a manual
 * `wrangler d1 execute`, a restored backup, a future import script — so
 * publishing is the moment to check, and it checks every field against the
 * policy the deployment is running with NOW. A job must never become publicly
 * applicable without a usable apply link (contract section 4).
 */
export function validateStoredJob(fields: JobFields, policy: ApplyUrlPolicy): FieldErrors {
  const e = new FieldErrors();
  requiredString(e, 'title', fields.title, LIMITS.title.min, LIMITS.title.max);
  requiredText(e, 'description', fields.description, LIMITS.description.min, LIMITS.description.max);
  requiredString(e, 'salary', fields.salary, 1, LIMITS.salary.max);
  requiredString(e, 'qualification', fields.qualification, 1, LIMITS.qualification.max);
  requiredString(e, 'location', fields.location, 1, LIMITS.location.max);
  requiredEnum(e, 'job_type', fields.job_type, JOB_TYPES);
  const problem = validateApplyUrl(fields.apply_url, policy);
  if (problem !== null) e.add('apply_url', problem);
  return e;
}

// ------------------------------------------------------------- query parsing

export interface ListQuery {
  page: number;
  perPage: number;
  q: string;
  location: string;
  country: string;
  jobType: string;
  remoteType: string;
  /** `null` means "no filter"; `false` means "only non-featured jobs". */
  featured: boolean | null;
  status: string;
  includeDeleted: boolean;
}

/** Strict: a non-numeric value is a bug in the caller, and quietly serving page
 *  1 hides it (contract section 3). */
function parseStrictInt(params: URLSearchParams, name: string, fallback: number): number {
  const raw = params.get(name)?.trim() ?? '';
  if (raw === '') return fallback;
  // `Number.parseInt` would accept "12abc"; the regex does not.
  if (!/^[+-]?\d+$/.test(raw)) throw errors.badRequest(`The ${name} parameter must be a number.`);
  return Number.parseInt(raw, 10);
}

/**
 * Lenient, and only for `per_page`.
 *
 * The contract makes `per_page` the exception: absent, `0` and unparseable all
 * mean "unspecified" and yield the default of 20. `page` is strict because a
 * wrong page silently returns the wrong rows, whereas a wrong `per_page` returns
 * the right rows in the default quantity.
 */
function parseLenientInt(params: URLSearchParams, name: string): number {
  const raw = params.get(name)?.trim() ?? '';
  if (!/^[+-]?\d+$/.test(raw)) return 0;
  return Number.parseInt(raw, 10);
}

function parseEnumParam(params: URLSearchParams, name: string, allowed: readonly string[]): string {
  const raw = params.get(name)?.trim() ?? '';
  if (raw === '') return '';
  if (!allowed.includes(raw)) {
    throw errors.badRequest(`The ${name} parameter must be one of: ${allowed.join(', ')}.`);
  }
  return raw;
}

function parseBoolParam(params: URLSearchParams, name: string): boolean | null {
  const raw = params.get(name)?.trim() ?? '';
  if (raw === '') return null;
  if (raw === 'true') return true;
  if (raw === 'false') return false;
  throw errors.badRequest(`The ${name} parameter must be true or false.`);
}

/**
 * Parse the query string for either list endpoint.
 *
 * A MALFORMED parameter is a 400; a merely out-of-range one is clamped later by
 * the service. `?page=abc` is a bug in the caller and quietly serving page 1
 * hides it, whereas `?page=0` from a hand-typed URL is worth being forgiving
 * about (contract section 3).
 */
export function parseListQuery(params: URLSearchParams, admin: boolean): ListQuery {
  const q = cleanSingleLine(params.get('q') ?? '');
  if (charLength(q) > MAX_SEARCH_LENGTH) {
    throw errors.badRequest(`The q parameter must be at most ${MAX_SEARCH_LENGTH} characters.`);
  }

  return {
    page: parseStrictInt(params, 'page', 1),
    // 0 / absent / unparseable all mean "unspecified", and the service turns
    // that into the default 20 rather than a useless one-row page.
    perPage: parseLenientInt(params, 'per_page'),
    q,
    location: cleanSingleLine(params.get('location') ?? ''),
    country: cleanSingleLine(params.get('country') ?? ''),
    jobType: parseEnumParam(params, 'job_type', JOB_TYPES),
    remoteType: parseEnumParam(params, 'remote_type', REMOTE_TYPES),
    featured: parseBoolParam(params, 'featured'),
    status: admin ? parseEnumParam(params, 'status', JOB_STATUSES) : '',
    includeDeleted: admin ? parseBoolParam(params, 'include_deleted') === true : false,
  };
}
