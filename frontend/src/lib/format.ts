/**
 * Presentation helpers: dates, enum labels, excerpts, description paragraphs.
 *
 * Deliberately free of any dependency on `copy.ts` or on Astro. These are pure
 * functions of their arguments so the unit tests can pin every result, and so
 * `now` is always injectable — a clock-dependent test is a flaky test.
 *
 * The API sends RFC 3339 UTC (contract section 1). Humanising is our job.
 */

// ------------------------------------------------------------------ constants

const MINUTE = 60_000;
const HOUR = 60 * MINUTE;
const DAY = 24 * HOUR;
const WEEK = 7 * DAY;

/**
 * `en-GB` is pinned rather than using the visitor's locale on purpose: this
 * string is rendered during SSR, so it must not vary with the request, or a
 * CDN-cached page would show one visitor's locale to everybody.
 */
const ABSOLUTE_DATE = new Intl.DateTimeFormat('en-GB', {
  day: 'numeric',
  month: 'short',
  year: 'numeric',
  timeZone: 'UTC',
});

/**
 * Known members of `JobType` / `RemoteType`. An unknown member must render
 * verbatim rather than crash or vanish (contract section 2.4), so these are
 * lookup tables with a passthrough default, not exhaustive switches.
 */
const JOB_TYPE_LABELS: Readonly<Record<string, string>> = {
  'full-time': 'Full-time',
  'part-time': 'Part-time',
  contract: 'Contract',
  internship: 'Internship',
  freelance: 'Freelance',
  temporary: 'Temporary',
};

const REMOTE_TYPE_LABELS: Readonly<Record<string, string>> = {
  remote: 'Remote',
  hybrid: 'Hybrid',
  onsite: 'On-site',
};

// ----------------------------------------------------------------- primitives

/** Trim, then treat a whitespace-only string as absent. */
export function nonEmpty(value: string | null | undefined): string | null {
  if (typeof value !== 'string') return null;
  const trimmed = value.trim();
  return trimmed.length > 0 ? trimmed : null;
}

/** Parse an API timestamp, returning null for absent or unparseable input. */
export function parseTimestamp(iso: string | null | undefined): Date | null {
  const raw = nonEmpty(iso);
  if (raw === null) return null;
  const date = new Date(raw);
  return Number.isNaN(date.getTime()) ? null : date;
}

// ---------------------------------------------------------------------- dates

/** `YYYY-MM-DD`, for the `datetime` attribute of `<time>`. */
export function isoDate(iso: string | null | undefined): string | null {
  const date = parseTimestamp(iso);
  if (date === null) return null;
  const asIso = date.toISOString();
  // toISOString is fixed-width, so a slice is safe and avoids a split() that
  // noUncheckedIndexedAccess would force us to null-check.
  return asIso.slice(0, 10);
}

/** e.g. `21 Aug 2026`. Always UTC, so it matches what the API stored. */
export function formatDate(iso: string | null | undefined): string | null {
  const date = parseTimestamp(iso);
  return date === null ? null : ABSOLUTE_DATE.format(date);
}

/**
 * Humanise a timestamp relative to `now`.
 *
 * Falls back to the absolute date beyond four weeks: "31 days ago" is less
 * useful to a job seeker than the actual date, and "1 month ago" would round
 * away information a candidate cares about.
 */
export function humaniseDate(iso: string | null | undefined, now: Date = new Date()): string | null {
  const date = parseTimestamp(iso);
  if (date === null) return null;

  const delta = now.getTime() - date.getTime();
  // A future timestamp (clock skew, or a scheduled publish) reads as new.
  if (delta < MINUTE) return 'Just now';
  if (delta < HOUR) return plural(Math.floor(delta / MINUTE), 'minute') + ' ago';
  if (delta < DAY) return plural(Math.floor(delta / HOUR), 'hour') + ' ago';

  const days = Math.floor(delta / DAY);
  if (days === 1) return 'Yesterday';
  if (days < 7) return `${days} days ago`;
  if (delta < 4 * WEEK) return plural(Math.floor(delta / WEEK), 'week') + ' ago';

  return ABSOLUTE_DATE.format(date);
}

function plural(count: number, unit: string): string {
  return `${count} ${unit}${count === 1 ? '' : 's'}`;
}

// --------------------------------------------------------------------- labels

/** Label for a `JobType`; unknown values pass through unchanged. */
export function jobTypeLabel(value: string | null | undefined): string | null {
  const raw = nonEmpty(value);
  if (raw === null) return null;
  return JOB_TYPE_LABELS[raw] ?? raw;
}

/** Label for a `RemoteType`; unknown values pass through unchanged. */
export function remoteTypeLabel(value: string | null | undefined): string | null {
  const raw = nonEmpty(value);
  if (raw === null) return null;
  return REMOTE_TYPE_LABELS[raw] ?? raw;
}

/** Join meta fragments with a middot, dropping the ones that are absent. */
export function joinMeta(parts: ReadonlyArray<string | null | undefined>, separator = ' · '): string | null {
  const kept = parts.map(nonEmpty).filter((part): part is string => part !== null);
  return kept.length > 0 ? kept.join(separator) : null;
}

// -------------------------------------------------------------------- excerpt

/**
 * Truncate on a word boundary and append an ellipsis.
 *
 * The API already computes `excerpt` server-side; this exists for the detail
 * page's meta description and as a fallback when `excerpt` is empty. Cutting
 * mid-word looks like a bug, and cutting mid-sentence then leaving a dangling
 * comma looks careless, so trailing punctuation is stripped before the ellipsis.
 */
export function truncateOnWord(text: string | null | undefined, maxLength = 200): string {
  const raw = nonEmpty(text);
  if (raw === null) return '';

  const collapsed = raw.replace(/\s+/g, ' ');
  if (collapsed.length <= maxLength) return collapsed;

  const window = collapsed.slice(0, maxLength);
  const lastSpace = window.lastIndexOf(' ');
  // A single token longer than maxLength has no space to cut at; a hard cut is
  // the only option there, and is still better than overflowing the meta tag.
  const cut = lastSpace > Math.floor(maxLength * 0.5) ? window.slice(0, lastSpace) : window;
  return `${cut.replace(/[\s,;:.\-–—]+$/u, '')}…`;
}

/** Prefer the server-computed excerpt; fall back to a local truncation. */
export function excerptFor(
  job: { excerpt?: string | null; description?: string | null },
  maxLength = 200,
): string {
  return nonEmpty(job.excerpt) ?? truncateOnWord(job.description, maxLength);
}

// --------------------------------------------------------------- descriptions

/**
 * Split a plain-text description into paragraphs.
 *
 * `description` is plain text with newlines, never HTML (contract section 2.2).
 * Returning an array of strings — rather than a joined HTML string — is what
 * makes it structurally impossible for a caller to reach for `set:html`: each
 * element goes into its own `<p>` and Astro escapes it.
 *
 * Single newlines inside a paragraph are preserved and rendered with
 * `white-space: pre-line`, so a bulleted list typed by an admin survives.
 */
export function descriptionParagraphs(text: string | null | undefined): string[] {
  const raw = nonEmpty(text);
  if (raw === null) return [];
  return raw
    .replace(/\r\n?/g, '\n')
    .split(/\n[ \t]*\n+/)
    .map((block) => block.trim())
    .filter((block) => block.length > 0);
}
