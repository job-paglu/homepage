/**
 * The HTTP boundary vocabulary: the JSON envelope, the closed set of 14 error
 * codes, request ids, and the body reader.
 *
 * Ported from backend/internal/httpx. It knows nothing about jobs, D1 or
 * Cloudflare Access, so every other server module can depend on it.
 *
 * The one rule that matters: `message` and `details` cross the wire, `cause`
 * never does. A D1 error, a SQL fragment or a stack frame reaches the log and
 * stops there (contract section 1.2).
 */

// ------------------------------------------------------------------ the codes

/**
 * Contract section 6, transcribed. The set is CLOSED: clients switch on the
 * code, so adding a member is an API change that belongs in the contract first.
 *
 * `INVALID_CREDENTIALS`, `CSRF_INVALID` and `RATE_LIMITED` are retained even
 * though this implementation can no longer emit the first two — authentication
 * is Cloudflare Access, so there is no password to get wrong and no cookie of
 * ours to forge. They stay because the set is closed by the contract and
 * `frontend/src/lib/types.ts` still lists them; deleting them here would make
 * the two files disagree for no gain.
 */
export const ERROR_STATUS = {
  BAD_REQUEST: 400,
  UNAUTHORIZED: 401,
  INVALID_CREDENTIALS: 401,
  FORBIDDEN: 403,
  CSRF_INVALID: 403,
  JOB_NOT_FOUND: 404,
  NOT_FOUND: 404,
  METHOD_NOT_ALLOWED: 405,
  SLUG_CONFLICT: 409,
  PAYLOAD_TOO_LARGE: 413,
  VALIDATION_FAILED: 422,
  RATE_LIMITED: 429,
  INTERNAL_ERROR: 500,
  SERVICE_UNAVAILABLE: 503,
} as const satisfies Record<string, number>;

export type ErrorCode = keyof typeof ERROR_STATUS;

export const JSON_CONTENT_TYPE = 'application/json; charset=utf-8';

/** Contract section 1: the request body cap. */
export const MAX_BODY_BYTES = 64 * 1024;

/** Cache directives from contract section 3, in one place. */
export const CACHE = {
  list: 'public, max-age=0, s-maxage=60, stale-while-revalidate=300',
  detail: 'public, max-age=0, s-maxage=300, stale-while-revalidate=600',
  /** Every /api/admin/* response. Admin data cached anywhere is a disclosure. */
  none: 'no-store',
} as const;

// ------------------------------------------------------------------- ApiError

/** An error that already knows how it should look to a client. */
export class ApiError extends Error {
  readonly code: ErrorCode;
  readonly details?: Record<string, string>;
  readonly retryAfter?: number;
  /**
   * The internal cause. Named `reason` rather than assigned to the standard
   * `Error.cause`, for two reasons: it is never serialised, and the two-argument
   * `Error` constructor needs the ES2022 lib, which this file otherwise does not.
   * NOTHING may copy this into a response body.
   */
  readonly reason?: unknown;

  constructor(
    code: ErrorCode,
    message: string,
    options?: { details?: Record<string, string>; retryAfter?: number; cause?: unknown },
  ) {
    super(message);
    this.name = 'ApiError';
    this.code = code;
    if (options?.details !== undefined) this.details = options.details;
    if (options?.retryAfter !== undefined) this.retryAfter = options.retryAfter;
    if (options?.cause !== undefined) this.reason = options.cause;
  }

  get status(): number {
    return ERROR_STATUS[this.code];
  }
}

/**
 * Constructors. Messages are written for a human reading a UI: no SQL, no file
 * path, no hostname, and never a value the caller supplied.
 */
export const errors = {
  badRequest: (message?: string) =>
    new ApiError('BAD_REQUEST', message ?? 'The request could not be understood.'),
  unauthorized: () => new ApiError('UNAUTHORIZED', 'You are not signed in.'),
  forbidden: (message?: string) =>
    new ApiError('FORBIDDEN', message ?? 'You do not have permission to do that.'),
  jobNotFound: () => new ApiError('JOB_NOT_FOUND', 'That job could not be found.'),
  notFound: () => new ApiError('NOT_FOUND', 'That resource could not be found.'),
  methodNotAllowed: () =>
    new ApiError('METHOD_NOT_ALLOWED', 'That method is not allowed on this resource.'),
  slugConflict: () => new ApiError('SLUG_CONFLICT', 'That URL slug is already in use.'),
  payloadTooLarge: () => new ApiError('PAYLOAD_TOO_LARGE', 'The request body is too large.'),
  validationFailed: (details: Record<string, string>) =>
    new ApiError('VALIDATION_FAILED', 'One or more fields are invalid.', { details }),
  /** Retry-After is floored at 1: `Retry-After: 0` tells a client to hammer. */
  rateLimited: (retryAfterSeconds: number) =>
    new ApiError('RATE_LIMITED', 'Too many attempts. Please wait and try again.', {
      retryAfter: Math.max(1, Math.trunc(retryAfterSeconds)),
    }),
  internal: (cause: unknown) =>
    new ApiError('INTERNAL_ERROR', 'Something went wrong on our side. Please try again.', { cause }),
  serviceUnavailable: (cause?: unknown) =>
    new ApiError(
      'SERVICE_UNAVAILABLE',
      'The service is temporarily unavailable. Please try again shortly.',
      { cause },
    ),
} as const;

// ---------------------------------------------------------------- request ids

/** Crockford-ish base32: case-insensitive and readable off a screenshot. */
const ID_ALPHABET = '0123456789ABCDEFGHJKMNPQRSTVWXYZ';

/** A fresh 80-bit id. Not a UUID and does not need to be — it only has to be
 *  unique enough that two concurrent requests do not collide in a log search. */
export function newRequestId(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(10));
  let out = '';
  for (const byte of bytes) out += ID_ALPHABET[byte % 32] ?? '0';
  return out;
}

/**
 * Accept an inbound `X-Request-Id` so a trace can span the page render and the
 * API — but only after sanitising. This value lands in log lines, and an
 * attacker-controlled newline in a log line forges log entries.
 */
function sanitiseRequestId(raw: string | null): string | null {
  if (raw === null) return null;
  const value = raw.trim();
  if (value.length < 8 || value.length > 64) return null;
  return /^[A-Za-z0-9_-]+$/.test(value) ? value : null;
}

// ------------------------------------------------------------------- writers

function withStandardHeaders(headers: HeadersInit | undefined, requestId: string): Headers {
  const out = new Headers(headers);
  out.set('Content-Type', JSON_CONTENT_TYPE);
  out.set('X-Request-Id', requestId);
  return out;
}

/** `{"data": …}`. */
export function jsonData(
  data: unknown,
  requestId: string,
  options?: { status?: number; headers?: HeadersInit },
): Response {
  return new Response(JSON.stringify({ data }), {
    status: options?.status ?? 200,
    headers: withStandardHeaders(options?.headers, requestId),
  });
}

/** `{"data": [...], "meta": {...}}`. Never a bare array (contract section 1.1). */
export function jsonCollection(
  data: readonly unknown[],
  meta: unknown,
  requestId: string,
  options?: { headers?: HeadersInit },
): Response {
  return new Response(JSON.stringify({ data, meta }), {
    status: 200,
    headers: withStandardHeaders(options?.headers, requestId),
  });
}

/** 204 with no body at all (contract section 6). */
export function noContent(requestId: string, headers?: HeadersInit): Response {
  const out = new Headers(headers);
  out.set('X-Request-Id', requestId);
  return new Response(null, { status: 204, headers: out });
}

/**
 * The error envelope for any thrown value.
 *
 * An `ApiError` decides the code and the client-visible message. Anything else
 * is a bug that reached the boundary and becomes a content-free 500 — the cause
 * is logged, never serialised.
 */
export function failure(err: unknown, requestId: string): Response {
  const apiError = err instanceof ApiError ? err : errors.internal(err);

  const logLine = {
    request_id: requestId,
    code: apiError.code,
    status: apiError.status,
    message: apiError.message,
    // The ONLY place the internal cause is rendered. It goes to the log and
    // stops there.
    cause: apiError.reason instanceof Error ? apiError.reason.stack : String(apiError.reason ?? ''),
  };
  if (apiError.status >= 500) console.error('request failed', logLine);
  else console.warn('request rejected', logLine);

  const headers = withStandardHeaders({ 'Cache-Control': CACHE.none }, requestId);
  if (apiError.retryAfter !== undefined) headers.set('Retry-After', String(apiError.retryAfter));

  return new Response(
    JSON.stringify({
      error: {
        code: apiError.code,
        message: apiError.message,
        // `details` accompanies VALIDATION_FAILED and nothing else.
        ...(apiError.code === 'VALIDATION_FAILED' && apiError.details
          ? { details: apiError.details }
          : {}),
        request_id: requestId,
      },
    }),
    { status: apiError.status, headers },
  );
}

/**
 * Wrap a handler so every route gets a request id, a catch-all error envelope
 * and an `X-Request-Id` header without repeating any of it.
 */
export async function respond(
  request: Request,
  run: (requestId: string) => Promise<Response>,
): Promise<Response> {
  const requestId = sanitiseRequestId(request.headers.get('X-Request-Id')) ?? newRequestId();
  try {
    const response = await run(requestId);
    if (!response.headers.has('X-Request-Id')) response.headers.set('X-Request-Id', requestId);
    return response;
  } catch (err) {
    return failure(err, requestId);
  }
}

/** `ALL` export for every route file, so a wrong method is 405 and not 404. */
export function methodNotAllowed(request: Request): Promise<Response> {
  return respond(request, () => Promise.reject(errors.methodNotAllowed()));
}

// ------------------------------------------------------------------ body read

/**
 * Read and parse a JSON request body.
 *
 * The size cap is enforced on BYTES, via arrayBuffer, not on `text().length` —
 * that counts UTF-16 units and would let a body of multi-byte characters exceed
 * the cap. Unknown fields are accepted and ignored (contract section 1) so a
 * newer admin UI can post a field this API does not know yet.
 *
 * `maxBytes` defaults to the contract's 64 KiB admin cap. The webhook ingestion
 * route passes a larger cap because a batch of up to 50 jobs is legitimately
 * bigger than one admin form submission — the ONE knob that differs, so the
 * parse/type/empty rules stay identical across every JSON route.
 */
export async function readJsonBody(
  request: Request,
  maxBytes: number = MAX_BODY_BYTES,
): Promise<Record<string, unknown>> {
  const contentType = request.headers.get('Content-Type');
  if (contentType !== null && contentType.trim() !== '') {
    const mediaType = contentType.split(';')[0]?.trim().toLowerCase() ?? '';
    if (mediaType !== 'application/json') {
      throw errors.badRequest('Content-Type must be application/json.');
    }
  }

  const declared = Number(request.headers.get('Content-Length') ?? '');
  if (Number.isFinite(declared) && declared > maxBytes) throw errors.payloadTooLarge();

  const buffer = await request.arrayBuffer();
  if (buffer.byteLength > maxBytes) throw errors.payloadTooLarge();
  if (buffer.byteLength === 0) throw errors.badRequest('A JSON request body is required.');

  let parsed: unknown;
  try {
    parsed = JSON.parse(new TextDecoder().decode(buffer));
  } catch (cause) {
    // The parser's message can quote the body, so it is the cause, not the
    // message.
    throw new ApiError('BAD_REQUEST', 'The request body is not valid JSON.', { cause });
  }

  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    throw errors.badRequest('The request body must be a JSON object.');
  }
  return parsed as Record<string, unknown>;
}
