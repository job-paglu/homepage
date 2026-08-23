/**
 * Unit tests for the webhook helpers.
 *
 * These cover the two pieces where a mistake is a security or correctness bug:
 * the constant-time bearer-key compare, and the single/batch body shaping with
 * its 50-item cap. The D1 upsert itself cannot be exercised here — it needs a
 * real database — so it is verified against a running deployment instead.
 */

import { describe, expect, it } from 'vitest';
import { ApiError } from '../respond';
import {
  MAX_INGEST_BATCH,
  bearerToken,
  constantTimeEqual,
  ingestionDisabled,
  parseWebhookBody,
} from '../webhook';

describe('constantTimeEqual', () => {
  it('is true for two identical keys', async () => {
    await expect(constantTimeEqual('s3cret-key-value', 's3cret-key-value')).resolves.toBe(true);
    // Works for the empty string too (both sides empty).
    await expect(constantTimeEqual('', '')).resolves.toBe(true);
  });

  it('is false for two different keys of the same length', async () => {
    await expect(constantTimeEqual('aaaaaaaaaaaaaaaa', 'aaaaaaaaaaaaaaab')).resolves.toBe(false);
  });

  it('is false — and does not throw — for keys of different lengths', async () => {
    // The whole point of hashing before comparing: a length mismatch must not
    // short-circuit, throw, or otherwise leak the length. It just returns false.
    await expect(constantTimeEqual('short', 'a-much-longer-configured-key')).resolves.toBe(false);
    await expect(constantTimeEqual('', 'non-empty')).resolves.toBe(false);
    await expect(constantTimeEqual('non-empty', '')).resolves.toBe(false);
  });

  it('is false when a key is a prefix of the other', async () => {
    // A prefix would beat a naive byte-by-byte compare that stops at the shorter
    // length; hashing removes any prefix relationship.
    await expect(constantTimeEqual('secret', 'secret-extra')).resolves.toBe(false);
  });

  it('distinguishes keys differing only in the last byte', async () => {
    const base = 'k'.repeat(63);
    await expect(constantTimeEqual(`${base}A`, `${base}B`)).resolves.toBe(false);
    await expect(constantTimeEqual(`${base}A`, `${base}A`)).resolves.toBe(true);
  });
});

describe('bearerToken', () => {
  const withAuth = (value: string): Request =>
    new Request('https://example.com/api/webhook/jobs', {
      method: 'POST',
      headers: { Authorization: value },
    });

  it('extracts the token after "Bearer "', () => {
    expect(bearerToken(withAuth('Bearer abc123'))).toBe('abc123');
    // Surrounding whitespace on the token is trimmed.
    expect(bearerToken(withAuth('Bearer   abc123  '))).toBe('abc123');
  });

  it('returns "" for a missing header', () => {
    const request = new Request('https://example.com/api/webhook/jobs', { method: 'POST' });
    expect(bearerToken(request)).toBe('');
  });

  it('returns "" for a non-Bearer scheme or empty token', () => {
    expect(bearerToken(withAuth('Basic abc123'))).toBe('');
    expect(bearerToken(withAuth('Bearer '))).toBe('');
    expect(bearerToken(withAuth('abc123'))).toBe('');
    // Case matters: the scheme keyword is exactly "Bearer ".
    expect(bearerToken(withAuth('bearer abc123'))).toBe('');
  });
});

describe('parseWebhookBody', () => {
  const job = { title: 'Engineer' };

  it('wraps a single job object into a one-item batch', () => {
    expect(parseWebhookBody(job)).toEqual([job]);
  });

  it('returns the jobs array for a batch', () => {
    const jobs = [job, { title: 'Designer' }];
    expect(parseWebhookBody({ jobs })).toBe(jobs);
  });

  it('accepts a batch of exactly the cap', () => {
    const jobs = Array.from({ length: MAX_INGEST_BATCH }, (_unused, i) => ({ title: `Job ${i}` }));
    expect(parseWebhookBody({ jobs })).toHaveLength(MAX_INGEST_BATCH);
  });

  it('rejects a batch over the cap with VALIDATION_FAILED', () => {
    const jobs = Array.from({ length: MAX_INGEST_BATCH + 1 }, () => job);
    try {
      parseWebhookBody({ jobs });
      expect.unreachable('expected parseWebhookBody to throw');
    } catch (err) {
      expect(err).toBeInstanceOf(ApiError);
      expect((err as ApiError).code).toBe('VALIDATION_FAILED');
      expect((err as ApiError).status).toBe(422);
    }
  });

  it('rejects a non-array "jobs" with VALIDATION_FAILED', () => {
    for (const bad of [{ jobs: 'nope' }, { jobs: 42 }, { jobs: { '0': job } }, { jobs: null }]) {
      try {
        parseWebhookBody(bad as Record<string, unknown>);
        expect.unreachable('expected parseWebhookBody to throw');
      } catch (err) {
        expect(err).toBeInstanceOf(ApiError);
        expect((err as ApiError).code).toBe('VALIDATION_FAILED');
      }
    }
  });

  it('allows an empty batch (zero items) — the loop simply does nothing', () => {
    expect(parseWebhookBody({ jobs: [] })).toEqual([]);
  });

  it('treats an object that happens to lack "jobs" as a single item, even if odd', () => {
    // A junk single object is NOT rejected here; the service validates each item
    // and turns a bad one into a per-item failure. parseWebhookBody only shapes.
    expect(parseWebhookBody({ anything: true })).toEqual([{ anything: true }]);
  });
});

describe('ingestionDisabled', () => {
  it('is a 503 SERVICE_UNAVAILABLE with an actionable message', () => {
    const err = ingestionDisabled();
    expect(err).toBeInstanceOf(ApiError);
    expect(err.code).toBe('SERVICE_UNAVAILABLE');
    expect(err.status).toBe(503);
    expect(err.message).toMatch(/not configured/i);
  });
});
