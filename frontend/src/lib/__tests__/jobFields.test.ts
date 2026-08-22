/**
 * The two field-level rules the admin form enforces before the server does:
 * slug derivation (contract section 8) and the apply-URL allowlist (section 7).
 *
 * Split out of `adminApi.test.ts`, where these lived only by accident. They test
 * `components/admin/jobFields.ts` and have nothing to do with the fetch layer,
 * which was rewritten around them.
 *
 * Relative imports on purpose — see the note in `adminApi.test.ts`.
 */
import { describe, expect, it, vi } from 'vitest';
import {
  deriveSlug,
  hasUnsafeUrlChars,
  isValidSlug,
  validateApplyUrl,
} from '../../components/admin/jobFields';

describe('deriveSlug', () => {
  it('follows the contract section 8 steps', () => {
    expect(deriveSlug('Senior Cloud Engineer')).toBe('senior-cloud-engineer');
    expect(deriveSlug('  Señor  Développeur!!  ')).toBe('senor-developpeur');
    expect(deriveSlug('C++ / Rust & Go')).toBe('c-rust-go');
  });

  it('returns empty when nothing survives, leaving the server to name it', () => {
    expect(deriveSlug('求人')).toBe('');
    expect(deriveSlug('---')).toBe('');
  });

  it('suffixes a reserved word', () => {
    expect(deriveSlug('Admin')).toBe('admin-job');
    expect(deriveSlug('robots')).toBe('robots-job');
  });

  it('truncates to 80 characters on a hyphen boundary', () => {
    const slug = deriveSlug(`${'word '.repeat(30)}tail`);
    expect(slug.length).toBeLessThanOrEqual(80);
    expect(slug.endsWith('-')).toBe(false);
    expect(slug.split('-').every((part) => part === 'word')).toBe(true);
  });

  it('agrees with the slug pattern for realistic titles', () => {
    expect(isValidSlug(deriveSlug('Staff Engineer, Platform (Remote)'))).toBe(true);
  });
});

describe('validateApplyUrl', () => {
  it('accepts an absolute https URL', () => {
    expect(validateApplyUrl('https://example.com/careers/sce')).toBeNull();
    expect(validateApplyUrl('https://sub.example.co.uk/a?b=c#d')).toBeNull();
  });

  const rejections: ReadonlyArray<[string, string]> = [
    ['empty', ''],
    ['relative', 'example.com/apply'],
    ['http in production', 'http://example.com/apply'],
    ['javascript', 'javascript:alert(1)'],
    ['data', 'data:text/html,<script>x</script>'],
    ['vbscript', 'vbscript:msgbox(1)'],
    ['file', 'file:///etc/passwd'],
    ['blob', 'blob:https://example.com/x'],
    ['about', 'about:blank'],
    ['mailto', 'mailto:jobs@example.com'],
    ['userinfo', 'https://user:pass@example.com/apply'],
    ['no dot in host', 'https://intranet/apply'],
    ['too long', `https://example.com/${'a'.repeat(2100)}`],
  ];

  for (const [name, value] of rejections) {
    it(`rejects ${name}`, () => {
      expect(validateApplyUrl(value)).not.toBeNull();
    });
  }

  it('rejects whitespace and control characters before parsing', () => {
    expect(hasUnsafeUrlChars('https://example.com/a b')).toBe(true);
    expect(hasUnsafeUrlChars('https://example.com/a\tb')).toBe(true);
    expect(hasUnsafeUrlChars('https://example.com/a\nb')).toBe(true);
    expect(hasUnsafeUrlChars('https://example.com/ab')).toBe(false);
    // The URL parser strips a tab silently, so the check must run first.
    expect(validateApplyUrl('https://exam\tple.com/apply')).not.toBeNull();
  });

  it('accepts http and localhost only in development', () => {
    vi.stubEnv('APP_ENV', 'development');
    expect(validateApplyUrl('http://localhost:8080/apply')).toBeNull();
    vi.stubEnv('APP_ENV', 'production');
    expect(validateApplyUrl('http://localhost:8080/apply')).not.toBeNull();
    vi.unstubAllEnvs();
  });
});
