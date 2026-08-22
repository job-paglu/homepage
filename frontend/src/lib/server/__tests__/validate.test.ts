/**
 * Field validation, query parsing and the timestamp format — contract sections
 * 1, 3 and 9.
 *
 * The behaviours asserted here were verified against the Go implementation, so a
 * failure is a regression rather than a difference of opinion. The two most
 * expensive ones to get wrong are marked in place: `status` stability on update
 * (a data-loss bug) and the timestamp format (an ordering bug that only shows up
 * within a single second).
 */

import { describe, expect, it } from 'vitest';
import { ApiError } from '../respond';
import type { ApplyUrlPolicy } from '../env';
import {
  charLength,
  cleanMultiline,
  cleanSingleLine,
  isUuid,
  parseListQuery,
  toRfc3339Utc,
  validateJobInput,
  validateStoredJob,
  type JobFields,
} from '../validate';

const policy: ApplyUrlPolicy = { allowHttp: false, allowLocalhost: false };
const NOW = new Date('2026-08-21T09:41:07Z');

/** A body that passes every rule, so each test can break exactly one thing. */
function validBody(): Record<string, unknown> {
  return {
    title: 'Senior Cloud Engineer',
    description: 'A description that comfortably clears the twenty-character minimum.',
    salary: '$140k - $175k',
    qualification: 'B.Sc. Computer Science or equivalent experience',
    job_type: 'full-time',
    location: 'Berlin, Germany',
    apply_url: 'https://example.com/careers/senior-cloud-engineer',
  };
}

function fieldsFrom(body: Record<string, unknown>): JobFields {
  const { fields, errors } = validateJobInput(body, null, NOW, policy);
  expect(errors.toDetails()).toEqual({});
  return fields;
}

// ------------------------------------------------------------------ timestamps

describe('toRfc3339Utc', () => {
  /**
   * D1 stores timestamps as TEXT, so `expires_at > ?` is a STRING comparison.
   * That is only chronological while every value is the same fixed width. A
   * stray `.123Z` would sort "…45.678Z" before "…45Z" inside the same second.
   */
  it('emits UTC with a Z and no fractional seconds', () => {
    expect(toRfc3339Utc(new Date('2026-08-21T09:41:07.123Z'))).toBe('2026-08-21T09:41:07Z');
    expect(toRfc3339Utc(new Date('2026-08-21T11:41:07+02:00'))).toBe('2026-08-21T09:41:07Z');
    expect(toRfc3339Utc(new Date('2026-08-21T09:41:07Z'))).toHaveLength(20);
  });

  it('produces values whose string order is their time order', () => {
    const earlier = toRfc3339Utc(new Date('2026-08-21T09:41:45.678Z'));
    const later = toRfc3339Utc(new Date('2026-08-21T09:41:46.001Z'));
    expect(earlier < later).toBe(true);
  });
});

// -------------------------------------------------------------------- cleaning

describe('cleaning', () => {
  it('trims and strips control characters from a single-line field', () => {
    const raw = `  Sen${String.fromCodePoint(0x07)}ior  `;
    expect(cleanSingleLine(raw)).toBe('Senior');
  });

  it('keeps newlines and tabs in a description but normalises CRLF', () => {
    expect(cleanMultiline('one\r\ntwo\rthree')).toBe('one\ntwo\nthree');
    expect(cleanMultiline('a\tb')).toBe('a\tb');
    expect(cleanMultiline(`a${String.fromCodePoint(0x00)}b`)).toBe('ab');
  });

  it('counts code points, not UTF-16 units', () => {
    // A 160-unit cap would silently cut a Japanese title to half its length.
    expect(charLength('エンジニア')).toBe(5);
    expect(charLength('abc')).toBe(3);
  });
});

// ------------------------------------------------------------------ job input

describe('validateJobInput — defaults', () => {
  it('defaults status to draft and is_featured to false on create', () => {
    const fields = fieldsFrom(validBody());
    expect(fields.status).toBe('draft');
    expect(fields.is_featured).toBe(false);
    expect(fields.expires_at).toBeNull();
  });

  it('treats an absent, null and empty optional string identically', () => {
    // A form posting "" for an untouched optional input must not store "".
    for (const value of [undefined, null, '', '   ']) {
      const body = { ...validBody(), company: value };
      expect(fieldsFrom(body).company).toBeNull();
    }
  });
});

describe('validateJobInput — failures', () => {
  it('reports EVERY invalid field at once, not one per round trip', () => {
    const { errors } = validateJobInput(
      { title: 'ab', description: 'too short', job_type: 'permanent', apply_url: 'javascript:alert(1)' },
      null,
      NOW,
      policy,
    );
    const details = errors.toDetails();
    expect(Object.keys(details).sort()).toEqual([
      'apply_url', 'description', 'job_type', 'location', 'qualification', 'salary', 'title',
    ]);
  });

  it('rejects a dangerous apply_url with the section 7 message', () => {
    const { errors } = validateJobInput(
      { ...validBody(), apply_url: 'javascript:alert(1)' },
      null,
      NOW,
      policy,
    );
    expect(errors.toDetails()['apply_url']).toBe('Must be an absolute https:// URL.');
  });

  it('rejects a non-string where a string is required', () => {
    const { errors } = validateJobInput({ ...validBody(), title: 42 }, null, NOW, policy);
    expect(errors.toDetails()['title']).toBe('Must be text.');
  });
});

describe('validateJobInput — expires_at', () => {
  it('must be in the future on CREATE', () => {
    const past = { ...validBody(), expires_at: '2026-01-01T00:00:00Z' };
    expect(validateJobInput(past, null, NOW, policy).errors.toDetails()['expires_at']).toBe(
      'Must be in the future.',
    );
  });

  /**
   * Format only on UPDATE. Requiring a future value would make an already-expired
   * job impossible to edit — including to extend its expiry, which is the one
   * edit it most needs.
   */
  it('is format-only on UPDATE', () => {
    const past = { ...validBody(), expires_at: '2026-01-01T00:00:00Z' };
    const { errors, fields } = validateJobInput(past, { status: 'published' }, NOW, policy);
    expect(errors.toDetails()).toEqual({});
    expect(fields.expires_at).toBe('2026-01-01T00:00:00Z');
  });

  it('rejects a value that is not RFC 3339 in either mode', () => {
    for (const existing of [null, { status: 'published' as const }]) {
      const body = { ...validBody(), expires_at: 'March 2027' };
      expect(validateJobInput(body, existing, NOW, policy).errors.toDetails()['expires_at']).toMatch(
        /RFC 3339/,
      );
    }
  });

  it('normalises an offset to UTC without fractional seconds', () => {
    const body = { ...validBody(), expires_at: '2026-12-31T23:59:59.500+01:00' };
    expect(fieldsFrom(body).expires_at).toBe('2026-12-31T22:59:59Z');
  });
});

describe('validateJobInput — status stability', () => {
  /**
   * THE DATA-LOSS GUARD. If an absent `status` fell back to the create default of
   * `draft`, a dashboard "Save" on a live job would silently unpublish it.
   */
  it('keeps the existing status when the body omits it', () => {
    for (const status of ['published', 'unpublished', 'draft'] as const) {
      const { fields } = validateJobInput(validBody(), { status }, NOW, policy);
      expect(fields.status).toBe(status);
    }
  });

  it('keeps the existing status for null and empty-string too', () => {
    for (const value of [null, '', '  ']) {
      const body = { ...validBody(), status: value };
      expect(validateJobInput(body, { status: 'published' }, NOW, policy).fields.status).toBe('published');
    }
  });

  it('honours an explicit status', () => {
    const body = { ...validBody(), status: 'published' };
    expect(validateJobInput(body, { status: 'draft' }, NOW, policy).fields.status).toBe('published');
  });

  it('rejects an unknown status without adopting it', () => {
    const body = { ...validBody(), status: 'archived' };
    const { errors, fields } = validateJobInput(body, { status: 'published' }, NOW, policy);
    expect(errors.toDetails()['status']).toMatch(/Must be one of/);
    // The invalid value must not survive into the record even though the write is
    // already going to be refused.
    expect(fields.status).toBe('published');
  });
});

describe('validateJobInput — slug', () => {
  it('reports no supplied slug when the key is absent or null', () => {
    expect(validateJobInput(validBody(), null, NOW, policy).suppliedSlug).toBeUndefined();
    expect(
      validateJobInput({ ...validBody(), slug: null }, null, NOW, policy).suppliedSlug,
    ).toBeUndefined();
  });

  it('normalises a supplied slug the same way derivation does', () => {
    const body = { ...validBody(), slug: 'Senior Cloud Engineer' };
    expect(validateJobInput(body, null, NOW, policy).suppliedSlug).toBe('senior-cloud-engineer');
  });

  it('rejects a supplied slug that normalises to nothing', () => {
    const body = { ...validBody(), slug: '***' };
    const { errors, suppliedSlug } = validateJobInput(body, null, NOW, policy);
    expect(suppliedSlug).toBeUndefined();
    expect(errors.toDetails()['slug']).toMatch(/lowercase letters/);
  });
});

// ---------------------------------------------------------------- stored rows

describe('validateStoredJob', () => {
  it('accepts a valid stored row', () => {
    expect(validateStoredJob(fieldsFrom(validBody()), policy).toDetails()).toEqual({});
  });

  /**
   * Publishing re-checks the STORED row, because a row can reach an invalid state
   * through a direct `wrangler d1 execute` or a restored backup. A job must never
   * become publicly applicable without a usable apply link.
   */
  it('rejects a row whose apply_url would be dangerous when public', () => {
    const tampered: JobFields = { ...fieldsFrom(validBody()), apply_url: 'javascript:alert(1)' };
    expect(validateStoredJob(tampered, policy).toDetails()['apply_url']).toBeDefined();
  });

  it('rejects a row whose required text was emptied out of band', () => {
    const tampered: JobFields = { ...fieldsFrom(validBody()), title: '' };
    expect(validateStoredJob(tampered, policy).toDetails()['title']).toBe('This field is required.');
  });
});

// -------------------------------------------------------------- query parsing

function query(search: string): URLSearchParams {
  return new URLSearchParams(search);
}

describe('parseListQuery', () => {
  it('defaults page to 1 and leaves per_page unspecified', () => {
    const parsed = parseListQuery(query(''), false);
    expect(parsed.page).toBe(1);
    // 0 means "unspecified"; the service turns it into the default of 20.
    expect(parsed.perPage).toBe(0);
    expect(parsed.featured).toBeNull();
  });

  it('rejects a non-numeric page with BAD_REQUEST', () => {
    // A frontend sending ?page=abc has a bug, and quietly serving page 1 hides it.
    expect(() => parseListQuery(query('page=abc'), false)).toThrow(ApiError);
    try {
      parseListQuery(query('page=12abc'), false);
      expect.unreachable('should have thrown');
    } catch (err) {
      expect((err as ApiError).code).toBe('BAD_REQUEST');
    }
  });

  it('treats an unparseable per_page as unspecified rather than an error', () => {
    // Contract section 3 makes per_page the deliberate exception: a wrong
    // per_page returns the right rows in the default quantity, whereas a wrong
    // page returns the wrong rows.
    expect(parseListQuery(query('per_page=abc'), false).perPage).toBe(0);
    expect(parseListQuery(query('per_page=0'), false).perPage).toBe(0);
    expect(parseListQuery(query('per_page=25'), false).perPage).toBe(25);
  });

  it('rejects a q over 100 characters instead of truncating it', () => {
    // Quietly searching for something other than what was asked returns
    // confidently wrong results.
    expect(parseListQuery(query(`q=${'a'.repeat(100)}`), false).q).toHaveLength(100);
    expect(() => parseListQuery(query(`q=${'a'.repeat(101)}`), false)).toThrow(ApiError);
  });

  it('rejects unknown enum and boolean values', () => {
    expect(() => parseListQuery(query('job_type=permanent'), false)).toThrow(ApiError);
    expect(() => parseListQuery(query('remote_type=lunar'), false)).toThrow(ApiError);
    expect(() => parseListQuery(query('featured=yes'), false)).toThrow(ApiError);
    expect(parseListQuery(query('featured=false'), false).featured).toBe(false);
  });

  it('ignores the admin-only parameters on the public endpoint', () => {
    const parsed = parseListQuery(query('status=draft&include_deleted=true'), false);
    expect(parsed.status).toBe('');
    expect(parsed.includeDeleted).toBe(false);
    // …and does not even validate them, so a stray public ?status=nonsense is
    // not a 400 for a parameter that endpoint does not have.
    expect(() => parseListQuery(query('status=nonsense'), false)).not.toThrow();
  });

  it('honours the admin-only parameters on the admin endpoint', () => {
    const parsed = parseListQuery(query('status=draft&include_deleted=true'), true);
    expect(parsed.status).toBe('draft');
    expect(parsed.includeDeleted).toBe(true);
    expect(() => parseListQuery(query('status=nonsense'), true)).toThrow(ApiError);
  });
});

describe('isUuid', () => {
  it('accepts a canonical UUID in either case', () => {
    expect(isUuid('8f14e45f-ceea-467a-9c5b-2b1a0e1c9d33')).toBe(true);
    expect(isUuid('8F14E45F-CEEA-467A-9C5B-2B1A0E1C9D33')).toBe(true);
  });

  it('rejects anything else, so a hostile id never reaches a query', () => {
    for (const bad of [
      '',
      'not-a-uuid',
      '8f14e45f-ceea-467a-9c5b-2b1a0e1c9d3', // one short
      '8f14e45fceea467a9c5b2b1a0e1c9d33', // no hyphens
      "8f14e45f-ceea-467a-9c5b-2b1a0e1c9d33' OR '1'='1",
    ]) {
      expect(isUuid(bad)).toBe(false);
    }
  });
});
