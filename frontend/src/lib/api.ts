/**
 * The BROWSER-side fetch core: one place that turns a same-origin `/api/...`
 * call into a parsed envelope or an `ApiError`.
 *
 * Who uses this
 * -------------
 * Only code that runs in a browser — i.e. the admin islands under
 * `src/components/admin/`. Public pages do **not** use it. They render inside the
 * same Worker as the API and read D1 directly through `src/lib/publicJobs.ts`;
 * see the header of that file for why a Worker must not fetch itself.
 *
 * There is no base URL, and that is the point
 * -------------------------------------------
 * Site and API are one Cloudflare Pages project, so every path here is
 * *relative*: `/api/admin/jobs`, never `https://something/api/admin/jobs`.
 * Consequences worth stating, because they are the reason the old
 * `PUBLIC_API_BASE_URL` is gone:
 *
 *   - nothing to configure, so nothing to misconfigure — this client cannot be
 *     pointed at the wrong backend, in any environment, ever;
 *   - the request is same-origin, so CORS never engages and the browser attaches
 *     the `CF_Authorization` cookie Cloudflare Access set, without
 *     `credentials: 'include'` theatre;
 *   - the browser sets `Origin` on every state-changing same-origin request,
 *     which is what satisfies Astro's `security.checkOrigin` (see `adminApi.ts`).
 *
 * `assertRelativePath` enforces the first bullet at runtime rather than trusting
 * every future caller to remember it.
 *
 * Contract: docs/API-CONTRACT.md
 */

import type { ApiErrorBody, ApiErrorCode } from './types';

// -------------------------------------------------------------------- errors

/**
 * A failed API call, in a shape the UI can branch on.
 *
 * `code` is always populated: a genuine transport failure becomes
 * `NETWORK_ERROR` rather than an exception of a different class, so a caller
 * only ever has to handle one error type.
 */
export class ApiError extends Error {
  readonly code: ApiErrorCode;
  readonly status: number;
  readonly details: Record<string, string>;
  readonly requestId: string | undefined;
  readonly retryAfterSeconds: number | undefined;

  constructor(init: {
    code: ApiErrorCode;
    message: string;
    status: number;
    details?: Record<string, string>;
    requestId?: string;
    retryAfterSeconds?: number;
  }) {
    super(init.message);
    this.name = 'ApiError';
    this.code = init.code;
    this.status = init.status;
    this.details = init.details ?? {};
    this.requestId = init.requestId;
    this.retryAfterSeconds = init.retryAfterSeconds;
  }

  /** True when the failure is worth offering a "Try again" button for. */
  get isRetryable(): boolean {
    return (
      this.code === 'NETWORK_ERROR' ||
      this.code === 'SERVICE_UNAVAILABLE' ||
      this.code === 'INTERNAL_ERROR' ||
      this.status === 0 ||
      this.status >= 500
    );
  }

  get isNotFound(): boolean {
    return this.code === 'JOB_NOT_FOUND' || this.code === 'NOT_FOUND' || this.status === 404;
  }

  /**
   * "Cloudflare Access did not authenticate this browser."
   *
   * There is no password, no login form and no session of ours, so this is not
   * "your session expired" — it is "you are not through the Access gate". The
   * UI copy that goes with it lives in `adminIdentity.ts`.
   */
  get isUnauthorized(): boolean {
    return this.code === 'UNAUTHORIZED' || this.status === 401;
  }
}

// ------------------------------------------------------------------ requests

export interface ApiFetchOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE';
  /** Serialised as JSON. Omit for GET/DELETE. */
  body?: unknown;
  /** Abort after this many ms. Default 10000. */
  timeoutMs?: number;
  /** Extra headers, merged last. */
  headers?: Record<string, string>;
  /** Forwarded to fetch. Admin reads pass `no-store`. */
  cache?: RequestCache;
  signal?: AbortSignal;
}

/**
 * Default fetch budget, in ms.
 *
 * A flat 10s, with no nested ladder of timeouts behind it. The previous value
 * was tuned to sit under a Vercel function's 10s `maxDuration` and under a
 * cross-origin proxy hop; both are gone. A Cloudflare Worker is limited by CPU
 * time, not wall-clock, and there is no proxy — so the only thing this number
 * has to be is "longer than a healthy D1 query and shorter than a visitor's
 * patience".
 */
const DEFAULT_TIMEOUT_MS = 10_000;

/**
 * Reject anything that is not a site-relative path.
 *
 * `//evil.example/x` is a protocol-relative URL, not a path, and `fetch` would
 * happily send the admin's cookies to it. A scheme is rejected for the same
 * reason. This is cheap and it makes "there is no base URL" a checked property
 * rather than a convention.
 */
function assertRelativePath(path: string): string {
  if (path.startsWith('/') && !path.startsWith('//') && !path.includes('://')) return path;
  throw new ApiError({
    code: 'INTERNAL_ERROR',
    status: 0,
    message: 'The request could not be completed.',
  });
}

/**
 * Perform an API call and return the parsed JSON body.
 *
 * Throws `ApiError` for any non-2xx response, any transport failure, and any
 * response that is not the JSON envelope the contract promises. Never returns
 * a partially-valid value.
 */
export async function apiFetch<T>(path: string, options: ApiFetchOptions = {}): Promise<T> {
  const {
    method = 'GET',
    body,
    timeoutMs = DEFAULT_TIMEOUT_MS,
    headers = {},
    cache,
    signal,
  } = options;

  const url = assertRelativePath(path);

  // Own timeout, linked to any caller-supplied signal. `AbortSignal.any` is not
  // available in every browser this has to work in, so the two are bridged
  // manually.
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(new Error('timeout')), timeoutMs);
  const onCallerAbort = () => controller.abort(signal?.reason);
  if (signal) {
    if (signal.aborted) controller.abort(signal.reason);
    else signal.addEventListener('abort', onCallerAbort, { once: true });
  }

  const requestHeaders: Record<string, string> = { Accept: 'application/json', ...headers };
  if (body !== undefined) requestHeaders['Content-Type'] = 'application/json';

  let response: Response;
  try {
    response = await fetch(url, {
      method,
      headers: requestHeaders,
      body: body === undefined ? undefined : JSON.stringify(body),
      // 'same-origin' is the fetch default; stating it makes the intent legible.
      // It is what carries Cloudflare Access's `CF_Authorization` cookie. There
      // is deliberately no 'include' anywhere: an admin credential must never
      // leave this origin.
      credentials: 'same-origin',
      signal: controller.signal,
      ...(cache ? { cache } : {}),
    });
  } catch {
    const timedOut = controller.signal.aborted;
    throw new ApiError({
      code: 'NETWORK_ERROR',
      status: 0,
      message: timedOut
        ? 'The request took too long. Please check your connection and try again.'
        : 'Could not reach the Job Paglu service. Please try again.',
      details: {},
    });
  } finally {
    clearTimeout(timer);
    if (signal) signal.removeEventListener('abort', onCallerAbort);
  }

  // 204 and 205 carry no body by contract.
  if (response.status === 204 || response.status === 205) {
    return undefined as T;
  }

  const rawText = await response.text();
  let parsed: unknown = undefined;
  if (rawText.length > 0) {
    try {
      parsed = JSON.parse(rawText);
    } catch {
      parsed = undefined;
    }
  }

  if (!response.ok) {
    throw toApiError(response, parsed);
  }

  if (parsed === undefined || typeof parsed !== 'object' || parsed === null || !('data' in parsed)) {
    // A 2xx that is not the documented envelope is a server bug, but the
    // visitor should still see a sane message rather than a blank page.
    throw new ApiError({
      code: 'INTERNAL_ERROR',
      status: response.status,
      message: 'The service returned an unexpected response.',
      requestId: response.headers.get('X-Request-Id') ?? undefined,
    });
  }

  return parsed as T;
}

function toApiError(response: Response, parsed: unknown): ApiError {
  const requestId =
    response.headers.get('X-Request-Id') ??
    (isErrorBody(parsed) ? parsed.error.request_id : undefined) ??
    undefined;

  const retryAfterHeader = response.headers.get('Retry-After');
  const retryAfterSeconds =
    retryAfterHeader && /^\d+$/.test(retryAfterHeader) ? Number(retryAfterHeader) : undefined;

  if (isErrorBody(parsed)) {
    return new ApiError({
      code: parsed.error.code,
      message: parsed.error.message || fallbackMessage(response.status),
      status: response.status,
      details: parsed.error.details ?? {},
      requestId,
      retryAfterSeconds,
    });
  }

  return new ApiError({
    code: codeForStatus(response.status),
    message: fallbackMessage(response.status),
    status: response.status,
    requestId,
    retryAfterSeconds,
  });
}

function isErrorBody(value: unknown): value is ApiErrorBody {
  return (
    typeof value === 'object' &&
    value !== null &&
    'error' in value &&
    typeof (value as ApiErrorBody).error === 'object' &&
    (value as ApiErrorBody).error !== null &&
    typeof (value as ApiErrorBody).error.code === 'string'
  );
}

function codeForStatus(status: number): ApiErrorCode {
  switch (status) {
    case 400:
      return 'BAD_REQUEST';
    case 401:
      return 'UNAUTHORIZED';
    case 403:
      return 'FORBIDDEN';
    case 404:
      return 'NOT_FOUND';
    case 405:
      return 'METHOD_NOT_ALLOWED';
    case 409:
      return 'SLUG_CONFLICT';
    case 413:
      return 'PAYLOAD_TOO_LARGE';
    case 422:
      return 'VALIDATION_FAILED';
    case 429:
      return 'RATE_LIMITED';
    case 503:
      return 'SERVICE_UNAVAILABLE';
    default:
      return status >= 500 ? 'INTERNAL_ERROR' : 'BAD_REQUEST';
  }
}

function fallbackMessage(status: number): string {
  if (status === 404) return 'That page could not be found.';
  if (status === 401 || status === 403) return 'You are not signed in.';
  if (status === 429) return 'Too many attempts. Please wait a moment and try again.';
  if (status >= 500) return 'The service is having trouble right now. Please try again shortly.';
  return 'The request could not be completed.';
}

// ------------------------------------------------------------- query strings

/** Serialise a query object, dropping undefined/null/empty values. */
export function buildQuery(
  query: Record<string, string | number | boolean | undefined | null>,
): string {
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === null) continue;
    const asString = String(value);
    if (asString === '') continue;
    params.set(key, asString);
  }
  const serialised = params.toString();
  return serialised ? `?${serialised}` : '';
}
