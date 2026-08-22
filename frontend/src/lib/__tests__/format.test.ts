import { describe, expect, it } from 'vitest';

import {
  descriptionParagraphs,
  excerptFor,
  formatDate,
  humaniseDate,
  isoDate,
  joinMeta,
  jobTypeLabel,
  nonEmpty,
  remoteTypeLabel,
  truncateOnWord,
} from '../format';

/**
 * Every date assertion pins `now` explicitly. A test that calls `new Date()`
 * passes for a week and then fails at a month boundary, which is the worst kind
 * of flake: it looks like a regression in code that never changed.
 */
const NOW = new Date('2026-08-21T12:00:00Z');

describe('humaniseDate', () => {
  it('returns null for absent or unparseable input', () => {
    expect(humaniseDate(null, NOW)).toBeNull();
    expect(humaniseDate(undefined, NOW)).toBeNull();
    expect(humaniseDate('', NOW)).toBeNull();
    expect(humaniseDate('not a date', NOW)).toBeNull();
  });

  it('reads a future timestamp as new rather than negative', () => {
    // Clock skew between the API host and the renderer is normal. "-3 minutes
    // ago" would be a visible bug; "Just now" is honest enough.
    expect(humaniseDate('2026-08-21T12:05:00Z', NOW)).toBe('Just now');
  });

  it('humanises within the day', () => {
    expect(humaniseDate('2026-08-21T11:59:30Z', NOW)).toBe('Just now');
    expect(humaniseDate('2026-08-21T11:59:00Z', NOW)).toBe('1 minute ago');
    expect(humaniseDate('2026-08-21T11:15:00Z', NOW)).toBe('45 minutes ago');
    expect(humaniseDate('2026-08-21T11:00:00Z', NOW)).toBe('1 hour ago');
    expect(humaniseDate('2026-08-21T01:00:00Z', NOW)).toBe('11 hours ago');
  });

  it('humanises days and weeks', () => {
    expect(humaniseDate('2026-08-20T12:00:00Z', NOW)).toBe('Yesterday');
    expect(humaniseDate('2026-08-18T12:00:00Z', NOW)).toBe('3 days ago');
    expect(humaniseDate('2026-08-14T12:00:00Z', NOW)).toBe('1 week ago');
    expect(humaniseDate('2026-08-01T12:00:00Z', NOW)).toBe('2 weeks ago');
  });

  it('falls back to the absolute date beyond four weeks', () => {
    // "31 days ago" is less useful to a candidate than the real date, and
    // "1 month ago" would round away information they care about.
    expect(humaniseDate('2026-07-01T12:00:00Z', NOW)).toBe('1 Jul 2026');
  });
});

describe('formatDate / isoDate', () => {
  it('formats in UTC, not the runner locale or timezone', () => {
    // 23:30Z is already the 22nd in Asia/Kolkata. Rendering during SSR means the
    // server's timezone must not leak into a CDN-cached page.
    expect(formatDate('2026-08-21T23:30:00Z')).toBe('21 Aug 2026');
    expect(isoDate('2026-08-21T23:30:00Z')).toBe('2026-08-21');
  });

  it('returns null rather than "Invalid Date"', () => {
    expect(formatDate('nonsense')).toBeNull();
    expect(isoDate(null)).toBeNull();
  });
});

describe('jobTypeLabel', () => {
  it('labels every documented member', () => {
    expect(jobTypeLabel('full-time')).toBe('Full-time');
    expect(jobTypeLabel('part-time')).toBe('Part-time');
    expect(jobTypeLabel('contract')).toBe('Contract');
    expect(jobTypeLabel('internship')).toBe('Internship');
    expect(jobTypeLabel('freelance')).toBe('Freelance');
    expect(jobTypeLabel('temporary')).toBe('Temporary');
  });

  it('passes an unknown member through verbatim', () => {
    // Contract section 2.4: the API may add an enum member before the frontend
    // knows about it, and the client must display it rather than crash or blank.
    expect(jobTypeLabel('job-share')).toBe('job-share');
    expect(jobTypeLabel('  seasonal  ')).toBe('seasonal');
  });

  it('returns null for absent input', () => {
    expect(jobTypeLabel(null)).toBeNull();
    expect(jobTypeLabel('   ')).toBeNull();
  });
});

describe('remoteTypeLabel', () => {
  it('labels members and passes unknowns through', () => {
    expect(remoteTypeLabel('remote')).toBe('Remote');
    expect(remoteTypeLabel('hybrid')).toBe('Hybrid');
    expect(remoteTypeLabel('onsite')).toBe('On-site');
    expect(remoteTypeLabel('flexible')).toBe('flexible');
    expect(remoteTypeLabel(null)).toBeNull();
  });
});

describe('truncateOnWord', () => {
  const sentence = 'Own the platform that serves millions of requests every single day worldwide';

  it('returns short text untouched', () => {
    expect(truncateOnWord('Short enough', 40)).toBe('Short enough');
  });

  it('cuts on a word boundary, never mid-word', () => {
    const result = truncateOnWord(sentence, 30);
    expect(result.endsWith('…')).toBe(true);
    expect(result).toBe('Own the platform that serves…');
    // The boundary is a real one: no partial word survives the cut.
    expect(sentence.startsWith(result.slice(0, -1))).toBe(true);
  });

  it('strips trailing punctuation before the ellipsis', () => {
    expect(truncateOnWord('Berlin, Germany, and remote too', 8)).toBe('Berlin…');
  });

  it('hard-cuts a single token with no boundary to use', () => {
    const token = 'a'.repeat(50);
    expect(truncateOnWord(token, 10)).toBe(`${'a'.repeat(10)}…`);
  });

  it('collapses internal whitespace so a meta description stays one line', () => {
    expect(truncateOnWord('one\n\ntwo\tthree', 100)).toBe('one two three');
  });

  it('returns an empty string for absent input', () => {
    expect(truncateOnWord(null)).toBe('');
    expect(truncateOnWord('   ')).toBe('');
  });
});

describe('excerptFor', () => {
  it('prefers the server-computed excerpt', () => {
    expect(excerptFor({ excerpt: 'From the API', description: 'Ignored' })).toBe('From the API');
  });

  it('falls back to the description when the excerpt is blank', () => {
    expect(excerptFor({ excerpt: '   ', description: 'Local fallback' })).toBe('Local fallback');
  });
});

describe('descriptionParagraphs', () => {
  it('splits on blank lines and keeps single newlines inside a block', () => {
    // The single newline survives because the detail page renders each block
    // with `white-space: pre-line`. It must not be turned into markup.
    const input = 'First paragraph.\n\nSecond line one\nline two\n\n\nThird.';
    expect(descriptionParagraphs(input)).toEqual([
      'First paragraph.',
      'Second line one\nline two',
      'Third.',
    ]);
  });

  it('normalises CRLF', () => {
    expect(descriptionParagraphs('a\r\n\r\nb')).toEqual(['a', 'b']);
  });

  it('returns an empty array for absent input', () => {
    expect(descriptionParagraphs(null)).toEqual([]);
    expect(descriptionParagraphs('\n\n  \n')).toEqual([]);
  });

  it('does not interpret markup — it only splits text', () => {
    const input = '<script>alert(1)</script>\n\n<b>bold</b>';
    expect(descriptionParagraphs(input)).toEqual(['<script>alert(1)</script>', '<b>bold</b>']);
  });
});

describe('nonEmpty / joinMeta', () => {
  it('treats whitespace-only as absent', () => {
    expect(nonEmpty('  x  ')).toBe('x');
    expect(nonEmpty('   ')).toBeNull();
    expect(nonEmpty(null)).toBeNull();
  });

  it('joins only the parts that are present', () => {
    expect(joinMeta(['Berlin', null, 'Full-time', '  '])).toBe('Berlin · Full-time');
    expect(joinMeta([null, undefined])).toBeNull();
  });
});
