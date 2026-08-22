/**
 * Field-level rules for the admin job form.
 *
 * These mirror API-CONTRACT.md sections 7 (apply_url), 8 (slugs) and 9 (field
 * validation). They exist for *fast feedback only*. The Go backend re-validates
 * everything and is the sole authority; a value that passes here can still be
 * rejected with 422, and that rejection is correct, not a bug in this file.
 *
 * It lives under `components/admin/` rather than `src/lib/` deliberately: the
 * public site owns `src/lib/slug.ts` and `src/lib/format.ts`, and duplicating a
 * small algorithm is cheaper than two agents fighting over one module.
 */

// ------------------------------------------------------------------ slug rules

/** Contract section 8: a derived slug matching one of these gets `-job`. */
export const RESERVED_SLUGS = [
  'new',
  'admin',
  'api',
  'jobs',
  'login',
  'logout',
  'health',
  'ready',
  'sitemap',
  'robots',
] as const;

export const SLUG_MAX_LENGTH = 80;
export const SLUG_PATTERN = /^[a-z0-9]+(-[a-z0-9]+)*$/;

/**
 * Derive a slug from a title, following contract section 8 step for step.
 *
 * Returns `''` for input that reduces to nothing (a CJK-only title, say). The
 * server substitutes `job-<first 8 of uuid>` in that case, and the client
 * cannot know the UUID, so the caller must show a placeholder rather than
 * pretend to know the final URL.
 */
export function deriveSlug(title: string): string {
  // NFKD then drop combining marks, so "Senor" survives as letters rather than
  // losing the accented character to the non-alphanumeric sweep below.
  const folded = title.normalize('NFKD').replace(/\p{M}+/gu, '').toLowerCase();

  let slug = folded.replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');

  if (slug.length > SLUG_MAX_LENGTH) {
    slug = slug.slice(0, SLUG_MAX_LENGTH);
    const boundary = slug.lastIndexOf('-');
    // Cut back to a hyphen so the slug never ends mid-word. If the first 80
    // characters contain no hyphen there is no boundary to honour, so the hard
    // truncation stands.
    if (boundary > 0) slug = slug.slice(0, boundary);
    slug = slug.replace(/-+$/, '');
  }

  if (slug === '') return '';
  if ((RESERVED_SLUGS as readonly string[]).includes(slug)) return `${slug}-job`;
  return slug;
}

export function isValidSlug(value: string): boolean {
  return value.length >= 1 && value.length <= 100 && SLUG_PATTERN.test(value);
}

// ------------------------------------------------------------- apply_url rules

export const APPLY_URL_MAX_LENGTH = 2048;

/** Schemes worth naming explicitly, because the generic message is unhelpful. */
const DANGEROUS_SCHEMES: Readonly<Record<string, string>> = {
  'javascript:': 'javascript:',
  'data:': 'data:',
  'vbscript:': 'vbscript:',
  'file:': 'file:',
  'blob:': 'blob:',
  'about:': 'about:',
};

/**
 * Whitespace anywhere, plus C0/C1 control characters and DEL.
 *
 * Written as a code-point scan rather than a regex character class on purpose:
 * a literal control character inside a source-level `[...]` is invisible in a
 * diff and trivially corrupted by tooling, whereas `0x20` is unambiguous.
 */
export function hasUnsafeUrlChars(value: string): boolean {
  if (/\s/.test(value)) return true;
  for (const char of value) {
    const code = char.codePointAt(0) ?? 0;
    if (code < 0x20 || code === 0x7f || (code >= 0x80 && code <= 0x9f)) return true;
  }
  return false;
}


function developmentMode(): boolean {
  return import.meta.env.APP_ENV === 'development';
}

/**
 * Validate an apply URL the way contract section 7 does. Returns a
 * human-readable problem, or `null` when the value looks acceptable.
 *
 * Control characters and length are checked *before* parsing, because the URL
 * parser silently strips tabs and newlines - a value that only passes after
 * that stripping is not the value the admin typed, and the server sees the raw
 * bytes.
 */
export function validateApplyUrl(raw: string): string | null {
  const value = raw.trim();
  if (value === '') return 'An apply link is required.';
  if (value.length > APPLY_URL_MAX_LENGTH) {
    return `Must be ${APPLY_URL_MAX_LENGTH} characters or fewer.`;
  }
  if (hasUnsafeUrlChars(value)) {
    return 'Must not contain spaces, tabs, or line breaks.';
  }

  let url: URL;
  try {
    url = new URL(value);
  } catch {
    return 'Must be an absolute URL that starts with https://';
  }

  const named = DANGEROUS_SCHEMES[url.protocol];
  if (named !== undefined) return `Links using ${named} are never allowed.`;

  const allowHttp = developmentMode();
  if (url.protocol !== 'https:' && !(allowHttp && url.protocol === 'http:')) {
    return url.protocol === 'http:'
      ? 'Must use https:// - http:// is accepted only in development.'
      : 'Must be an absolute https:// URL.';
  }

  // https://user:pass@host smuggles credentials into a link visitors click.
  if (url.username !== '' || url.password !== '') {
    return 'Must not contain a username or password before the hostname.';
  }

  const host = url.hostname;
  if (host === '') return 'Must include a hostname.';
  const localhostAllowed = allowHttp && (host === 'localhost' || host === '127.0.0.1');
  if (!host.includes('.') && !localhostAllowed) {
    return 'The hostname must contain a dot, for example example.com.';
  }

  return null;
}

// --------------------------------------------------------------- field limits

export interface FieldLimit {
  readonly min?: number;
  readonly max: number;
}

/** Contract section 9, character counts after trimming. */
export const FIELD_LIMITS: Readonly<Record<string, FieldLimit>> = {
  title: { min: 3, max: 160 },
  description: { min: 20, max: 20_000 },
  salary: { min: 1, max: 120 },
  qualification: { min: 1, max: 240 },
  location: { min: 1, max: 160 },
  apply_url: { min: 1, max: APPLY_URL_MAX_LENGTH },
  company: { max: 160 },
  country: { max: 80 },
  experience_level: { max: 80 },
  category: { max: 80 },
  slug: { min: 1, max: 100 },
};

/**
 * Order used when deciding which invalid field to move focus to. Matches the
 * visual order of the form so focus never jumps backwards past a field the user
 * can already see.
 */
export const JOB_FIELD_ORDER = [
  'title',
  'slug',
  'description',
  'salary',
  'qualification',
  'job_type',
  'location',
  'apply_url',
  'company',
  'country',
  'remote_type',
  'experience_level',
  'category',
  'is_featured',
  'expires_at',
  'status',
] as const;

export function firstFieldInOrder(names: readonly string[]): string | undefined {
  for (const candidate of JOB_FIELD_ORDER) {
    if (names.includes(candidate)) return candidate;
  }
  return names[0];
}

// ------------------------------------------------------------------ formatting

/**
 * Dates are rendered in the viewer's locale. Safe here because every admin view
 * paints its rows only after a browser fetch, so there is no server-rendered
 * string for a differing server locale to disagree with.
 */
export function formatDateTime(iso: string | null): string {
  if (!iso) return '—';
  const parsed = new Date(iso);
  if (Number.isNaN(parsed.getTime())) return iso;
  return new Intl.DateTimeFormat(undefined, { dateStyle: 'medium', timeStyle: 'short' }).format(parsed);
}

/** ISO 8601 UTC to the `YYYY-MM-DDTHH:mm` an `<input type="datetime-local">` wants. */
export function toDateTimeLocal(iso: string | null): string {
  if (!iso) return '';
  const parsed = new Date(iso);
  if (Number.isNaN(parsed.getTime())) return '';
  const pad = (value: number) => String(value).padStart(2, '0');
  return (
    `${parsed.getFullYear()}-${pad(parsed.getMonth() + 1)}-${pad(parsed.getDate())}` +
    `T${pad(parsed.getHours())}:${pad(parsed.getMinutes())}`
  );
}

/** The reverse: a local wall-clock value to RFC 3339 UTC, or null when blank. */
export function fromDateTimeLocal(value: string): string | null {
  if (value.trim() === '') return null;
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return null;
  return parsed.toISOString();
}

/** `full-time` to `Full-time`. An unknown member is shown verbatim (contract 2.4). */
export function jobTypeLabel(value: string): string {
  if (value === '') return '—';
  return value.charAt(0).toUpperCase() + value.slice(1);
}
