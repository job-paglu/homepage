/**
 * Pure helpers for the webhook ingestion route (`POST /api/webhook/jobs`):
 * the constant-time bearer-key comparison and the single/batch body shaping.
 *
 * They live here, apart from the route, for one reason: they are the security-
 * and correctness-critical logic, and they must be unit-testable without an HTTP
 * server, a D1 binding, or the Cloudflare runtime. The route stays a thin shell
 * that wires these to the request. See `__tests__/webhook.test.ts`.
 */

import { ApiError, errors } from './respond';

/** A batch may carry at most this many jobs; more is a 422 (see the route). */
export const MAX_INGEST_BATCH = 50;

/**
 * Max ingestion body, larger than the 64 KiB admin cap because a legitimate
 * batch of up to 50 jobs is bigger than one admin form submission. Passed to
 * `readJsonBody` so the parse/type/empty rules stay shared; only the cap differs.
 */
export const MAX_WEBHOOK_BODY_BYTES = 256 * 1024;

/**
 * Compare a presented key to the configured one WITHOUT leaking, through timing,
 * either the key or even its length.
 *
 * Method: hash both sides with SHA-256 and compare the two 32-byte digests with
 * an XOR-accumulator. Two properties make this safe where `a === b` would not:
 *
 *   * The comparison always runs over 32 bytes regardless of the inputs' lengths
 *     — a SHA-256 digest is fixed-width — so a wrong-length guess takes the same
 *     time as a right-length one. `===` on the raw strings can short-circuit on
 *     the first differing byte AND on a length mismatch, both of which are timing
 *     oracles.
 *   * We compare DIGESTS, not the raw strings, so even the per-byte compare below
 *     reveals nothing about the real key: a digest byte is not a key byte.
 *
 * `crypto.subtle.digest` is available in workerd, Node 18+ and the test runner,
 * so no polyfill is needed. The function is async only because WebCrypto is.
 */
export async function constantTimeEqual(presented: string, configured: string): Promise<boolean> {
  const encoder = new TextEncoder();
  const [presentedDigest, configuredDigest] = await Promise.all([
    crypto.subtle.digest('SHA-256', encoder.encode(presented)),
    crypto.subtle.digest('SHA-256', encoder.encode(configured)),
  ]);

  const a = new Uint8Array(presentedDigest);
  const b = new Uint8Array(configuredDigest);

  // Both are 32 bytes, so this loop count does not depend on the inputs. The
  // `?? 0` satisfies noUncheckedIndexedAccess; the indices are always in range.
  let diff = 0;
  for (let i = 0; i < a.length; i += 1) {
    diff |= (a[i] ?? 0) ^ (b[i] ?? 0);
  }
  return diff === 0;
}

/**
 * Extract the presented bearer key from an `Authorization: Bearer <key>` header.
 *
 * Returns `''` for a missing, non-Bearer, or empty header. Returning a value
 * rather than short-circuiting matters: the caller still runs the constant-time
 * compare on `''`, so "no header" and "wrong key" take the same time and yield
 * the same 401 — there is no oracle telling an attacker whether a header was even
 * present.
 */
export function bearerToken(request: Request): string {
  const header = request.headers.get('Authorization');
  if (header === null) return '';
  const prefix = 'Bearer ';
  if (!header.startsWith(prefix)) return '';
  return header.slice(prefix.length).trim();
}

/**
 * Turn a parsed JSON object into the list of items to ingest.
 *
 *   * `{ "jobs": [ ... ] }`  -> the array, rejected as 422 if it is not an array
 *     or exceeds `MAX_INGEST_BATCH`.
 *   * any other object       -> a single-item batch `[ body ]`.
 *
 * The top-level value is already guaranteed to be a non-array object by
 * `readJsonBody`, so a bare array or a scalar has been rejected as 400 before we
 * get here. Individual items are returned as `unknown`: the service validates
 * each one and turns a non-object into a per-item failure, so a single junk
 * element never fails the whole batch.
 */
export function parseWebhookBody(body: Record<string, unknown>): unknown[] {
  if ('jobs' in body) {
    const jobs = body['jobs'];
    if (!Array.isArray(jobs)) {
      throw errors.validationFailed({ jobs: 'Must be an array of job objects.' });
    }
    if (jobs.length > MAX_INGEST_BATCH) {
      throw errors.validationFailed({
        jobs: `A batch may contain at most ${MAX_INGEST_BATCH} jobs; received ${jobs.length}.`,
      });
    }
    return jobs;
  }
  return [body];
}

/**
 * Build the 503 for an unconfigured endpoint. A dedicated constructor (rather
 * than `errors.serviceUnavailable`, whose message is a generic "try again
 * shortly") so the caller is told the specific, actionable truth: ingestion is
 * switched off because no key is set. This is the fail-closed path — a missing
 * `WEBHOOK_API_KEY` must NEVER leave the endpoint open.
 */
export function ingestionDisabled(): ApiError {
  return new ApiError(
    'SERVICE_UNAVAILABLE',
    'Webhook ingestion is not configured on this deployment.',
  );
}
