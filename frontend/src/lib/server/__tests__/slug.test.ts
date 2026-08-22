/**
 * Slug derivation, normalisation and de-duplication — contract section 8.
 *
 * Derivation itself lives in `components/admin/jobFields.ts` and is exercised
 * here through the server wrapper, because the property that matters is that the
 * SERVER and the admin form's live preview agree. A test that only covered the
 * wrapper would let them diverge.
 */

import { describe, expect, it, vi } from 'vitest';
import {
  RESERVED_SLUGS,
  deduplicateSlug,
  deriveSlugForTitle,
  isReservedSlug,
  isValidSlug,
  normaliseSuppliedSlug,
} from '../slug';

const ID = '8f14e45f-ceea-467a-9c5b-2b1a0e1c9d33';

describe('deriveSlugForTitle', () => {
  it('lowercases and hyphenates', () => {
    expect(deriveSlugForTitle('Senior Cloud Engineer', ID)).toBe('senior-cloud-engineer');
  });

  it('folds accents instead of dropping the letter', () => {
    // "Señor" must become "senor", not "se-or". Both precomposed and decomposed
    // input have to work, which is why derivation normalises to NFKD first.
    const precomposed = 'Señor Ingeniero'; // one code point: N WITH TILDE
    const decomposed = 'Señor Ingeniero'; // two: 'n' + COMBINING TILDE
    expect(precomposed).not.toBe(decomposed); // genuinely different inputs
    expect(deriveSlugForTitle(precomposed, ID)).toBe('senor-ingeniero');
    expect(deriveSlugForTitle(decomposed, ID)).toBe('senor-ingeniero');
    expect(deriveSlugForTitle('Café Manager', ID)).toBe('cafe-manager');
  });

  it('collapses every run of non-alphanumerics to a single hyphen', () => {
    expect(deriveSlugForTitle('  C++ / Rust  Engineer!!! ', ID)).toBe('c-rust-engineer');
    expect(deriveSlugForTitle('Head-of--Engineering', ID)).toBe('head-of-engineering');
  });

  it('truncates to 80 characters on a hyphen boundary', () => {
    const slug = deriveSlugForTitle(`${'alpha '.repeat(20)}omega`, ID);
    expect(slug.length).toBeLessThanOrEqual(80);
    expect(slug.endsWith('-')).toBe(false);
    // Cut on a boundary means no half word at the end.
    expect(slug.split('-').every((part) => part === 'alpha')).toBe(true);
  });

  it('falls back to job-<first 8 of uuid> when nothing alphanumeric survives', () => {
    // Contract section 8 step 6. Only the server can produce this, which is why
    // the admin form shows a placeholder instead of guessing.
    expect(deriveSlugForTitle('エンジニア', ID)).toBe('job-8f14e45f');
    expect(deriveSlugForTitle('***', ID)).toBe('job-8f14e45f');
    expect(deriveSlugForTitle('   ', ID)).toBe('job-8f14e45f');
  });

  it('escapes a reserved word with -job rather than replacing it', () => {
    // "admin" becomes "admin-job", not "job-3f2a91bc": the slug stays
    // recognisable to the admin who typed the title.
    expect(deriveSlugForTitle('Admin', ID)).toBe('admin-job');
    expect(deriveSlugForTitle('Jobs', ID)).toBe('jobs-job');
    for (const reserved of RESERVED_SLUGS) {
      expect(deriveSlugForTitle(reserved, ID)).toBe(`${reserved}-job`);
    }
  });
});

describe('normaliseSuppliedSlug', () => {
  it('normalises an admin-typed value the same way derivation does', () => {
    // Contract section 4: "When supplied it is normalised the same way", so
    // typing a title into the slug field works instead of failing a pattern
    // match.
    expect(normaliseSuppliedSlug('Senior Cloud Engineer')).toBe('senior-cloud-engineer');
    expect(normaliseSuppliedSlug('--Senior--Engineer--')).toBe('senior-engineer');
  });

  it('allows up to 100 characters, unlike an 80-character derived slug', () => {
    // A derived slug is capped shorter so the "-2" de-duplication suffix always
    // fits; an explicit slug is never de-duplicated and gets the contract's full
    // field length.
    const long = 'a'.repeat(95);
    expect(normaliseSuppliedSlug(long)).toBe(long);
    expect(normaliseSuppliedSlug('b'.repeat(140)).length).toBeLessThanOrEqual(100);
  });

  it('does NOT escape a reserved word', () => {
    // A reserved explicit slug is reported to the admin instead of being quietly
    // turned into a different URL from the one they asked for.
    expect(normaliseSuppliedSlug('admin')).toBe('admin');
    expect(isReservedSlug('admin')).toBe(true);
    expect(isReservedSlug('senior-cloud-engineer')).toBe(false);
  });

  it('returns an empty string when nothing usable survives', () => {
    expect(normaliseSuppliedSlug('***')).toBe('');
    expect(normaliseSuppliedSlug('エンジニア')).toBe('');
  });
});

describe('isValidSlug', () => {
  it('accepts lowercase alphanumeric groups joined by single hyphens', () => {
    for (const good of ['a', 'a1', 'senior-cloud-engineer', 'job-8f14e45f', '2026-intern']) {
      expect(isValidSlug(good)).toBe(true);
    }
  });

  it('rejects anything else', () => {
    for (const bad of ['', '-lead', 'lead-', 'double--hyphen', 'Upper', 'has space', 'sl/ash', 'a'.repeat(101)]) {
      expect(isValidSlug(bad)).toBe(false);
    }
  });
});

describe('deduplicateSlug', () => {
  it('returns the base slug when it is free', async () => {
    const taken = vi.fn<(candidate: string) => Promise<boolean>>().mockResolvedValue(false);
    await expect(deduplicateSlug('senior-cloud-engineer', taken)).resolves.toBe('senior-cloud-engineer');
    expect(taken).toHaveBeenCalledTimes(1);
  });

  it('appends -2, then -3, until one is free', async () => {
    const used = new Set(['role', 'role-2', 'role-3']);
    const taken = (candidate: string) => Promise.resolve(used.has(candidate));
    await expect(deduplicateSlug('role', taken)).resolves.toBe('role-4');
  });

  it('falls back to a random suffix rather than looping for ever', async () => {
    // A bug that made every candidate look taken would otherwise be an unbounded
    // query storm against the database.
    const taken = () => Promise.resolve(true);
    const result = await deduplicateSlug('role', taken);
    expect(result).toMatch(/^role-[0-9a-f]{8}$/);
  });
});
