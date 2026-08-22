/**
 * Slug derivation, normalisation and de-duplication — contract section 8.
 *
 * Derivation is NOT reimplemented here. It is imported from
 * `src/components/admin/jobFields.ts`, which already implements section 8 step
 * for step and is what the admin form uses for its live slug preview. Sharing
 * the one function is not tidiness: if the server derived slugs differently, the
 * preview an admin sees while typing would be a lie, and they would only find
 * out after saving. One implementation means the preview is the answer.
 *
 * (That module is pure TypeScript with no React and no DOM access, so it is safe
 * to import into Worker code.)
 */

import { RESERVED_SLUGS, SLUG_MAX_LENGTH, deriveSlug, isValidSlug } from '../../components/admin/jobFields';

export { RESERVED_SLUGS, SLUG_MAX_LENGTH, isValidSlug };

/** An explicit slug may be longer than a derived one (contract section 9). */
export const SUPPLIED_SLUG_MAX_LENGTH = 100;

/**
 * Derive a slug from a title.
 *
 * `deriveSlug` returns `''` for a title with nothing alphanumeric in it — a
 * CJK-only title, or `"***"`. Section 8 step 6 specifies `job-<first 8 of uuid>`
 * for that case, which only the server can produce, which is precisely why the
 * client shows a placeholder instead of guessing.
 */
export function deriveSlugForTitle(title: string, id: string): string {
  const derived = deriveSlug(title);
  if (derived !== '') return derived;
  return `job-${id.replace(/-/g, '').slice(0, 8)}`;
}

/**
 * Normalise an admin-supplied slug.
 *
 * The contract requires the same transformation as derivation ("When supplied it
 * is normalised the same way"), so an admin who types "Senior Cloud Engineer"
 * into the slug field gets a working slug instead of a pattern-match error.
 *
 * Two differences from `deriveSlug`, both matching the Go reference's
 * `NormaliseSlug`:
 *
 *   * the limit is 100, not 80 — a derived slug is kept shorter so the `-2`
 *     de-duplication suffix always fits, but an explicit slug is never
 *     de-duplicated, so it gets the contract's full field length;
 *   * a reserved word is NOT escaped to `-job` here. An explicit slug that
 *     collides with a reserved word is reported to the admin (see
 *     `isReservedSlug`) rather than silently changed into a different URL from
 *     the one they asked for.
 *
 * Returns `''` when nothing usable survives, which the caller reports as a
 * format failure.
 */
export function normaliseSuppliedSlug(raw: string): string {
  const folded = raw
    .normalize('NFKD')
    .replace(/\p{M}+/gu, '')
    .toLowerCase();

  let slug = folded.replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '');
  if (slug.length <= SUPPLIED_SLUG_MAX_LENGTH) return slug;

  slug = slug.slice(0, SUPPLIED_SLUG_MAX_LENGTH);
  const boundary = slug.lastIndexOf('-');
  // Cut back to a hyphen so the slug never ends mid-word. One very long word has
  // no boundary to honour, so the hard truncation stands.
  if (boundary > 0) slug = slug.slice(0, boundary);
  return slug.replace(/-+$/, '');
}

/** A job may never occupy a path the site routes itself (contract section 8). */
export function isReservedSlug(slug: string): boolean {
  return (RESERVED_SLUGS as readonly string[]).includes(slug);
}

/** Answers "is this slug already used by a live job other than `excludeId`?" */
export type SlugTaken = (candidate: string) => Promise<boolean>;

/**
 * Return `base`, or `base-2`, `base-3`, … — the first candidate that is free
 * (contract section 8).
 *
 * The loop has a ceiling because it is talking to a database: a bug that made
 * `taken` always true would otherwise be an unbounded query storm. Hitting the
 * ceiling falls back to a random suffix, which cannot collide in practice.
 *
 * No lock is taken. The Postgres version did not need one either — the partial
 * unique index is the real arbiter, and D1 has a single writer per database, so
 * this read-then-insert cannot interleave with another writer at all.
 */
export async function deduplicateSlug(base: string, taken: SlugTaken): Promise<string> {
  const maxAttempts = 50;

  if (!(await taken(base))) return base;

  for (let suffix = 2; suffix <= maxAttempts; suffix += 1) {
    const candidate = `${base}-${suffix}`;
    if (!(await taken(candidate))) return candidate;
  }

  const random = crypto.getRandomValues(new Uint8Array(4));
  const hex = Array.from(random, (byte) => byte.toString(16).padStart(2, '0')).join('');
  return `${base}-${hex}`;
}
