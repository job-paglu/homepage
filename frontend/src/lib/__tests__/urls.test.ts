/**
 * Tests for the render-time apply-URL guard and the URL helpers.
 *
 * `isSafeApplyUrl` is the most security-sensitive function in the frontend: its
 * output decides whether a database value becomes a live `href` in a visitor's
 * browser. It was written without tests because the original brief did not list
 * this file; that was the wrong call for exactly this function, so it is covered
 * here.
 *
 * The Go backend has an independent equivalent
 * (`backend/internal/validation/applyurl_test.go`). Both must reject the same
 * inputs. If one is changed, change the other — a divergence means a value the
 * server accepts renders as a disabled link, or worse, the reverse.
 *
 * `vitest.config.ts` pins `APP_ENV=production`, so the strict path is the
 * default and the development-only `http:`/`localhost` allowances have to be
 * opted into explicitly per test.
 */

import { afterEach, describe, expect, it, vi } from 'vitest';

import {
  absoluteUrl,
  applyUrlHost,
  canonicalUrl,
  isCurrentPath,
  isSafeApplyUrl,
  jobListPath,
  jobPath,
  readPageParam,
  ROUTES,
  siteOrigin,
} from '../urls';

afterEach(() => {
  vi.unstubAllEnvs();
});

// ---------------------------------------------------------------------------
// isSafeApplyUrl — rejection
// ---------------------------------------------------------------------------

describe('isSafeApplyUrl: dangerous schemes', () => {
  // The check is an allowlist, so this list is illustrative rather than
  // exhaustive — a scheme invented tomorrow is rejected without a code change.
  const cases = [
    'javascript:alert(1)',
    'JavaScript:alert(1)', // URL lowercases the protocol, so casing cannot bypass
    'JAVASCRIPT:alert(1)',
    'data:text/html,<script>alert(1)</script>',
    'data:text/html;base64,PHNjcmlwdD4=',
    'vbscript:msgbox(1)',
    'file:///etc/passwd',
    'file://C:/Windows/System32',
    'blob:https://example.com/1234',
    'about:blank',
    'mailto:someone@example.com',
    'tel:+15550100',
    'ftp://example.com/x',
    'ws://example.com',
    'chrome://settings',
    'intent://scan/#Intent;scheme=zxing;end',
  ];

  for (const value of cases) {
    it(`rejects ${JSON.stringify(value)}`, () => {
      expect(isSafeApplyUrl(value)).toBe(false);
    });
  }
});

describe('isSafeApplyUrl: whitespace and control characters', () => {
  // These matter because `new URL()` tolerates surrounding whitespace and strips
  // tab/newline from inside a scheme, which is the classic way to smuggle
  // `java\nscript:` past a naive check. Rejecting the raw string first is what
  // makes the scheme test trustworthy.
  const cases: Array<[string, string]> = [
    ['leading space', ' javascript:alert(1)'],
    ['leading spaces before https', '  https://example.com'],
    ['trailing space', 'https://example.com '],
    ['newline inside the scheme', 'java\nscript:alert(1)'],
    ['carriage return inside the scheme', 'java\rscript:alert(1)'],
    ['tab inside the scheme', 'java\tscript:alert(1)'],
    ['null byte', 'https://example.com\u0000/x'],
    ['vertical tab', 'https://example.com\u000b/x'],
    ['form feed', 'https://example.com\u000c/x'],
    ['DEL', 'https://example.com\u007f/x'],
    ['C1 control', 'https://example.com\u0085/x'],
    ['non-breaking space', 'https://example.com\u00a0/x'],
    ['U+2028 line separator', 'https://example.com\u2028/x'],
    ['U+2029 paragraph separator', 'https://example.com\u2029/x'],
    ['BOM', 'https://example.com\ufeff/x'],
    ['embedded space in path', 'https://example.com/a b'],
  ];

  for (const [label, value] of cases) {
    it(`rejects ${label}`, () => {
      expect(isSafeApplyUrl(value)).toBe(false);
    });
  }
});

describe('isSafeApplyUrl: relative and malformed values', () => {
  const cases: Array<[string, unknown]> = [
    ['empty string', ''],
    ['scheme-relative //host', '//evil.example.com/apply'],
    ['absolute path', '/careers/apply'],
    ['bare relative path', 'careers/apply'],
    ['bare hostname with no scheme', 'example.com/apply'],
    ['scheme with no authority', 'https://'],
    ['scheme with empty host', 'https:///path'],
    ['not a URL at all', 'this is not a url'],
    ['null', null],
    ['undefined', undefined],
    ['number', 12345],
    ['object', { href: 'https://example.com' }],
    ['array', ['https://example.com']],
    ['boolean', true],
  ];

  for (const [label, value] of cases) {
    it(`rejects ${label}`, () => {
      expect(isSafeApplyUrl(value)).toBe(false);
    });
  }
});

describe('isSafeApplyUrl: credentials in the URL', () => {
  // `https://attacker.example@real-employer.com/` reads as the employer in a
  // truncated status bar, which makes it a phishing primitive.
  it('rejects a username and password', () => {
    expect(isSafeApplyUrl('https://user:secret@example.com/apply')).toBe(false);
  });

  it('rejects a username alone', () => {
    expect(isSafeApplyUrl('https://attacker.example@real-employer.com/')).toBe(false);
  });

  it('rejects an empty username with a password', () => {
    expect(isSafeApplyUrl('https://:secret@example.com/')).toBe(false);
  });
});

describe('isSafeApplyUrl: hosts', () => {
  it('rejects a dotless host in production', () => {
    expect(isSafeApplyUrl('https://intranet/apply')).toBe(false);
  });

  it('rejects localhost in production', () => {
    // Not a usable link for a public visitor, so it must not reach an href even
    // though it is harmless locally.
    expect(isSafeApplyUrl('https://localhost/apply')).toBe(false);
  });

  it('accepts a multi-label host', () => {
    expect(isSafeApplyUrl('https://careers.sub.example.co.uk/apply')).toBe(true);
  });

  it('accepts an explicit port', () => {
    expect(isSafeApplyUrl('https://example.com:8443/apply')).toBe(true);
  });
});

describe('isSafeApplyUrl: length cap', () => {
  const base = 'https://example.com/'; // 20 characters

  it('accepts exactly 2048 characters', () => {
    const value = base + 'a'.repeat(2048 - base.length);
    expect(value).toHaveLength(2048);
    expect(isSafeApplyUrl(value)).toBe(true);
  });

  it('rejects 2049 characters', () => {
    const value = base + 'a'.repeat(2049 - base.length);
    expect(value).toHaveLength(2049);
    expect(isSafeApplyUrl(value)).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// isSafeApplyUrl — acceptance
// ---------------------------------------------------------------------------

describe('isSafeApplyUrl: valid https URLs', () => {
  const cases = [
    'https://example.com',
    'https://example.com/',
    'https://example.com/careers/senior-cloud-engineer',
    'https://example.com/apply?ref=jobpaglu&utm_source=x',
    'https://example.com/apply#form',
    'https://example.com/apply/%20encoded',
    'https://boards.example.com/acme/jobs/1234567890',
  ];

  for (const value of cases) {
    it(`accepts ${JSON.stringify(value)}`, () => {
      expect(isSafeApplyUrl(value)).toBe(true);
    });
  }
});

describe('isSafeApplyUrl: the http and localhost allowances', () => {
  it('rejects http:// when APP_ENV is not development', () => {
    expect(isSafeApplyUrl('http://example.com/apply')).toBe(false);
  });

  it('accepts http:// only when APP_ENV is development', () => {
    vi.stubEnv('APP_ENV', 'development');
    expect(isSafeApplyUrl('http://example.com/apply')).toBe(true);
  });

  it('accepts http://localhost in development', () => {
    vi.stubEnv('APP_ENV', 'development');
    expect(isSafeApplyUrl('http://localhost:3000/apply')).toBe(true);
  });

  it('still rejects a dangerous scheme in development', () => {
    // The dev relaxation must widen the scheme allowlist by exactly one entry.
    vi.stubEnv('APP_ENV', 'development');
    expect(isSafeApplyUrl('javascript:alert(1)')).toBe(false);
    expect(isSafeApplyUrl('data:text/html,x')).toBe(false);
  });

  it('still rejects credentials in development', () => {
    vi.stubEnv('APP_ENV', 'development');
    expect(isSafeApplyUrl('http://user:pass@localhost/apply')).toBe(false);
  });

  it('rejects a dotless non-localhost host even in development', () => {
    vi.stubEnv('APP_ENV', 'development');
    expect(isSafeApplyUrl('http://intranet/apply')).toBe(false);
  });
});

// ---------------------------------------------------------------------------
// applyUrlHost
// ---------------------------------------------------------------------------

describe('applyUrlHost', () => {
  it('returns the hostname', () => {
    expect(applyUrlHost('https://boards.example.com/x')).toBe('boards.example.com');
  });

  it('strips a leading www.', () => {
    expect(applyUrlHost('https://www.example.com/x')).toBe('example.com');
  });

  it('strips the port', () => {
    expect(applyUrlHost('https://example.com:8443/x')).toBe('example.com');
  });

  it('returns null rather than throwing on a malformed value', () => {
    expect(applyUrlHost('not a url')).toBeNull();
  });
});

// ---------------------------------------------------------------------------
// URL emission helpers
// ---------------------------------------------------------------------------

describe('siteOrigin', () => {
  it('prefers the supplied Astro.site', () => {
    expect(siteOrigin(new URL('https://preview.example.com/base/'))).toBe('https://preview.example.com');
  });

  it('falls back to SITE_URL when no site is given', () => {
    expect(siteOrigin(undefined)).toBe('https://jobpaglu.com');
  });

  it('does not blank out the origin when SITE_URL is malformed', () => {
    // A wrong-but-absolute canonical is recoverable; an empty one silently
    // breaks Open Graph and the sitemap.
    vi.stubEnv('SITE_URL', 'not-a-url');
    expect(siteOrigin(undefined)).toBe('https://jobpaglu.com');
  });
});

describe('absoluteUrl', () => {
  const site = new URL('https://jobpaglu.com');

  it('joins a path to the origin', () => {
    expect(absoluteUrl('/jobs', site)).toBe('https://jobpaglu.com/jobs');
  });

  it('adds a missing leading slash', () => {
    expect(absoluteUrl('jobs', site)).toBe('https://jobpaglu.com/jobs');
  });

  it('strips a trailing slash, because trailingSlash is "never"', () => {
    expect(absoluteUrl('/jobs/', site)).toBe('https://jobpaglu.com/jobs');
  });

  it('keeps the root slash', () => {
    expect(absoluteUrl('/', site)).toBe('https://jobpaglu.com/');
  });

  it('preserves a query string', () => {
    expect(absoluteUrl('/jobs?page=2', site)).toBe('https://jobpaglu.com/jobs?page=2');
  });
});

describe('canonicalUrl', () => {
  const site = new URL('https://jobpaglu.com');

  it('drops the query so tracking parameters cannot mint canonicals', () => {
    const url = new URL('https://jobpaglu.com/jobs?utm_source=newsletter&page=3');
    expect(canonicalUrl(url, site)).toBe('https://jobpaglu.com/jobs');
  });
});

describe('jobPath and jobListPath', () => {
  it('builds a job detail path', () => {
    expect(jobPath('senior-cloud-engineer')).toBe('/jobs/senior-cloud-engineer');
  });

  it('encodes an unexpected slug rather than emitting it raw', () => {
    expect(jobPath('a b/c')).toBe('/jobs/a%20b%2Fc');
  });

  it('gives page 1 no query, so it has exactly one canonical', () => {
    expect(jobListPath(1)).toBe(ROUTES.jobs);
  });

  it('adds a page query beyond page 1', () => {
    expect(jobListPath(4)).toBe('/jobs?page=4');
  });
});

describe('isCurrentPath', () => {
  it('keeps a section link current on its children', () => {
    expect(isCurrentPath('/jobs/senior-cloud-engineer', '/jobs')).toBe(true);
  });

  it('matches the home link exactly, so it is not current everywhere', () => {
    expect(isCurrentPath('/jobs', '/')).toBe(false);
    expect(isCurrentPath('/', '/')).toBe(true);
  });

  it('does not treat a prefix match as a child', () => {
    // `/jobsearch` must not light up the `/jobs` nav item.
    expect(isCurrentPath('/jobsearch', '/jobs')).toBe(false);
  });
});

describe('readPageParam', () => {
  const at = (query: string) => new URL(`https://jobpaglu.com/jobs${query}`);

  it('defaults to 1 when absent', () => {
    expect(readPageParam(at(''))).toBe(1);
  });

  it('parses a valid page', () => {
    expect(readPageParam(at('?page=7'))).toBe(7);
  });

  it('clamps zero and negatives to 1', () => {
    expect(readPageParam(at('?page=0'))).toBe(1);
    expect(readPageParam(at('?page=-5'))).toBe(1);
  });

  it('falls back to 1 on non-numeric input', () => {
    expect(readPageParam(at('?page=abc'))).toBe(1);
    expect(readPageParam(at('?page='))).toBe(1);
  });

  it('caps an absurd page number', () => {
    // Without a cap, `?page=99999999` becomes a cheap way to make the API do a
    // very large OFFSET scan.
    expect(readPageParam(at('?page=99999999'))).toBe(10_000);
  });
});
