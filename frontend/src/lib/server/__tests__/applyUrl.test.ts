/**
 * The apply-URL allowlist — contract section 7.
 *
 * This is the highest-value test file in the server layer: a bad value in this
 * one field becomes an attack on a visitor who clicks "Apply Now". The cases are
 * transcribed from backend/internal/validation/applyurl_test.go so a regression
 * against the Go behaviour shows up as a failing assertion rather than as a live
 * `javascript:` link.
 */

import { describe, expect, it } from 'vitest';
import { APPLY_URL_MESSAGES, MAX_APPLY_URL_LENGTH, validateApplyUrl } from '../applyUrl';
import type { ApplyUrlPolicy } from '../env';

const strict: ApplyUrlPolicy = { allowHttp: false, allowLocalhost: false };
const httpOk: ApplyUrlPolicy = { allowHttp: true, allowLocalhost: false };
const developing: ApplyUrlPolicy = { allowHttp: true, allowLocalhost: true };

describe('validateApplyUrl — rejections', () => {
  const rejected: ReadonlyArray<[string, string, ApplyUrlPolicy, string]> = [
    // ------------------------------------------------- dangerous schemes
    ['javascript', 'javascript:alert(1)', strict, APPLY_URL_MESSAGES.scheme],
    ['javascript mixed case', 'JavaScript:alert(1)', strict, APPLY_URL_MESSAGES.scheme],
    ['javascript upper case', 'JAVASCRIPT:alert(1)', strict, APPLY_URL_MESSAGES.scheme],
    ['data html', 'data:text/html,<script>alert(1)</script>', strict, APPLY_URL_MESSAGES.scheme],
    ['data base64', 'data:text/html;base64,PHNjcmlwdD4=', strict, APPLY_URL_MESSAGES.scheme],
    ['vbscript', 'vbscript:msgbox(1)', strict, APPLY_URL_MESSAGES.scheme],
    ['file', 'file:///etc/passwd', strict, APPLY_URL_MESSAGES.scheme],
    ['blob', 'blob:https://example.com/1234-5678', strict, APPLY_URL_MESSAGES.scheme],
    ['about', 'about:blank', strict, APPLY_URL_MESSAGES.scheme],
    ['mailto', 'mailto:jobs@example.com', strict, APPLY_URL_MESSAGES.scheme],
    ['tel', 'tel:+15551234567', strict, APPLY_URL_MESSAGES.scheme],
    ['ftp', 'ftp://example.com/jobs', strict, APPLY_URL_MESSAGES.scheme],

    // ------------------------------------------------------ not absolute
    ['scheme relative', '//evil.com/apply', strict, APPLY_URL_MESSAGES.scheme],
    ['root relative', '/relative/path', strict, APPLY_URL_MESSAGES.scheme],
    ['bare host', 'example.com/jobs', strict, APPLY_URL_MESSAGES.scheme],
    ['https with empty host', 'https:///jobs', strict, APPLY_URL_MESSAGES.host],

    // ---------------------------------------------------------- userinfo
    ['user and password', 'https://user:pass@host.example/x', strict, APPLY_URL_MESSAGES.userinfo],
    ['user only', 'https://user@host.example/x', strict, APPLY_URL_MESSAGES.userinfo],

    // ----------------------------------------- whitespace and controls
    ['embedded newline', 'https://example.com/a\nb', strict, APPLY_URL_MESSAGES.whitespace],
    ['embedded carriage return', 'https://example.com/a\rb', strict, APPLY_URL_MESSAGES.whitespace],
    ['embedded tab', 'https://example.com/a\tb', strict, APPLY_URL_MESSAGES.whitespace],
    ['leading space before scheme', '  javascript:alert(1)', strict, APPLY_URL_MESSAGES.whitespace],
    ['internal space', 'https://example.com/a b', strict, APPLY_URL_MESSAGES.whitespace],
    // Control characters and exotic separators have their own test below, built
    // programmatically rather than as literals.

    // ------------------------------------------------------------ empty
    ['empty', '', strict, APPLY_URL_MESSAGES.required],
    ['whitespace only', '   ', strict, APPLY_URL_MESSAGES.required],

    // ------------------------------------------------- the http switch
    ['http rejected by default', 'http://x.com', strict, APPLY_URL_MESSAGES.scheme],

    // ----------------------------------------------------- bad hosts
    ['leading dot host', 'https://.example.com/x', strict, APPLY_URL_MESSAGES.host],
    ['trailing dot host', 'https://example.com./x', strict, APPLY_URL_MESSAGES.host],
    ['double dot host', 'https://example..com/x', strict, APPLY_URL_MESSAGES.host],
    ['localhost rejected in production', 'https://localhost/x', strict, APPLY_URL_MESSAGES.host],
    ['dotless intranet host', 'https://intranet/apply', developing, APPLY_URL_MESSAGES.host],
  ];

  for (const [name, raw, policy, message] of rejected) {
    it(`rejects ${name}`, () => {
      expect(validateApplyUrl(raw, policy)).toBe(message);
    });
  }

  /**
   * Control characters and the exotic separators, built with fromCodePoint rather
   * than written as literals: a raw NUL or U+2028 in a source file is invisible
   * in a diff and is silently rewritten by editors, linters and encoding
   * conversions. These are exactly the characters used to smuggle a
   * `java\nscript:` value past a naive scheme check, so the assertion has to
   * survive a round trip through tooling.
   */
  it('rejects control characters and exotic separators inside the URL', () => {
    const codePoints = [
      0x00, // NUL
      0x0b, // vertical tab
      0x0c, // form feed
      0x1f, // unit separator
      0x7f, // DEL
      0x85, // C1 NEL
      0xa0, // non-breaking space
      0x2028, // line separator
      0x2029, // paragraph separator
      0xfeff, // byte order mark
    ];
    for (const codePoint of codePoints) {
      const raw = `https://example.com/a${String.fromCodePoint(codePoint)}b`;
      expect(validateApplyUrl(raw, strict)).toBe(APPLY_URL_MESSAGES.whitespace);
    }
  });

  it('rejects a value over the length cap', () => {
    const raw = `https://example.com/${'a'.repeat(MAX_APPLY_URL_LENGTH)}`;
    expect(validateApplyUrl(raw, strict)).toBe(APPLY_URL_MESSAGES.tooLong);
  });

  /**
   * The scheme check must be an ALLOWLIST. A denylist of known-bad schemes is
   * always one exotic scheme behind, and this is the assertion that catches
   * someone "simplifying" it into one.
   */
  it('rejects every unlisted scheme, not just the famous ones', () => {
    const schemes = [
      'chrome', 'ms-msdt', 'steam', 'intent', 'jar',
      'view-source', 'ws', 'wss', 'gopher', 'x-made-up',
    ];
    for (const scheme of schemes) {
      expect(validateApplyUrl(`${scheme}://example.com/apply`, developing)).not.toBeNull();
    }
  });
});

describe('validateApplyUrl — acceptances', () => {
  const accepted: ReadonlyArray<[string, string, ApplyUrlPolicy]> = [
    ['plain https', 'https://x.com', strict],
    ['path and query', 'https://boards.example.com/careers/apply?id=42&src=jobpaglu', strict],
    ['explicit port', 'https://jobs.example.com:8443/apply', strict],
    ['fragment', 'https://example.com/careers#apply', strict],
    ['punycode host', 'https://xn--bcher-kva.example/stelle', strict],
    ['percent-encoded path', 'https://example.com/apply/senior%20engineer', strict],
    ['http when the flag is on', 'http://x.com', httpOk],
    ['localhost in development', 'https://localhost/x', developing],
    ['localhost with port in development', 'http://localhost:3000/apply', developing],
  ];

  for (const [name, raw, policy] of accepted) {
    it(`accepts ${name}`, () => {
      expect(validateApplyUrl(raw, policy)).toBeNull();
    });
  }

  it('accepts a value exactly at the length cap', () => {
    const prefix = 'https://example.com/';
    const raw = prefix + 'a'.repeat(MAX_APPLY_URL_LENGTH - prefix.length);
    expect(raw).toHaveLength(MAX_APPLY_URL_LENGTH);
    expect(validateApplyUrl(raw, strict)).toBeNull();
  });

  /**
   * The two switches are independent (contract section 7). `http://localhost`
   * is two departures from the production rule and needs both.
   */
  it('needs both switches for http://localhost', () => {
    expect(validateApplyUrl('http://localhost/x', httpOk)).toBe(APPLY_URL_MESSAGES.host);
    expect(validateApplyUrl('https://localhost/x', { allowHttp: false, allowLocalhost: true })).toBeNull();
    expect(validateApplyUrl('http://localhost/x', developing)).toBeNull();
  });
});
